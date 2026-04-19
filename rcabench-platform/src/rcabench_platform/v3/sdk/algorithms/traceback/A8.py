from collections import defaultdict
from collections.abc import Iterable
from dataclasses import dataclass
from enum import auto
from pprint import pformat
from queue import Queue

import networkx as nx
import polars as pl

from .....compat import StrEnum
from ...datasets.rcabench import rcabench_fix_injection
from ...graphs.networkx.neo4j import export_networkx_to_neo4j
from ...graphs.sdg.defintion import SDG, DepEdge, DepKind, PlaceKind, PlaceNode
from ...graphs.sdg.statistics import STAT_PREFIX, calc_statistics
from ...logging import logger, timeit
from ...utils.env import debug
from ...utils.serde import load_json
from ..spec import Algorithm, AlgorithmAnswer, AlgorithmArgs
from ._common import build_sdg_with_cache


class TraceBackA8(Algorithm):
    def needs_cpu_count(self) -> int | None:
        return 8

    @timeit()
    def __call__(self, args: AlgorithmArgs) -> list[AlgorithmAnswer]:
        assert_dataset(args)

        if debug():
            _print_injection(args)

        sdg = build_sdg_with_cache(args)

        calc_statistics(sdg)

        detect_anomalies(sdg)

        acg = build_acg(sdg)

        if debug():
            export_networkx_to_neo4j(acg)

        rcc_list = find_root_cause_candidates(acg, sdg)

        service_names = unify_to_service_candidates(sdg, rcc_list)

        answers = []
        for rank, service_name in enumerate(service_names, start=1):
            answers.append(AlgorithmAnswer(level="service", name=service_name, rank=rank))

        if debug():
            for rank, service_name in enumerate(service_names, start=1):
                logger.debug(f"RCC {rank:>2}: {service_name}")

        return answers


def assert_dataset(args: AlgorithmArgs) -> None:
    datasets = ["rcabench", "rcaeval_re2_tt"]
    if not any(args.dataset.startswith(ds) for ds in datasets):
        raise NotImplementedError


def _print_injection(args: AlgorithmArgs) -> None:
    injection_path = args.input_folder / "injection.json"
    if injection_path.exists():
        injection = load_json(path=injection_path)
        rcabench_fix_injection(injection)
        logger.debug(f"found injection:\n{pformat(injection)}")


# --- Anomaly Detection ---


class AnomalyKey(StrEnum):
    error_rate = auto()
    latency = auto()
    qpm = auto()

    cpu = auto()
    memory = auto()
    jvm_gc_duration = auto()
    restart = auto()

    forward_call_prob = auto()
    backward_call_prob = auto()


class AnomalyKind(StrEnum):
    up = auto()
    down = auto()


@dataclass(kw_only=True, slots=True, frozen=True)
class Anomaly:
    key: AnomalyKey
    kind: AnomalyKind
    score: float


def relative_diff(a: float, b: float) -> float:
    if a == 0:
        if b == 0:
            return 0.0
        else:
            return 1.0
    return (b - a) / abs(a)


def detect_node_anomalies(sdg: SDG, node: PlaceNode) -> list[Anomaly]:
    ans: list[Anomaly] | None = node.data.get("alg.anomalies")
    if ans is not None:
        return ans
    else:
        ans = []

    keys = {
        "error_rate": AnomalyKey.error_rate,
        "function_error_rate.mean": AnomalyKey.error_rate,
        "latency": AnomalyKey.latency,
        "latency_p50": AnomalyKey.latency,
        "latency_p90": AnomalyKey.latency,
        "qpm": AnomalyKey.qpm,
        "cpu_usage": AnomalyKey.cpu,
        "memory_usage": AnomalyKey.memory,
        "jvm_gc_duration": AnomalyKey.jvm_gc_duration,
        "restart_count": AnomalyKey.restart,
    }

    for key in keys:
        normal_value: float | None = node.data.get(f"{STAT_PREFIX[0]}.{key}")
        anomal_value: float | None = node.data.get(f"{STAT_PREFIX[1]}.{key}")

        if normal_value is None or anomal_value is None:
            continue

        rel_diff = relative_diff(normal_value, anomal_value)
        if rel_diff > 0.5:
            anomaly = Anomaly(key=keys[key], kind=AnomalyKind.up, score=abs(rel_diff))
        elif rel_diff < -0.5:
            anomaly = Anomaly(key=keys[key], kind=AnomalyKind.down, score=abs(rel_diff))
        else:
            anomaly = None

        if anomaly is not None:
            ans.append(anomaly)

            logger.debug(
                "detected anomaly: {}, node: `{}`, {}: {} -> {}",
                anomaly,
                node.uniq_name,
                key,
                normal_value,
                anomal_value,
            )

    node.data["alg.anomalies"] = ans
    return ans


