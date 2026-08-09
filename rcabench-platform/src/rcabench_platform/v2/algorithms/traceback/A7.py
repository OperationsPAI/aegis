import json
from collections import defaultdict, deque
from dataclasses import dataclass
from enum import auto
from pprint import pformat

import numpy as np

from ....compat import StrEnum
from ...graphs.sdg.defintion import (
    SDG,
    DepEdge,
    DepKind,
    ExpandedGraphPath,
    GraphPath,
    Indicator,
    PlaceKind,
    PlaceNode,
)
from ...graphs.sdg.statistics import STAT_PREFIX, calc_statistics
from ...logging import logger, timeit
from ...utils.env import debug
from ..spec import Algorithm, AlgorithmAnswer, AlgorithmArgs
from ._common import build_sdg_with_cache


class AnomalyKind(StrEnum):
    error_rate_up = auto()
    latency_up = auto()
    qpm_down = auto()

    cpu_up = auto()
    cpu_down = auto()
    memory_up = auto()
    memory_down = auto()
    jvm_gc_up = auto()
    jvm_gc_down = auto()

    forward_call_prob_up = auto()
    forward_call_prob_down = auto()
    backward_call_prob_up = auto()
    backward_call_prob_down = auto()


@dataclass(kw_only=True, slots=True, frozen=True)
class Anomaly:
    kind: AnomalyKind
    score: float


def find_sli_nodes(sdg: SDG) -> list[PlaceNode]:
    ans_ids = []
    for node in sdg.iter_nodes():
        if node.kind == PlaceKind.function:
            if node.self_name.startswith("ts-ui-dashboard"):
                ans_ids.append(node.id)
        elif node.kind == PlaceKind.pod:
            ans_ids.append(node.id)
        elif node.kind == PlaceKind.service:
            ans_ids.append(node.id)

    top_op_names: set[str] | None = sdg.data.get("top_op_names")
    if top_op_names:
        for top_op_name in top_op_names:
            node = sdg.query_node_by_kind(PlaceKind.function, top_op_name)
            if node:
                ans_ids.append(node.id)

    ans_ids = set(ans_ids)

    ans = [sdg.get_node_by_id(x) for x in ans_ids]

    if debug():
        for node in ans:
            logger.debug(f"found SLI node: `{node.uniq_name}`")

    return ans


def detect_node_anomalies(node: PlaceNode) -> list[Anomaly]:
    ans = node.data.get("alg.anomalies")
    if ans:
        return ans

    if node.kind == PlaceKind.function:
        ans = detect_anomalies_for_function(node)
    elif node.kind == PlaceKind.pod:
        ans = detect_anomalies_for_pod_or_service(node)
    elif node.kind == PlaceKind.service:
        ans = detect_anomalies_for_pod_or_service(node)
    else:
        ans = []

    node.data["alg.anomalies"] = ans
    return ans


def relative_diff(a: float, b: float) -> float:
    if a == 0:
        if b == 0:
            return 0.0
        else:
            return 1.0
    return (b - a) / abs(a)


