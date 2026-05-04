# RCABench v2 评估器 — 判定逻辑

> 本文是新版评估器的**设计契约**。当前 `matcher.py` / `evaluator.py` /
> `processer/rcabench.py` 的实现仍是上一版，待与本 README 对齐后再改动。

聚合到 benchmark 维度的逻辑在
`src/rcabench_platform/v3/sdk/llm_eval/eval/processer/rcabench.py`
的 `RCABenchProcesser.calculate_metrics`；单 case 评分核在本目录。

## 模块构成

| 文件 | 职责 |
|---|---|
| `schema.py` | Agent 输出契约 `AgentRCAOutput`：`root_causes` + `propagation`，每条 `RootCauseClaim` 至少 1 条 evidence |
| `ground_truth.py` | 从 `injection.json` 提取 `GTFault` 列表（`service` + `fault_kind`，`direction_*` / `method` 仍保留为可选诊断字段，但**不再参与判定**） |
| `fault_kind.py` | 26 类故障枚举 + `chaos_type` 映射 |
| `matcher.py` | 单层 `(service, fault_kind)` 多重集匹配 → P/R/F1、`exact_match`、`fault_kind_accuracy`；服务级 `node_f1` / `edge_f1`；`path_reachability`（HIT 根因→GT alarm 服务在 agent propagation 上是否可达） |
| `sql_verify.py` | DuckDB 在 case 的 parquet 目录上 re-run 每条 evidence SQL（机械验证：能跑、有行） |
| `chain_judge.py` | LLM-as-judge：**逐 evidence** 判定 "SQL 行集合是否支撑 claim 且整条链不断裂" |
| `evaluator.py` | 串起以上五步，组合 `EvaluationResultV2` |

## 单 case 流水线

```
Agent JSON ─→ AgentRCAOutput            (schema 校验失败 → parse_error，全分=0)
injection.json ─→ list[GTFault]         (无 GT → parse_error)
                ↓
   matcher.compute_outcome              (匹配 + P/R/F1 + exact + kind_accuracy)
   matcher.compute_graph_metrics        (服务级 node_f1 / edge_f1)
   sql_verify.verify_evidence (per ev)  (OK / EMPTY / SQL_ERROR)
   chain_judge.evidence_support (per ev) (LLM 判定 claim 是否被支撑 + 链是否连贯)
                ↓
        EvaluationResultV2
```

无 headline 乘积。各指标独立暴露，让用户自己看到底是哪一轴塌了。

## 匹配规则（`matcher._evaluate_pair`）

把每个 agent root_cause 和每个 GT fault 提炼成 `(service, fault_kind)` 二元组：

```python
HIT  ⇔  service_match(rc, gt) and rc.fault_kind == gt.fault_kind
```

- **服务名归一化**：小写、去 `-` 与 `_`，避免命名风格抖动。
- **网络类故障两端皆可命中**：`gt.fault_kind ∈ NETWORK_KINDS`（6 类 L3/L4 netem：delay / loss / partition / corrupt / duplicate / bandwidth）时，agent 报 `direction_src` 或 `direction_dst` 任一端都算服务匹配。理由：netem 规则装在哪一端从 trace/metrics 看不出来（双侧 RTT/丢包都能观测到），归因到任一端都合理。**HTTP 类、JVM 类、Pod / 资源 / DNS / 时钟等仍走严格 `_service_eq(rc.service, gt.service)`** ——这些故障的 span 直接挂在装规则的 service 上，归因明确。
- **不再检查 direction、method、confidence**——这些字段允许出现在 agent 输出里（schema 不强制移除），但**不影响匹配**。
  - 旧版 `WRONG_DIRECTION` / 三层评分全部废弃，匹配状态收敛为 `HIT` / `WRONG_KIND` / `MISS`。
  - 网络故障的 `direction.src/dst` 仅作为**诊断字段**写在 `FaultMatchResult`，不参与得分；同时也作为"两端可命中"的候选服务集来源（仅对 NETWORK_KINDS 生效）。
- 多故障 case：贪心一对一分配（每个 agent_rc 和每个 gt_fault 各只能用一次）。剩下的 agent_rc 是多报，剩下的 gt_fault 是漏报。多故障的 `exact_match` 仍要求多重集严格相等（`n_hit == n_agent == n_gt`），网络两端可命中只放宽单条匹配，不放宽 multiset 完整性。

## Headline 指标（每个 case 独立计算，benchmark 维度做平均）

