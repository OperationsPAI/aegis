# Inject Loop 设计说明（当前 Aegis 集群）

本文档描述当前仓库里 inject loop 的目标、约束、执行策略，以及为什么这样设计。

## 1. 目标

当前 inject loop 的目标不是“尽量多提交成功”，而是为每个系统持续生成**覆盖面合理、可回放、可评分**的故障注入轮次数据。

具体来说，我们希望：

- 每个系统独立推进自己的 fault campaign
- 每轮先决定 `chaos_type` 配额，再选具体 candidate
- 避免因为 backend dedup 机制而产生“看起来提交成功、实际上没有 trace”的空气轮次
- 用真实运行中的 trace / 当前注入库存来约束下一轮，而不是只看本地历史
- 对当前集群明确不支持的故障类型直接硬禁用

## 2. 总体架构

### 2.1 每个系统一个独立 campaign

每个系统对应一个独立状态目录：

- `ralph/examples/inject-loop/campaigns/trainticket/`
- `ralph/examples/inject-loop/campaigns/teastore/`
- `ralph/examples/inject-loop/campaigns/hs/`
- `ralph/examples/inject-loop/campaigns/mm/`
- `ralph/examples/inject-loop/campaigns/sn/`
- `ralph/examples/inject-loop/campaigns/otel-demo/`
- `ralph/examples/inject-loop/campaigns/sockshop/`

每个目录维护：

- `campaign.json`：系统、namespace、project、round 策略
- `CODEX.md`：单系统 loop prompt
- `progress.txt`：追加式进展日志
- `live_mix.json`：当前系统支持故障空间 + 已提交/已运行状态快照

这样设计是为了支持**分系统并行**：一个 Codex loop 只负责一个系统，状态互不污染。

### 2.2 Ralph 负责外层 fresh-context loop

`ralph/ralph.sh --tool codex` 负责一轮一轮拉起新的 Codex 实例。  
单次迭代只做一件事：

- 要么回收并评分上一轮
- 要么规划并提交下一轮

这样可以避免长上下文污染，也方便多系统并行运行。

## 3. 轮次数据模型

每个系统的实验目录例如 `experiments/trainticket-loop/` 下维护：

- `candidates_round<N>.json`
- `runs_round<N>.jsonl`
- `terminals_round<N>.tsv`

其中：

- `candidates_round<N>.json` 保存本轮候选与策略说明
- `runs_round<N>.jsonl` 记录每次提交结果、`trace_id`、`ns`
- `terminals_round<N>.tsv`/回收逻辑记录 trace 的终态

## 4. 当前核心策略

### 4.1 先定 chaos_type budget，再定 candidate

这是最重要的策略修正。

过去的问题是：

- 先挑“容易提交成功”的 candidate
- 再把换 `PodFailure / PodKill / ContainerKill` 当成多样化

这会导致：

- pod-family 严重过量
- network / dns / stress / time / jvm / http 等类别长期缺失

现在的规则是：

1. 先看系统支持哪些 `chaos_type`
2. 再看最近几轮和当前 live mix 哪些类型被过度使用/完全没用过
3. 先分配每轮的类型预算
4. 最后才在每个类型内部挑具体 `(app, chaos_type, params)` candidate

### 4.2 duration 固定，不再作为常规 dedup 手段

当前 loop 已改为：

- 轮级别固定 `defaults.duration`
- 正常轮次禁止 candidate 级 `duration_override`

原因：

- 之前把改 duration 当主要 dedup-bypass 手段，会把“避免 dedup”和“扩大故障空间覆盖”两件事混在一起
- duration 在 Aegis 里是分钟，不是秒，容易造成长时间挂起和误判

现在 dedup-bypass 的优先方式是：

- 改 `params`
- 改 `interval`
- 改 `pre_duration`
- 或在已经分配好的非 pod 类型配额内切换真实不同的 `chaos_type`

### 4.3 实时 mix 优先于本地历史

现在规划新一轮之前，先跑：

```bash
python3 experiments/lib/live_mix.py --campaign <campaign.json> --refresh-supported
```

`live_mix.py` 会产出：

- 当前系统允许的 supported candidate 空间
- 本地 loop 已提交过哪些类型
- 这些 trace 当前是 `Running / Completed / Failed`
- 哪些类型完全缺失