def detect_anomalies_for_pod_or_service(node: PlaceNode) -> list[Anomaly]:
    assert node.kind == PlaceKind.pod or node.kind == PlaceKind.service

    ans: list[Anomaly] = []

    normal_cpu_usage: float | None = node.data.get(f"{STAT_PREFIX[0]}.cpu_usage")
    anomal_cpu_usage: float | None = node.data.get(f"{STAT_PREFIX[1]}.cpu_usage")

    if normal_cpu_usage is not None and anomal_cpu_usage is not None:
        rel_diff = relative_diff(normal_cpu_usage, anomal_cpu_usage)
        if rel_diff > 0.5:
            anomaly = Anomaly(kind=AnomalyKind.cpu_up, score=abs(rel_diff))
            ans.append(anomaly)
            logger.debug(
                f"detected anomaly: {anomaly} "
                f"node: `{node.uniq_name}` "
                f"cpu_usage: {normal_cpu_usage} -> {anomal_cpu_usage}"
            )

        if rel_diff < -0.5:
            anomaly = Anomaly(kind=AnomalyKind.cpu_down, score=abs(rel_diff))
            ans.append(anomaly)
            logger.debug(
                f"detected anomaly: {anomaly} "
                f"node: `{node.uniq_name}` "
                f"cpu_usage: {normal_cpu_usage} -> {anomal_cpu_usage}"
            )

    normal_memory_usage: float | None = node.data.get(f"{STAT_PREFIX[0]}.memory_usage")
    anomal_memory_usage: float | None = node.data.get(f"{STAT_PREFIX[1]}.memory_usage")

    if normal_memory_usage is not None and anomal_memory_usage is not None:
        rel_diff = relative_diff(normal_memory_usage, anomal_memory_usage)
        if rel_diff > 0.5:
            anomaly = Anomaly(kind=AnomalyKind.memory_up, score=abs(rel_diff))
            ans.append(anomaly)
            logger.debug(
                f"detected anomaly: {anomaly} "
                f"node: `{node.uniq_name}` "
                f"memory_usage: {normal_memory_usage} -> {anomal_memory_usage}"
            )

        if rel_diff < -0.5:
            anomaly = Anomaly(kind=AnomalyKind.memory_down, score=abs(rel_diff))
            ans.append(anomaly)
            logger.debug(
                f"detected anomaly: {anomaly} "
                f"node: `{node.uniq_name}` "
                f"memory_usage: {normal_memory_usage} -> {anomal_memory_usage}"
            )

    normal_jvm_gc_duration: float | None = node.data.get(f"{STAT_PREFIX[0]}.jvm_gc_duration")
    anomal_jvm_gc_duration: float | None = node.data.get(f"{STAT_PREFIX[1]}.jvm_gc_duration")

    if normal_jvm_gc_duration is not None and anomal_jvm_gc_duration is not None:
        rel_diff = relative_diff(normal_jvm_gc_duration, anomal_jvm_gc_duration)
        if rel_diff > 0.5:
            anomaly = Anomaly(kind=AnomalyKind.jvm_gc_up, score=abs(rel_diff))
            ans.append(anomaly)
            logger.debug(
                f"detected anomaly: {anomaly} "
                f"node: `{node.uniq_name}` "
                f"jvm_gc_duration: {normal_jvm_gc_duration} -> {anomal_jvm_gc_duration}"
            )

        if rel_diff < -0.5:
            anomaly = Anomaly(kind=AnomalyKind.jvm_gc_down, score=abs(rel_diff))
            ans.append(anomaly)
            logger.debug(
                f"detected anomaly: {anomaly} "
                f"node: `{node.uniq_name}` "
                f"jvm_gc_duration: {normal_jvm_gc_duration} -> {anomal_jvm_gc_duration}"
            )

    return ans


def detect_anomalies_for_function(node: PlaceNode) -> list[Anomaly]:
    assert node.kind == PlaceKind.function

    ans: list[Anomaly] = []

    normal_error_rate: float | None = node.data.get(f"{STAT_PREFIX[0]}.error_rate")
    anomal_error_rate: float | None = node.data.get(f"{STAT_PREFIX[1]}.error_rate")

    if normal_error_rate is not None and anomal_error_rate is not None:
        rel_diff = relative_diff(normal_error_rate, anomal_error_rate)
        if rel_diff > 0.5:
            anomaly = Anomaly(kind=AnomalyKind.error_rate_up, score=abs(rel_diff))
            ans.append(anomaly)
            logger.debug(
                f"detected anomaly: {anomaly} "
                f"node: `{node.uniq_name}` "
                f"error_rate: {normal_error_rate} -> {anomal_error_rate}"
            )

    normal_latency: float | None = node.data.get(f"{STAT_PREFIX[0]}.latency")
    anomal_latency: float | None = node.data.get(f"{STAT_PREFIX[1]}.latency")

    if normal_latency is not None and anomal_latency is not None:
        rel_diff = relative_diff(normal_latency, anomal_latency)
        if rel_diff > 0.5:
            anomaly = Anomaly(kind=AnomalyKind.latency_up, score=abs(rel_diff))
            ans.append(anomaly)
            logger.debug(
                f"detected anomaly: {anomaly} node: `{node.uniq_name}` latency: {normal_latency} -> {anomal_latency}"
            )

    normal_latency: float | None = node.data.get(f"{STAT_PREFIX[0]}.latency_p50")
    anomal_latency: float | None = node.data.get(f"{STAT_PREFIX[1]}.latency_p50")

    if normal_latency is not None and anomal_latency is not None:
        rel_diff = relative_diff(normal_latency, anomal_latency)
        if rel_diff > 0.5:
            anomaly = Anomaly(kind=AnomalyKind.latency_up, score=abs(rel_diff))
            ans.append(anomaly)
            logger.debug(
                f"detected anomaly: {anomaly} "
                f"node: `{node.uniq_name}` "
                f"latency_p50: {normal_latency} -> {anomal_latency}"
            )

    normal_qpm: float | None = node.data.get(f"{STAT_PREFIX[0]}.qpm")
    anomal_qpm: float | None = node.data.get(f"{STAT_PREFIX[1]}.qpm")
    if normal_qpm is not None and anomal_qpm is not None:
        rel_diff = relative_diff(normal_qpm, anomal_qpm)
        if rel_diff < -0.5:
            anomaly = Anomaly(kind=AnomalyKind.qpm_down, score=abs(rel_diff))
            ans.append(anomaly)
            logger.debug(f"detected anomaly: {anomaly} node: `{node.uniq_name}` qpm: {normal_qpm} -> {anomal_qpm}")

    return ans