def detect_edge_anomalies(sdg: SDG, edge: DepEdge) -> list[Anomaly]:
    ans: list[Anomaly] | None = edge.data.get("alg.anomalies")
    if ans is not None:
        return ans
    else:
        ans = []

    src = sdg.get_node_by_id(edge.src_id)
    dst = sdg.get_node_by_id(edge.dst_id)
    edge_uniq_name = f"{src.uniq_name} --({edge.kind})-> {dst.uniq_name}"

    keys = {
        "forward_call_prob": AnomalyKey.forward_call_prob,
        "backward_call_prob": AnomalyKey.backward_call_prob,
    }

    for key in keys:
        normal_value: float | None = edge.data.get(f"{STAT_PREFIX[0]}.{key}")
        anomal_value: float | None = edge.data.get(f"{STAT_PREFIX[1]}.{key}")

        if normal_value is None or anomal_value is None:
            continue

        rel_diff = relative_diff(normal_value, anomal_value)
        if rel_diff > 0.5:
            anomaly = Anomaly(key=keys[key], kind=AnomalyKind.up, score=abs(rel_diff))
        elif rel_diff < -0.5:
            anomaly = Anomaly(key=keys[key], kind=AnomalyKind.down, score=abs(rel_diff))
        else:
            anomaly = None

        if anomaly is not None:
            ans.append(anomaly)

            logger.debug(
                "detected anomaly: {}, edge: `{}`, {}: {} -> {}",
                anomaly,
                edge_uniq_name,
                key,
                normal_value,
                anomal_value,
            )

    edge.data["alg.anomalies"] = ans
    return ans


@timeit(log_args=False)
def detect_anomalies(sdg: SDG) -> None:
    for node in sdg.iter_nodes():
        detect_node_anomalies(sdg, node)

    for edge in sdg.iter_edges():
        detect_edge_anomalies(sdg, edge)


# --- Anomaly Causal Graph ---


class CausalEdgeKind(StrEnum):
    user_impact = auto()
    """SLI -> USER"""

    error_rate_up = auto()
    """function -> function"""

    latency_up = auto()
    """function -> function"""

    call_fault = auto()
    """function -> function"""

    call_seq_abort = auto()
    """function -> function"""

    server_failure = auto()
    """
    (service|pod|container) -> function
    container -> pod -> service
    """

    server_exhausion = auto()
    """
    (pod|container) -> function
    container -> pod -> service
    """

    cpu_up = auto()
    """container -> pod"""

    memory_up = auto()
    """container -> pod"""


@dataclass(kw_only=True, slots=True)
class CausalEdge:
    kind: CausalEdgeKind
    sdg_edge_id: int | None


def build_acg(sdg: SDG) -> nx.MultiDiGraph:
    acg = nx.MultiDiGraph()

    for node in sdg.iter_nodes():
        anomalies = detect_node_anomalies(sdg, node)
        if not anomalies:
            continue

        if len(anomalies) == 1:
            a = anomalies[0]

            if a.key == AnomalyKey.qpm:
                continue

            if a.kind == AnomalyKind.down:
                continue

        add_node(acg, sdg, node)

    infer_resource_propagation(acg, sdg)

    infer_function_propagation(acg, sdg)

    infer_server_fault(acg, sdg)

    infer_call_fault(acg, sdg)

    infer_user_impact(acg, sdg)

    return acg