这样做的原因是：

- 只看本地最近几轮，无法知道后台现在是否已经积压了很多同类 Running trace
- live mix 能更真实反映“现在这个系统还缺什么”

### 4.4 强校验后才能提交

提交前必须经过：

```bash
python3 experiments/lib/validate_round.py ...
```

当前 validator 会强制检查：

- pod-family 上限
- 最近历史缺失类型的 floor
- round 内 duplicate fingerprint
- 固定 duration 约束
- campaign 排除类型约束

也就是说，策略不是“靠 agent 自觉遵守”，而是“违反策略直接不给提交”。

## 5. 当前集群的 HTTP 故障策略

### 5.1 结论

在当前集群里，`HTTP*` 故障类型被**硬禁用**。

### 5.2 原因

当前集群属于 byte-cluster / IPVLAN 类网络环境，Chaos Mesh 的 HTTP/tproxy 注入在这类环境下不可用。即使平台能枚举出 HTTP 路由候选，实际注入也不可靠。

因此我们不再把 HTTP 类故障视为“可选但不推荐”，而是视为“当前集群不支持”。

### 5.3 具体实现

当前仓库已经做了三层防护：

1. `campaign.json` 默认包含：

```json
"excluded_chaos_types": ["HTTP*"]
```

2. `live_mix.py` 刷新 `_supported_candidates.json` 时会先按 `excluded_chaos_types` 过滤
3. `validate_round.py` 会拒绝任何仍然包含 `HTTP*` 的 round
4. `submit_dual.py` 增加了最终保险：如果 round 里仍有 `HTTP*` candidate，会把它们记成 `submit.error.hard_disabled_chaos_type:*`，而不是实际提交

因此现在 HTTP 类故障已经不是“策略上不建议”，而是“工具链层面禁止进入执行面”。

## 6. 并行策略

当前目标是**分系统并行**，不是在同一系统里堆很多混杂状态。

做法是：

- 一个系统一个 campaign state dir
- 一个 Codex loop 只处理一个系统
- 用 `launch_parallel.py` 按 manifest 同时启动多个系统

好处：

- `progress.txt`、`live_mix.json`、round 文件不会互相覆盖
- 可以独立暂停 signal 很差的系统
- 可以分别观察各系统当前最缺的故障类别

## 7. 系统映射与 namespace 策略

当前代码里已经补了两类映射：

### 7.1 backend system alias

部分系统本地简称和 Aegis 后端系统名不一致，例如：

- 本地 loop 用 `mm`
- Aegis backend 实际系统名是 `media`

因此 campaign 中加入了 `backend_system` 字段，供 `aegisctl` 调用使用。

### 7.2 namespace 自动解析

初始化 campaign 时会根据当前集群实际 namespace 和 pod 存活情况优先选择“活着的 namespace”，例如：

- `teastore -> tea0`
- `sockshop -> sockshop10`

这能避免把 campaign 指到一个存在但没有工作负载的 namespace。

## 8. 暂停策略

不是所有系统都值得一直迭代。

当系统出现以下情况时，应暂停：

- 连续多轮信号很低且没有稳定模式
- 环境失败占主导
- namespace 池/集群容量成为主要瓶颈
- 到达轮次上限

暂停后应：

- 更新 `campaign.json` 的 `status`
- 在 loop 目录写 `PAUSED.md`
- 在 `progress.txt` 追加原因

## 9. 当前推荐执行流程

单系统一次正常迭代：

1. 回收上一轮 trace 并评分
2. 刷新 `live_mix.json`
3. 根据 live mix 和历史分配下一轮 `chaos_type` budget
4. 在每个类型内部挑具体 candidate
5. 运行 `validate_round.py`
6. 通过后调用 `submit_dual.py`
7. 记录 runs / progress

多系统运行时：

1. 先确保各系统 `campaign.json` 正确
2. 用 `launch_parallel.py` 按 manifest 启动多个 Codex loops
3. 对 signal 差或环境不稳定的系统单独暂停

## 10. 一句话总结

当前 inject loop 的设计原则是：

**按系统拆分、按类型预算、按实时 mix 调度、固定 duration、严防 dedup，并把当前集群不支持的 HTTP 故障从工具链层面直接禁掉。**
