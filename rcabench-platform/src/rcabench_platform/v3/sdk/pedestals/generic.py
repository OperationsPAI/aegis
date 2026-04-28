from collections import defaultdict

import polars as pl

from ..logging import timeit
from .registry import Pedestal, register_pedestal


class GenericPedestal(Pedestal):
    """No-op pedestal: skip URL templating, treat span_name verbatim.

    Use when a per-system pedestal hasn't been written yet. Detection still
    works (per-endpoint normal/abnormal delta is system-agnostic), but
    high-cardinality URLs (with session ids / currency codes / etc) will
    fragment into many low-count endpoints and degrade signal.
    """

    _NAME: str = ""
    _ENTRANCE: str = ""

    @property
    def black_list(self) -> list[str]:
        return []

    @property
    def name(self) -> str:
        return self._NAME

    @property
    def entrance_service(self) -> str:
        return self._ENTRANCE

    def normalize_op_name(self, op_name: pl.Expr) -> pl.Expr:
        return op_name

    def normalize_path(self, path: str) -> str:
        return path

    def add_op_name(self, traces: pl.LazyFrame) -> pl.LazyFrame:
        op_name = pl.concat_str(pl.col("service_name"), pl.col("span_name"), separator=" ")
        return traces.with_columns(op_name.alias("op_name")).drop("span_name")

    @timeit(log_args=False)
    def fix_client_spans(
        self, traces: pl.DataFrame
    ) -> tuple[pl.DataFrame, dict[str, str], dict[str, str]]:
        id2op: dict[str, str] = {}
        id2parent: dict[str, str] = {}
        parent_child: defaultdict[str, set[str]] = defaultdict(set)

        for span_id, parent_span_id, op_name in traces.select(
            "span_id", "parent_span_id", "op_name"
        ).iter_rows():
            assert isinstance(span_id, str) and span_id
            id2op[span_id] = op_name
            if parent_span_id:
                assert isinstance(parent_span_id, str)
                id2parent[span_id] = parent_span_id
                parent_child[parent_span_id].add(span_id)

        # Lift bare-method client spans (op ends with ' GET' / ' POST') to use child path.
        fix: dict[str, str] = {}
        to_drop: set[str] = set()
        for span_id, op_name in id2op.items():
            if op_name.endswith(" GET") or op_name.endswith(" POST") or op_name.endswith(
                " PUT"
            ) or op_name.endswith(" DELETE"):
                kids = parent_child.get(span_id, set())
                if not kids:
                    to_drop.add(span_id)
                elif len(kids) == 1:
                    child_op = id2op.get(next(iter(kids)), "")
                    parts = child_op.split(" ", 2)
                    if len(parts) >= 3:
                        fix[span_id] = op_name + " " + parts[2]

        for sid in to_drop:
            id2op.pop(sid, None)
        id2op.update(fix)

        if fix:
            patch_df = pl.DataFrame(
                [{"span_id": sid, "op_name_fix": op} for sid, op in fix.items()]
            )
            traces = traces.join(patch_df, on="span_id", how="left").with_columns(
                pl.coalesce("op_name_fix", "op_name").alias("op_name")
            ).drop("op_name_fix")

        return traces, id2op, id2parent


@register_pedestal("hs")
class HotelReservationPedestal(GenericPedestal):
    _NAME = "hs"
    _ENTRANCE = "frontend"

    @property
    def success_codes(self) -> set[str]:
        return {"200"}


@register_pedestal("otel-demo")
class OtelDemoPedestal(GenericPedestal):
    _NAME = "otel-demo"
    _ENTRANCE = "frontend-proxy"

    @property
    def success_codes(self) -> set[str]:
        # Browser-facing Envoy ingress: redirects (301/302/304/307/308) and 201 are
        # legitimate user responses, not service failures.
        return {"200", "201", "204", "301", "302", "304", "307", "308"}

    @property
    def slo_latency_min_absolute(self) -> float:
        # otel-demo's normal p99 is ~3s (gen-AI inference, image generation), so
        # the noise floor for relative-ratio detection has to be higher than the
        # 100ms generic default.
        return 0.5


@register_pedestal("tea")
class TeaStorePedestal(GenericPedestal):
    _NAME = "tea"
    _ENTRANCE = "teastore-webui"

    @property
    def success_codes(self) -> set[str]:
        # TeaStore JSP front-end: 302 redirects after login/cart actions are normal.
        return {"200", "302"}


@register_pedestal("sn")
class SocialNetworkPedestal(GenericPedestal):
    _NAME = "sn"
    _ENTRANCE = "nginx-thrift"


@register_pedestal("mm")
class MediaMicroservicesPedestal(GenericPedestal):
    _NAME = "mm"
    _ENTRANCE = "nginx-web-server"


@register_pedestal("sockshop")
class SockShopPedestal(GenericPedestal):
    _NAME = "sockshop"
    _ENTRANCE = "front-end"

    @property
    def success_codes(self) -> set[str]:
        # Sock Shop follows REST conventions: 201 Created for POST /cart / POST /orders,
        # 204 No Content for DELETE, 302 redirects after login.
        return {"200", "201", "202", "204", "302"}