| 指标 | 单 case 算法 | 含义 |
|---|---|---|
| `exact_match` | 二值：agent 的 `(service, fault_kind)` **多重集**是否完全等于 GT 多重集（即贪心匹配后 `n_hit == n_agent == n_gt`） | 整 case 严格通过；多故障要求条数和组合都对 |
| `precision` | `n_hit / n_agent` | 多报惩罚 |
| `recall`    | `n_hit / n_gt`    | 漏报惩罚 |
| `f1`        | 二者调和平均 | 综合定位质量 |
| `fault_kind_accuracy` | service 命中的子集中 kind 也对的比例。分母 = 服务匹配上某个 GT 的 agent_rc 数（HIT + WRONG_KIND）；分子 = HIT 数。**分母为 0 的 case 直接从该指标的 benchmark 均值中剔除**（用 `kind_accuracy_denom` 子分母） | "定位失败 vs 分类失败"诊断 |
| `sql_executable_rate` | `#OK / #all_evidence`（root_causes + propagation 的 evidence 全算） | 机械可执行率 |
| `evidence_support_rate` | **case 内**先按 evidence 取均值（每条 evidence judge 出 0/1），**再**在 benchmark 维度按 case 取均值 | claim 与 SQL 行集合 + chain 连贯性 |
| `node_f1` | agent 服务集合 vs `causal_graph.json` 服务集合的 F1（含 root_cause + propagation 端点） | 传播图节点完整性 |
| `edge_f1` | agent propagation `(from, to)` 集合 vs GT 边集合的 F1 | 传播图边完整性 |
| `path_reachability` | 二值：是否存在某个 **HIT** 的 `agent.root_causes[i]`，其 `service` 沿 agent 自报的 `propagation` 边能走到 `causal_graph.alarm_nodes` 对应的某个服务（路径长度 0 也算）。无 HIT 即 0；GT 无 causal_graph 或无 alarm 服务时记 None，从均值剔除（用 `path_reachability_denom` 子分母） | 至少一条根因→告警的因果链是连通的（补 `edge_f1` 看不出连通性的盲区） |

聚合维度（在 `RCABenchProcesser.calculate_metrics`）一律对**全部样本**取算术平均，分母 = `total_samples`，**`fault_kind_accuracy` 与 `path_reachability` 例外**（前者剔除分母为 0 的 case，单独跟踪 `kind_accuracy_denom`；后者剔除无 GT 图 / 无 alarm 服务的 case，单独跟踪 `path_reachability_denom`）。parse 失败 / 缺 case dir 的样本对其他指标计 0 进平均，避免高 JSON 失败率的 agent 偷分。

`evidence_support_rate` 的双层平均（case 内→benchmark）让 evidence 多的 case 不会权重碾压其他 case。case 内全部 evidence judge 都失败时，该 case 该指标记 0 计入 benchmark 均值；**单条 evidence 的 judge 失败不污染其他 evidence 的得分**（粒度收到 evidence 级，比旧版 chain 级容错好）。

## 诊断字段

| 字段 | 含义 |
|---|---|
| `parse_errors` | Agent 输出 JSON 解析失败或不符合 schema 的 case 数 |
| `zero_evidence_outputs` | `per_evidence` 为空的 case 数（agent 没产出任何 evidence） |
| `judge_failed` | LLM judge 调用本身失败（网络等环境问题）的 evidence 总数 |
| `kind_accuracy_denom` | `fault_kind_accuracy` 实际参与平均的 case 数（用于评估该指标可信度） |
| `per_fault` (per case) | 每个 GT fault 的匹配状态 + 对位的 agent_rc index |
| `overclaim_indices` (per case) | agent 多报的 root_cause 下标 |
| `per_evidence` (per case) | 每条 evidence 的 SQL 状态（OK/EMPTY/SQL_ERROR）+ 行数 + judge 0/1 |

`overclaim_rate` 不再独立暴露——`precision` 已经覆盖（`overclaim_rate ≈ 1 - precision`）。

## SQL 可执行率细节（`sql_verify.py`）

case 目录下每个 `*.parquet` 注册成同名 view，agent SQL 原样跑在内存 DuckDB 上：

| 状态 | 含义 |
|---|---|
| `OK` | 跑成功且至少 1 行 |
| `EMPTY` | 跑成功但 0 行 |
| `SQL_ERROR` | DuckDB 解析/执行抛错 |

只验证"能跑且有行"。是否在事故时间窗内、是否真的支撑 claim，归 `evidence_support_rate` 管。

## Evidence Support 细节（`chain_judge.py`）

**粒度**：逐 evidence 调用 LLM judge，而不是整条 chain 一次性判定。

**输入**：单条 evidence 的 `claim` + 对应 SQL 的样本行预览（最多 N 行）+ 这条 evidence 在 chain 里的位置（属于哪个 root_cause / propagation 节点）。

**输出**：`{ supported: bool, reason: str }`，benchmark 维度按 evidence 数取平均。

**判定基准**：
- `supported=True`：行集合不矛盾 claim，且 claim 与上下游 evidence 在逻辑上连得上。
- `supported=False`：行集合反驳 claim、或 claim 是孤立断言、或它的上游 evidence 缺失导致这条 evidence 在链里悬空。

**对 GT 不可见**：judge 不接触 GT，避免与 `f1` / `exact_match` 重复打分。GT 对齐由匹配层负责，evidence_support 只看内部一致性。

## 调试入口

- 单 case 完整打分：`sample.meta["eval_v2"]`（`EvaluationResultV2.model_dump`），含 `per_fault`、`overclaim_indices`、`per_evidence`、`propagation_metrics`、各 evidence 的 judge 输出。
- LLM judge 原始返回：`sample.judged_response`（聚合每条 evidence 的 raw response）。
- 看 prompt：`LLM_EVAL_LOG_LEVEL=DEBUG` 或 tail `$LLM_EVAL_LOG_DIR/llm_eval.log`。