@timeit(log_args=False)
def search_by_anomaly(
    sdg: SDG,
    start_node: PlaceNode,
    anomaly: Anomaly,
) -> list[GraphPath]:
    if start_node.kind == PlaceKind.service:
        return [GraphPath.from_single_node(start_node)]

    if anomaly.kind == AnomalyKind.error_rate_up:
        return search_recursively_by_function_anomaly(sdg, start_node, anomaly)

    elif anomaly.kind == AnomalyKind.latency_up:
        return search_recursively_by_function_anomaly(sdg, start_node, anomaly)

    elif anomaly.kind == AnomalyKind.qpm_down:
        return search_by_qpm_down(sdg, start_node)

    elif anomaly.kind == AnomalyKind.cpu_up:
        return search_by_pod_anomaly(sdg, start_node)

    elif anomaly.kind == AnomalyKind.cpu_down:
        return search_by_pod_anomaly(sdg, start_node)

    elif anomaly.kind == AnomalyKind.memory_up:
        return search_by_pod_anomaly(sdg, start_node)

    elif anomaly.kind == AnomalyKind.memory_down:
        return search_by_pod_anomaly(sdg, start_node)

    elif anomaly.kind == AnomalyKind.jvm_gc_up:
        return search_by_pod_anomaly(sdg, start_node)

    elif anomaly.kind == AnomalyKind.jvm_gc_down:
        return search_by_pod_anomaly(sdg, start_node)

    else:
        raise NotImplementedError


@timeit(log_args=False)
def search_by_qpm_down(sdg: SDG, start_node: PlaceNode) -> list[GraphPath]:
    assert start_node.kind == PlaceKind.function

    return []  # FIXME


@timeit(log_args=False)
def search_recursively_by_function_anomaly(sdg: SDG, start_node: PlaceNode, anomaly: Anomaly) -> list[GraphPath]:
    assert start_node.kind == PlaceKind.function

    ans = []

    q: deque[GraphPath] = deque()
    q.append(GraphPath.from_single_node(start_node))

    logger.debug("start bfs:")
    logger.debug(f"  start_node: `{start_node.uniq_name}`")
    logger.debug(f"  anomaly:    `{anomaly}`")

    while q:
        item = q.popleft()
        u = item.node

        logger.debug(f"visit node: `{u.uniq_name}`")

        extended = False
        for out_edge in sdg.out_edges(u.id):
            if out_edge.kind == DepKind.calls:
                v = sdg.get_node_by_id(out_edge.dst_id)
                assert v.kind == PlaceKind.function

                if v.id == u.id:
                    logger.debug(f"  skip self-loop: `{u.uniq_name}`")
                    continue

                anomalies = detect_node_anomalies(v)

                if any(anomaly.kind == a.kind for a in anomalies):
                    q.append(item.move(out_edge, v))
                    extended = True
                    logger.debug(f"move: `{u.uniq_name}` -> `{v.uniq_name}`")

        if not extended:
            logger.debug(f"  no more extension for `{u.uniq_name}`")
            service_edge = search_service_from_function(sdg, u)
            if service_edge:
                service_node = sdg.get_node_by_id(service_edge.src_id)
                assert service_node.kind == PlaceKind.service

                path = item.move(service_edge, service_node)
                ans.append(path)

    return ans


def search_service_from_function(sdg: SDG, function_node: PlaceNode) -> DepEdge | None:
    assert function_node.kind == PlaceKind.function
    services = []
    for in_edge in sdg.in_edges(function_node.id):
        if in_edge.kind == DepKind.includes:
            src = sdg.get_node_by_id(in_edge.src_id)
            assert src.kind == PlaceKind.service
            services.append(in_edge)
    if services:
        assert len(services) == 1
        return services[0]
    else:
        return None