def add_node(acg: nx.MultiDiGraph, sdg: SDG, node: PlaceNode) -> None:
    anomalies = detect_node_anomalies(sdg, node)

    tags = {}
    for i, a in enumerate(anomalies):
        tags[f"anomalies[{i}]"] = f"{a.key}:{a.kind}:{a.score:.3f}"

    acg.add_node(
        node.id,
        name=node.self_name,
        kind=node.kind,
        uniq_name=node.uniq_name,
        len_anomalies=len(anomalies),
        **tags,
    )


def add_causal_edge(
    acg: nx.MultiDiGraph,
    sdg: SDG,
    u: PlaceNode,
    v: PlaceNode,
    kind: CausalEdgeKind,
    *,
    sdg_edge_id: int | None,
) -> None:
    logger.debug("acg edge: ({}) `{}` -> `{}`", kind, u.uniq_name, v.uniq_name)

    if u.id not in acg.nodes:
        add_node(acg, sdg, u)

    if v.id not in acg.nodes:
        add_node(acg, sdg, v)

    acg.add_edge(u.id, v.id, key=kind, kind=kind, sdg_edge_id=sdg_edge_id)


def acg_iter_nodes(acg: nx.MultiDiGraph, sdg: SDG) -> Iterable[PlaceNode]:
    for node_id in acg.nodes:
        assert isinstance(node_id, int)

        if node_id == USER_NODE_ID:
            continue

        node = sdg.get_node_by_id(node_id)
        yield node


USER_NODE_ID = 999999999


def is_user_interface(sdg: SDG, node: PlaceNode) -> bool:
    dataset: str = sdg.data["dataset"]
    if dataset.startswith("rcabench"):
        return node.kind == PlaceKind.function and node.self_name.startswith("ts-ui-dashboard")
    if dataset.startswith("rcaeval"):
        top_op_names: set[str] = sdg.data["top_op_names"]
        return node.kind == PlaceKind.function and node.self_name in top_op_names
    raise NotImplementedError


def is_anomal_service(sdg, node: PlaceNode) -> bool:
    if node.kind != PlaceKind.service:
        return False

    if node.self_name == "loadgenerator":
        return False

    anomalies = detect_node_anomalies(sdg, node)
    if not anomalies:
        return False

    major_keys = [
        AnomalyKey.error_rate,
        AnomalyKey.latency,
        AnomalyKey.cpu,
        AnomalyKey.memory,
        AnomalyKey.restart,
    ]
    for anomaly in anomalies:
        if anomaly.key in major_keys and anomaly.kind == AnomalyKind.up:
            return True
    return False


def infer_user_impact(acg: nx.MultiDiGraph, sdg: SDG) -> None:
    assert not sdg.has_node(USER_NODE_ID)
    acg.add_node(USER_NODE_ID, name="USER", kind="USER")

    for node in acg_iter_nodes(acg, sdg):
        if is_user_interface(sdg, node) or is_anomal_service(sdg, node):
            u, v, kind = node.id, USER_NODE_ID, CausalEdgeKind.user_impact
            logger.debug("acg edge: ({}) `{}` -> `USER`", kind, node.uniq_name)
            acg.add_edge(u, v, key=kind, kind=kind)


def has_same_anomaly(sdg: SDG, lhs: PlaceNode, rhs: PlaceNode, key: AnomalyKey, kind: AnomalyKind) -> bool:
    lhs_anomalies = detect_node_anomalies(sdg, lhs)
    rhs_anomalies = detect_node_anomalies(sdg, rhs)
    for a in lhs_anomalies:
        if a.key == key and a.kind == kind:
            for b in rhs_anomalies:
                if b.key == key and b.kind == kind:
                    return True
    return False


def has_anomaly(
    sdg: SDG,
    place: PlaceNode | DepEdge,
    key: AnomalyKey,
    kind: AnomalyKind,
    score: float | None = None,
) -> bool:
    if isinstance(place, PlaceNode):
        anomalies = detect_node_anomalies(sdg, place)
    elif isinstance(place, DepEdge):
        anomalies = detect_edge_anomalies(sdg, place)
    else:
        raise ValueError

    for anomaly in anomalies:
        if anomaly.key == key and anomaly.kind == kind:
            if score is None:
                return True
            elif anomaly.score >= score:
                return True
    return False