@timeit(log_args=False)
def search_by_pod_anomaly(sdg: SDG, start_node: PlaceNode) -> list[GraphPath]:
    assert start_node.kind == PlaceKind.pod

    ans = []

    service_edge = search_service_from_pod(sdg, start_node)
    if service_edge:
        service_node = sdg.get_node_by_id(service_edge.src_id)

        path = GraphPath.from_single_node(start_node)
        path = path.move(service_edge, service_node)
        ans.append(path)

    return ans


def search_service_from_pod(sdg: SDG, pod_node: PlaceNode) -> DepEdge | None:
    assert pod_node.kind == PlaceKind.pod
    services: list[DepEdge] = []
    for in_edge in sdg.in_edges(pod_node.id):
        if in_edge.kind == DepKind.routes_to:
            src = sdg.get_node_by_id(in_edge.src_id)
            assert src.kind == PlaceKind.service
            services.append(in_edge)
    if services:
        assert len(services) == 1
        return services[0]
    else:
        return None


def detect_edge_anomalies(sdg: SDG, edge: DepEdge) -> list[Anomaly]:
    ans = edge.data.get("alg.anomalies")
    if ans:
        return ans

    if edge.kind == DepKind.calls:
        ans = detect_anomalies_for_calls_edge(sdg, edge)
    else:
        ans = []

    edge.data["alg.anomalies"] = ans
    return ans


def detect_anomalies_for_calls_edge(sdg: SDG, edge: DepEdge) -> list[Anomaly]:
    assert edge.kind == DepKind.calls

    src = sdg.get_node_by_id(edge.src_id)
    dst = sdg.get_node_by_id(edge.dst_id)
    edge_uniq_name = f"{src.uniq_name} --({edge.kind})-> {dst.uniq_name}"

    ans = []

    normal_forward_call_prob: float = edge.data[f"{STAT_PREFIX[0]}.forward_call_prob"]
    anomal_forward_call_prob: float = edge.data[f"{STAT_PREFIX[1]}.forward_call_prob"]
    if normal_forward_call_prob is not None and anomal_forward_call_prob is not None:
        rel_diff = relative_diff(normal_forward_call_prob, anomal_forward_call_prob)

        if rel_diff > 0.5:
            anomaly = Anomaly(kind=AnomalyKind.forward_call_prob_up, score=abs(rel_diff))
            ans.append(anomaly)
            logger.debug(
                f"detected anomaly: {anomaly} "
                f"edge: `{edge_uniq_name}` "
                f"forward_call_prob: {normal_forward_call_prob} -> {anomal_forward_call_prob}"
            )

        if rel_diff < -0.5:
            anomaly = Anomaly(kind=AnomalyKind.forward_call_prob_down, score=abs(rel_diff))
            ans.append(anomaly)
            logger.debug(
                f"detected anomaly: {anomaly} "
                f"edge: `{edge_uniq_name}` "
                f"forward_call_prob: {normal_forward_call_prob} -> {anomal_forward_call_prob}"
            )

    normal_backward_call_prob: float = edge.data[f"{STAT_PREFIX[0]}.backward_call_prob"]
    anomal_backward_call_prob: float = edge.data[f"{STAT_PREFIX[1]}.backward_call_prob"]
    if normal_backward_call_prob is not None and anomal_backward_call_prob is not None:
        rel_diff = relative_diff(normal_backward_call_prob, anomal_backward_call_prob)

        if rel_diff > 0.5:
            anomaly = Anomaly(kind=AnomalyKind.backward_call_prob_up, score=abs(rel_diff))
            ans.append(anomaly)
            logger.debug(
                f"detected anomaly: {anomaly} "
                f"edge: `{edge_uniq_name}` "
                f"backward_call_prob: {normal_backward_call_prob} -> {anomal_backward_call_prob}"
            )

        if rel_diff < -0.5:
            anomaly = Anomaly(kind=AnomalyKind.backward_call_prob_down, score=abs(rel_diff))
            ans.append(anomaly)
            logger.debug(
                f"detected anomaly: {anomaly} "
                f"edge: `{edge_uniq_name}` "
                f"backward_call_prob: {normal_backward_call_prob} -> {anomal_backward_call_prob}"
            )

    return ans