def infer_function_propagation(acg: nx.MultiDiGraph, sdg: SDG) -> None:
    causal_kinds = [
        (AnomalyKey.error_rate, AnomalyKind.up, CausalEdgeKind.error_rate_up),
        (AnomalyKey.latency, AnomalyKind.up, CausalEdgeKind.latency_up),
    ]

    for edge in sdg.iter_edges():
        if edge.kind not in (DepKind.calls, DepKind.includes):
            continue

        src = sdg.get_node_by_id(edge.src_id)
        dst = sdg.get_node_by_id(edge.dst_id)

        for key, kind, causality in causal_kinds:
            if has_same_anomaly(sdg, src, dst, key, kind):
                add_causal_edge(acg, sdg, dst, src, causality, sdg_edge_id=edge.id)


def infer_resource_propagation(acg: nx.MultiDiGraph, sdg: SDG) -> None:
    causal_kinds = [
        (AnomalyKey.cpu, AnomalyKind.up, CausalEdgeKind.cpu_up),
        (AnomalyKey.memory, AnomalyKind.up, CausalEdgeKind.memory_up),
    ]

    for edge in sdg.iter_edges():
        if edge.kind not in (DepKind.runs, DepKind.routes_to):
            continue

        src = sdg.get_node_by_id(edge.src_id)
        dst = sdg.get_node_by_id(edge.dst_id)

        for key, kind, causality in causal_kinds:
            if has_same_anomaly(sdg, src, dst, key, kind):
                add_causal_edge(acg, sdg, dst, src, causality, sdg_edge_id=edge.id)


def infer_server_fault(
    acg: nx.MultiDiGraph,
    sdg: SDG,
) -> None:
    for edge in sdg.iter_edges():
        if edge.kind != DepKind.calls:
            continue

        caller = sdg.get_node_by_id(edge.src_id)
        callee = sdg.get_node_by_id(edge.dst_id)

        caller_error_rate_up = has_anomaly(sdg, caller, AnomalyKey.error_rate, AnomalyKind.up)
        caller_latency_up = has_anomaly(sdg, caller, AnomalyKey.latency, AnomalyKind.up)

        if not (caller_error_rate_up or caller_latency_up):
            continue

        caller_service = find_related_service(sdg, caller)
        callee_service = find_related_service(sdg, callee)
        if caller_service is None or callee_service is None:
            continue

        if caller_service.id == callee_service.id:
            return

        service = callee_service

        fault = [
            (detect_server_failure, CausalEdgeKind.server_failure),
            (detect_server_exhaustion, CausalEdgeKind.server_exhausion),
        ]

        for detect_func, causal_edge_kind in fault:
            has_server_fault = False
            if detect_func(sdg, service):
                add_causal_edge(acg, sdg, service, caller, causal_edge_kind, sdg_edge_id=edge.id)
                add_causal_edge(acg, sdg, service, callee, causal_edge_kind, sdg_edge_id=edge.id)
                has_server_fault = True

            pods = find_related_pods(sdg, service)
            for pod in pods:
                if detect_func(sdg, pod):
                    add_causal_edge(acg, sdg, pod, service, causal_edge_kind, sdg_edge_id=edge.id)
                    add_causal_edge(acg, sdg, pod, caller, causal_edge_kind, sdg_edge_id=edge.id)
                    add_causal_edge(acg, sdg, pod, callee, causal_edge_kind, sdg_edge_id=edge.id)
                    has_server_fault = True

                containers = find_related_containers(sdg, pod)
                for container in containers:
                    if detect_func(sdg, container):
                        add_causal_edge(
                            acg,
                            sdg,
                            container,
                            pod,
                            causal_edge_kind,
                            sdg_edge_id=edge.id,
                        )
                        add_causal_edge(
                            acg,
                            sdg,
                            container,
                            caller,
                            causal_edge_kind,
                            sdg_edge_id=edge.id,
                        )
                        add_causal_edge(
                            acg,
                            sdg,
                            container,
                            callee,
                            causal_edge_kind,
                            sdg_edge_id=edge.id,
                        )
                        has_server_fault = True

            if has_server_fault:
                add_causal_edge(acg, sdg, callee, caller, causal_edge_kind, sdg_edge_id=edge.id)


def detect_server_failure(sdg: SDG, node: PlaceNode) -> bool:
    assert node.kind in (PlaceKind.service, PlaceKind.pod, PlaceKind.container)
    anomalies = detect_node_anomalies(sdg, node)

    has_restart_up = has_anomaly(sdg, node, AnomalyKey.restart, AnomalyKind.up)
    if has_restart_up:
        logger.debug(
            "detected server failure for node: `{}`, anomalies: {}",
            node.uniq_name,
            anomalies,
        )
        return True

    cpu_threshold = 0.8
    memory_threshold = 0.8

    has_cpu_drop = has_anomaly(sdg, node, AnomalyKey.cpu, AnomalyKind.down, score=cpu_threshold)
    has_memory_drop = has_anomaly(sdg, node, AnomalyKey.memory, AnomalyKind.down, score=memory_threshold)

    if has_cpu_drop and has_memory_drop:
        logger.debug(
            "detected server failure for node: `{}`, anomalies: {}",
            node.uniq_name,
            anomalies,
        )
        return True

    return False


def detect_server_exhaustion(sdg: SDG, node: PlaceNode) -> bool:
    anomalies = detect_node_anomalies(sdg, node)

    if node.kind == PlaceKind.pod:
        if detect_pod_exhaustion(sdg, node):
            logger.debug(
                "detected pod exhaustion for node: `{}`, anomalies: {}",
                node.uniq_name,
                anomalies,
            )
            return True

    elif node.kind == PlaceKind.container:
        if detect_container_exhaustion(sdg, node):
            logger.debug(
                "detected container exhaustion for node: `{}`, anomalies: {}",
                node.uniq_name,
                anomalies,
            )
            return True

    return False


def detect_pod_exhaustion(sdg: SDG, node: PlaceNode) -> bool:
    assert node.kind == PlaceKind.pod

    if has_anomaly(sdg, node, AnomalyKey.jvm_gc_duration, AnomalyKind.up, 0.5):
        return True

    return False


def detect_container_exhaustion(sdg: SDG, node: PlaceNode) -> bool:
    assert node.kind == PlaceKind.container

    if has_anomaly(sdg, node, AnomalyKey.cpu, AnomalyKind.up, 0.8):
        return True

    if has_anomaly(sdg, node, AnomalyKey.memory, AnomalyKind.up, 0.8):
        return True

    return False


def infer_call_fault(acg: nx.MultiDiGraph, sdg: SDG):
    for caller in sdg.iter_nodes():
        if caller.kind != PlaceKind.function:
            continue

        has_error_rate_up = has_anomaly(sdg, caller, AnomalyKey.error_rate, AnomalyKind.up)
        has_latency_up = has_anomaly(sdg, caller, AnomalyKey.latency, AnomalyKind.up)
        if not (has_error_rate_up or has_latency_up):
            continue

        out_edges: list[DepEdge] = []
        for edge in sdg.out_edges(caller.id):
            if edge.kind != DepKind.calls:
                continue

            if not has_anomaly(sdg, edge, AnomalyKey.forward_call_prob, AnomalyKind.down, 0.95):
                continue

            out_edges.append(edge)

        out_nodes: list[PlaceNode] = [sdg.get_node_by_id(e.dst_id) for e in out_edges]

        for edge, callee in zip(out_edges, out_nodes):
            add_causal_edge(acg, sdg, callee, caller, CausalEdgeKind.call_fault, sdg_edge_id=edge.id)
            extend_client_server_fault(acg, sdg, callee)

        topo_graph = build_call_topo_graph(caller, out_nodes)
        for lhs_id, rhs_id in topo_graph.edges:
            assert isinstance(lhs_id, int) and isinstance(rhs_id, int)
            lhs = sdg.get_node_by_id(lhs_id)
            rhs = sdg.get_node_by_id(rhs_id)
            add_causal_edge(acg, sdg, lhs, rhs, CausalEdgeKind.call_seq_abort, sdg_edge_id=None)