def calc_path_score(sdg: SDG, path: ExpandedGraphPath) -> float:
    anomalies_list: list[list[Anomaly]] = []
    for item in path:
        if isinstance(item, PlaceNode):
            anomalies = detect_node_anomalies(item)
        elif isinstance(item, DepEdge):
            anomalies = detect_edge_anomalies(sdg, item)
        else:
            raise NotImplementedError

        if anomalies:
            anomalies_list.append(anomalies)

    max_score_list = []
    for anomalies in anomalies_list:
        max_score = max(a.score for a in anomalies)
        max_score_list.append(max_score)

    max_score_array = np.array(max_score_list)
    score = float(max_score_array.mean())
    return score


class TraceBackA7(Algorithm):
    def needs_cpu_count(self) -> int | None:
        return 8

    @timeit()
    def __call__(self, args: AlgorithmArgs) -> list[AlgorithmAnswer]:
        if debug():
            injection_path = args.input_folder / "injection.json"
            if injection_path.exists():
                with open(injection_path) as f:
                    injection = json.load(f)
                    injection["display_config"] = json.loads(injection["display_config"])
                    injection["engine_config"] = json.loads(injection["engine_config"])
                logger.debug(f"found injection:\n{pformat(injection)}")

        sdg = build_sdg_with_cache(args)

        calc_statistics(sdg)

        sli_nodes = find_sli_nodes(sdg)

        sli_anomalies: list[tuple[PlaceNode, list[Anomaly]]] = []

        for sli_node in sli_nodes:
            anomalies = detect_node_anomalies(sli_node)
            if anomalies:
                sli_anomalies.append((sli_node, anomalies))

        for edge in sdg.iter_edges():
            anomalies = detect_edge_anomalies(sdg, edge)
            # TODO

        search_paths: list[list[PlaceNode | DepEdge]] = []
        for sli_node, anomalies in sli_anomalies:
            for anomaly in anomalies:
                paths = search_by_anomaly(sdg, sli_node, anomaly)
                for path in paths:
                    search_paths.append(path.expand())

        if debug():
            for path in search_paths:
                path_score = calc_path_score(sdg, path)
                logger.debug(f"found path: (score={path_score})")
                for x in path:
                    if isinstance(x, PlaceNode):
                        logger.debug(f"--> {x.uniq_name}")
                        anomalies = detect_node_anomalies(x)
                        if anomalies:
                            logger.debug(f"         anomalies: {anomalies}")
                    elif isinstance(x, DepEdge):
                        logger.debug(f"--> ({x.kind})")
                        anomalies = detect_edge_anomalies(sdg, x)
                        if anomalies:
                            logger.debug(f"         anomalies: {anomalies}")
                logger.debug("")

        service_buckets: defaultdict[str, list[ExpandedGraphPath]] = defaultdict(list)

        for path in search_paths:
            assert len(path) >= 1
            last_node = path[-1]
            assert isinstance(last_node, PlaceNode)
            if last_node.kind == PlaceKind.service:
                service_buckets[last_node.self_name].append(path)

        if debug():
            for service_name, paths in service_buckets.items():
                logger.debug(f"found service: `{service_name}` with {len(paths)} paths")

        service_names = list(service_buckets.keys())
        service_names.sort(
            key=lambda x: (
                len(service_buckets[x]),
                # max(len(path) for path in service_buckets[x]),
                max(calc_path_score(sdg, path) for path in service_buckets[x]),
            ),
            reverse=True,
        )

        answers: list[AlgorithmAnswer] = []
        for rank, service_name in enumerate(service_names, start=1):
            answer = AlgorithmAnswer(level="service", name=service_name, rank=rank)
            answers.append(answer)

        if debug():
            logger.debug(f"len(answers): {len(answers)}")
            for answer in answers:
                logger.debug(str(answer))

        if all(node.kind != PlaceKind.function for node, _ in sli_anomalies):
            logger.warning(f"SLI: no function anomalies detected: dataset=`{args.dataset}` datapack=`{args.datapack}`")
        else:
            has_error_rate_up = any(
                any(a.kind == AnomalyKind.error_rate_up for a in anomalies) for _, anomalies in sli_anomalies
            )
            has_latency_up = any(
                any(a.kind == AnomalyKind.latency_up for a in anomalies) for _, anomalies in sli_anomalies
            )
            if not (has_error_rate_up or has_latency_up):
                logger.warning(
                    f"SLI: no major function anomalies detected: dataset=`{args.dataset}` datapack=`{args.datapack}`"
                )

        return answers