def extend_client_server_fault(acg: nx.MultiDiGraph, sdg: SDG, caller: PlaceNode) -> None:
    assert caller.kind == PlaceKind.function

    edges = list(sdg.out_edges(caller.id))
    if len(edges) != 1:
        return

    edge = edges[0]
    if edge.kind != DepKind.calls:
        return

    callee = sdg.get_node_by_id(edge.dst_id)

    caller_parts = caller.self_name.split(" ")
    callee_parts = callee.self_name.split(" ")
    if len(caller_parts) != 3 or len(callee_parts) != 3:
        return

    if caller_parts[2] != callee_parts[2]:
        return

    if caller_parts[1] != callee_parts[1]:
        logger.debug("detected replace-method: `{}` -> `{}`", caller.uniq_name, callee.uniq_name)

    service = find_related_service(sdg, callee)
    if service is None:
        return

    fault = [
        (detect_server_failure, CausalEdgeKind.server_failure),
        (detect_server_exhaustion, CausalEdgeKind.server_exhausion),
    ]

    for detect_func, causal_edge_kind in fault:
        has_server_fault = False
        if detect_func(sdg, service):
            has_server_fault = True

        pods = find_related_pods(sdg, service)
        for pod in pods:
            if detect_func(sdg, pod):
                has_server_fault = True

            containers = find_related_containers(sdg, pod)
            for container in containers:
                if detect_func(sdg, container):
                    has_server_fault = True

        if has_server_fault:
            add_causal_edge(acg, sdg, callee, caller, causal_edge_kind, sdg_edge_id=edge.id)


def build_call_topo_graph(caller: PlaceNode, callees: list[PlaceNode]) -> nx.DiGraph:
    g = nx.DiGraph()
    n = len(callees)

    seq_table: defaultdict[int, defaultdict[int, bool | None]] = defaultdict(lambda: defaultdict(lambda: None))

    for i in range(n - 1):
        for j in range(i + 1, n):
            lhs = callees[i]
            rhs = callees[j]

            seq = is_seq_before(caller, lhs, rhs)
            if seq is None:
                continue

            if seq:
                g.add_edge(lhs.id, rhs.id)
            else:
                g.add_edge(rhs.id, lhs.id)

            seq_table[lhs.id][rhs.id] = seq
            seq_table[rhs.id][lhs.id] = not seq

    nodes = list(g.nodes)
    for u, v in list(g.edges):
        for k in nodes:
            if seq_table[u][k] and seq_table[k][v]:
                g.remove_edge(u, v)
                break

    return g


def is_seq_before(top: PlaceNode, lhs: PlaceNode, rhs: PlaceNode) -> bool | None:
    assert top.kind == PlaceKind.function
    assert lhs.kind == PlaceKind.function
    assert rhs.kind == PlaceKind.function

    top_duration = top.indicators.get("duration")
    if top_duration is None:
        return None

    lhs_duration = lhs.indicators.get("duration")
    rhs_duration = rhs.indicators.get("duration")
    if lhs_duration is None or rhs_duration is None:
        return None

    start_time = pl.col("time").dt.timestamp("ns").cast(pl.UInt64)
    end_time = start_time.add(pl.col("value").cast(pl.UInt64))

    lf = top_duration.df.lazy().select("span_id")

    lhs_lf = lhs_duration.df.lazy().select(
        "parent_span_id",
        start_time.alias("start_time:l"),
        end_time.alias("end_time:l"),
    )

    rhs_lf = rhs_duration.df.lazy().select(
        "parent_span_id",
        start_time.alias("start_time:r"),
        end_time.alias("end_time:r"),
    )

    lf = lf.join(lhs_lf, left_on="span_id", right_on="parent_span_id", how="inner")
    lf = lf.join(rhs_lf, left_on="span_id", right_on="parent_span_id", how="inner")

    a = lf.select((pl.col("end_time:l") < pl.col("start_time:r")).all()).collect().item()
    b = lf.select((pl.col("end_time:r") < pl.col("start_time:l")).all()).collect().item()

    if a and not b:
        return True
    elif b and not a:
        return False
    else:
        return None


# --- Searching & Ranking ---


def find_related_service(sdg: SDG, function: PlaceNode) -> PlaceNode | None:
    assert function.kind == PlaceKind.function
    for edge in sdg.in_edges(function.id):
        if edge.kind == DepKind.includes:
            src = sdg.get_node_by_id(edge.src_id)
            assert src.kind == PlaceKind.service
            return src


def find_related_pods(sdg: SDG, service: PlaceNode) -> list[PlaceNode]:
    ans = []
    for edge in sdg.out_edges(service.id):
        if edge.kind == DepKind.routes_to:
            dst = sdg.get_node_by_id(edge.dst_id)
            assert dst.kind == PlaceKind.pod
            ans.append(dst)
    return ans


def find_related_containers(sdg: SDG, pod: PlaceNode) -> list[PlaceNode]:
    ans = []
    for edge in sdg.out_edges(pod.id):
        if edge.kind == DepKind.runs:
            dst = sdg.get_node_by_id(edge.dst_id)
            assert dst.kind == PlaceKind.container
            ans.append(dst)
    return ans


def find_root_cause_candidates(acg: nx.MultiDiGraph, sdg: SDG) -> list[PlaceNode]:
    dp: defaultdict[int, int] = defaultdict(int)
    q: Queue[int] = Queue()

    degrees: dict[int, int] = {}
    for node_id in acg.nodes:
        degree = acg.out_degree(node_id)
        assert isinstance(degree, int)
        degrees[node_id] = degree
        if degree == 0:
            q.put(node_id)
    dp[USER_NODE_ID] = 1

    rcc_list: list[PlaceNode] = []
    while not q.empty():
        v = q.get()

        expanded = False
        for u, _ in acg.in_edges(v):  # type: ignore
            dp[u] += dp[v]
            degrees[u] -= 1
            if degrees[u] == 0:
                q.put(u)
                expanded = True

        if not expanded and v != USER_NODE_ID:
            v_node = sdg.get_node_by_id(v)
            v_node.data["alg.dp"] = dp[v]
            rcc_list.append(v_node)

    rcc_list.sort(key=lambda node: dp[node.id], reverse=True)

    if debug():
        logger.debug("found {} root cause candidates:", len(rcc_list))
        for node in rcc_list:
            logger.debug(f"  {node.uniq_name}: dp={dp[node.id]}")

    return rcc_list


def find_related_service_for_rcc(sdg: SDG, node: PlaceNode) -> PlaceNode:
    if node.kind == PlaceKind.service:
        return node

    if node.kind == PlaceKind.function:
        service = find_related_service(sdg, node)
        assert service is not None
        return service

    if node.kind == PlaceKind.pod:
        for edge in sdg.in_edges(node.id):
            if edge.kind == DepKind.routes_to:
                service = sdg.get_node_by_id(edge.src_id)
                return service
        else:
            raise ValueError(f"Pod `{node.uniq_name}` has no related service")

    if node.kind == PlaceKind.container:
        for edge in sdg.in_edges(node.id):
            if edge.kind == DepKind.runs:
                pod = sdg.get_node_by_id(edge.src_id)
                break
        else:
            raise ValueError(f"Container `{node.uniq_name}` has no related pod")

        for edge in sdg.in_edges(pod.id):
            if edge.kind == DepKind.routes_to:
                service = sdg.get_node_by_id(edge.src_id)
                return service
        else:
            raise ValueError(f"Pod `{pod.uniq_name}` has no related service")

    raise NotImplementedError


def unify_to_service_candidates(sdg: SDG, rcc_list: list[PlaceNode]) -> list[str]:
    service_buckets: defaultdict[str, list[PlaceNode]] = defaultdict(list)
    for node in rcc_list:
        service = find_related_service_for_rcc(sdg, node)
        assert service is not None
        service_buckets[service.self_name].append(node)

    services = []
    for service_name, nodes in service_buckets.items():
        total_dp = sum(node.data["alg.dp"] for node in nodes)
        services.append((service_name, total_dp))
    services.sort(key=lambda x: x[1], reverse=True)

    if debug():
        logger.debug("found {} service candidates:", len(services))
        for service_name, total_dp in services:
            logger.debug(f"  {service_name}: total_dp={total_dp}")

    if all(total_dp == 0 for _, total_dp in services):
        datapack = sdg.data["datapack"]
        logger.warning("all service candidates have zero dp: datapack=`{}`", datapack)

    ans = [service_name for service_name, _ in services if service_name != "loadgenerator"]
    return ans
