#!/usr/bin/env python3
"""Mine an observed-in-normal-traffic chaos surface from raw datapack traces.

Why this exists
---------------
The legacy `clickhouseanalyzer` builds chaos-point candidates from
`otel.otel_traces` with **no phase filtering** — initialization, normal, and
fault-injection windows are folded together, and endpoints are keyed by the
*caller* (client spans). The result contains points for routes that are only
hit at startup or only appear under a fault, attributed to services that do
not actually host them.

This tool instead reads the **raw** `normal_traces.parquet` (ClickHouse schema,
`SpanAttributes` / `ResourceAttributes` as JSON strings) from collected
datapacks and emits, per system, only the surface that was actually exercised
during the NORMAL phase — endpoints attributed to the **server** that hosts
them, with an observation count so long-tail per-request noise can be dropped.
Output is a per-system JSON "first-pass" manifest; an aegis-side renderer turns
it into PointManifests.

Usage
-----
    mine.py --datapack-root <dir> --out <dir> [--min-count 2]

`--datapack-root` is walked for `*/normal_traces.parquet`. The system of each
datapack is taken from the span's `k8s.namespace.name` (trailing digits
stripped: `otel-demo1` -> `otel-demo`).
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from collections import defaultdict
from pathlib import Path

import polars as pl

# --- path / span-name normalization (kept in parity with manifestgen) --------
_UUID = re.compile(r"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")
_TRAIN = re.compile(r"^[A-Z]{1,3}\d+$")  # train codes: G1234 / D002 / K85
_NUM = re.compile(r"^\d+$")
_TOKEN = re.compile(r"^[A-Za-z0-9_-]{16,}$")  # opaque session / cart tokens
_HASDIGIT = re.compile(r"\d")
_DIGITRUN = re.compile(r"\d{2,}")  # any run of >=2 digits => an id
_IP = re.compile(r"^\d{1,3}(\.\d{1,3}){3}$")  # bare IPv4 (no resolvable name)


def _high_card(seg: str) -> bool:
    if seg in ("", "*", "**") or seg.startswith("{"):  # keep templated/wildcard
        return False
    if _UUID.match(seg) or _TRAIN.match(seg) or _NUM.match(seg):
        return True
    # a run of 2+ digits marks an id (user1392, addr:12) while sparing version
    # segments such as v1 / oauth2 that carry a single digit.
    if _DIGITRUN.search(seg):
        return True
    if _TOKEN.match(seg) and _HASDIGIT.search(seg):
        return True
    return False


def norm_path(p: str) -> str:
    if not p:
        return p
    p = p.split("?", 1)[0]  # drop query string
    return "/".join("*" if _high_card(s) else s for s in p.split("/"))


def norm_span(s: str) -> str:
    if not s:
        return s
    parts = s.split(" ", 1)
    if len(parts) == 2 and parts[0].isupper():
        return parts[0] + " " + norm_path(parts[1])
    return norm_path(s) if s.startswith("/") else s


def system_of(ns: str) -> str:
    return re.sub(r"\d+$", "", ns or "")


def _first(sa: dict, *keys: str) -> str:
    for k in keys:
        v = sa.get(k)
        if v not in (None, ""):
            return str(v)
    return ""


def load_k8s_ip_map(path: Path) -> dict:
    """IP -> workload/service name, from a datapack's k8s.json. A message broker
    is reached via its Service ClusterIP (e.g. 192.168.x.x -> "rabbitmq"), so we
    map both Service ClusterIPs (-> service name) and pod IPs (-> app label)."""
    if not path.exists():
        return {}
    try:
        d = json.loads(path.read_text())
    except Exception:
        return {}
    m = {}
    for lst in (d.get("pods") or {}).values():
        for p in lst:
            md = p.get("metadata") or {}
            lb = md.get("labels") or {}
            ip = (p.get("status") or {}).get("pod_ip")
            name = lb.get("app") or lb.get("app.kubernetes.io/name") or md.get("name")
            if ip and name:
                m[ip] = name
    for lst in (d.get("services") or {}).values():
        for s in lst:
            sp = s.get("spec") or {}
            name = (s.get("metadata") or {}).get("name")
            for ip in [sp.get("cluster_ip")] + list(sp.get("cluster_i_ps") or []):
                if ip and ip != "None" and name:
                    m[ip] = name
    return m


def _new_system() -> dict:
    return {
        "http": defaultdict(lambda: {"count": 0, "status": set(), "span_names": set()}),
        "grpc": defaultdict(lambda: {"count": 0, "status": set()}),
        "db": defaultdict(lambda: {"count": 0}),
        "infra": defaultdict(lambda: {"count": 0}),
        "edges": defaultdict(lambda: {"count": 0, "span_names": set()}),
        "services": set(),
        "datapacks": set(),
    }


def extract(parquet_paths: list[Path]) -> dict:
    sysd: dict[str, dict] = defaultdict(_new_system)
    for pth in parquet_paths:
        try:
            df = pl.read_parquet(
                pth,
                columns=[
                    "SpanId",
                    "ParentSpanId",
                    "SpanName",
                    "SpanKind",
                    "ServiceName",
                    "ResourceAttributes",
                    "SpanAttributes",
                ],
            )
        except Exception as e:  # corrupt / partial download
            print(f"skip {pth}: {e}", file=sys.stderr)
            continue
        dp = Path(pth).parent.name
        ip2name = load_k8s_ip_map(Path(pth).parent / "k8s.json")
        span_svc = dict(zip(df["SpanId"].to_list(), df["ServiceName"].to_list()))
        for row in df.iter_rows(named=True):
            try:
                ra = json.loads(row["ResourceAttributes"] or "{}")
                sa = json.loads(row["SpanAttributes"] or "{}")
            except Exception:
                continue
            system = system_of(ra.get("k8s.namespace.name") or ra.get("service.namespace") or "")
            if not system:
                continue
            S = sysd[system]
            S["datapacks"].add(dp)
            svc = row["ServiceName"] or ra.get("service.name") or ""
            S["services"].add(svc)
            kind = row["SpanKind"]

            rpc_svc = _first(sa, "rpc.service")
            db_sys = _first(sa, "db.system.name", "db.system")
            method = _first(sa, "http.request.method", "http.method")
            path = _first(sa, "http.route", "url.path", "http.target")
            port = _first(sa, "server.port", "net.host.port")
            status = _first(sa, "http.response.status_code", "http.status_code")
            sn = norm_span(row["SpanName"] or "")

            # SERVER spans are the evidence that `svc` hosts this endpoint.
            # The parent span (caller) -> svc is a topology edge.
            if kind in ("Server", "Consumer"):
                if rpc_svc:
                    e = S["grpc"][(svc, rpc_svc, _first(sa, "rpc.method"))]
                    e["count"] += 1
                    st = _first(sa, "rpc.grpc.status_code")
                    st and e["status"].add(st)
                elif method or path:
                    e = S["http"][(svc, method, norm_path(path), port)]
                    e["count"] += 1
                    status and e["status"].add(status)
                    sn and e["span_names"].add(sn)
                caller = span_svc.get(row["ParentSpanId"])
                if caller and caller != svc:
                    e = S["edges"][(caller, svc)]
                    e["count"] += 1
                    sn and e["span_names"].add(sn)
            elif kind == "Client" and db_sys:
                # full sql granularity feeds jvm_mysql_* (db_name/table/sql_type)
                dbn = _first(sa, "db.name")
                tbl = _first(sa, "db.sql.table")
                sqt = _first(sa, "db.operation").lower()  # agent expects lowercase sqlType
                S["db"][(svc, db_sys, dbn, tbl, sqt)]["count"] += 1
                # infra dependency: the db host (server.address is the workload
                # name, e.g. "mysql"/"valkey-cart"); skip bare IPs.
                tgt = _first(sa, "server.address", "net.peer.name")
                if tgt and not _IP.match(tgt) and tgt != svc:
                    S["infra"][(svc, tgt, "db", db_sys)]["count"] += 1
            elif kind == "Producer":
                # message queues carry only an IP peer; resolve it to the broker
                # workload via k8s.json, else fall back to the system type.
                msys = _first(sa, "messaging.system")
                if msys:
                    peer = _first(sa, "network.peer.address", "net.peer.ip", "net.peer.name")
                    tgt = ip2name.get(peer) or msys
                    S["infra"][(svc, tgt, "mq", msys)]["count"] += 1
    return sysd


_SPAN_CAP = 30  # representative span_names per endpoint/edge; the rest is noise


def _spans(s: set) -> list:
    return sorted(s)[:_SPAN_CAP]


def dump(sysd: dict, out_dir: Path, min_count: int) -> dict:
    out_dir.mkdir(parents=True, exist_ok=True)
    summary = {}
    for system, S in sorted(sysd.items()):
        http_all = list(S["http"].items())
        http_keep = [x for x in http_all if x[1]["count"] >= min_count]
        obj = {
            "system": system,
            "datapack_count": len(S["datapacks"]),
            "min_count": min_count,
            "services": sorted(S["services"]),
            "http_endpoints": [
                {
                    "service": s,
                    "method": m,
                    "path": p,
                    "port": port,
                    "count": e["count"],
                    "status": sorted(e["status"]),
                    "span_names": _spans(e["span_names"]),
                }
                for (s, m, p, port), e in sorted(http_keep, key=lambda x: -x[1]["count"])
            ],
            "grpc_operations": [
                {"service": s, "rpc_service": rs, "rpc_method": rm, "count": e["count"], "status": sorted(e["status"])}
                for (s, rs, rm), e in sorted(S["grpc"].items(), key=lambda x: -x[1]["count"])
                if e["count"] >= min_count
            ],
            "db_operations": [
                {"service": s, "db_system": d, "db_name": dn, "table": tb, "sql_type": st, "count": e["count"]}
                for (s, d, dn, tb, st), e in sorted(S["db"].items(), key=lambda x: -x[1]["count"])
                if e["count"] >= min_count
            ],
            "infra_deps": [
                {"service": s, "target": t, "kind": k, "system": sysm, "count": e["count"]}
                for (s, t, k, sysm), e in sorted(S["infra"].items(), key=lambda x: -x[1]["count"])
                if e["count"] >= min_count
            ],
            "edges": [
                {"source": s, "target": t, "count": e["count"], "span_names": _spans(e["span_names"])}
                for (s, t), e in sorted(S["edges"].items(), key=lambda x: -x[1]["count"])
                if e["count"] >= min_count
            ],
        }
        (out_dir / f"{system}.json").write_text(json.dumps(obj, indent=2, ensure_ascii=False) + "\n")
        summary[system] = {
            "datapacks": obj["datapack_count"],
            "services": len(obj["services"]),
            "http": len(obj["http_endpoints"]),
            "http_dropped_longtail": len(http_all) - len(http_keep),
            "grpc": len(obj["grpc_operations"]),
            "db": len(obj["db_operations"]),
            "infra": len(obj["infra_deps"]),
            "edges": len(obj["edges"]),
        }
    return summary


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--datapack-root", required=True, type=Path, help="dir walked for */normal_traces.parquet")
    ap.add_argument("--out", required=True, type=Path, help="output dir for <system>.json")
    ap.add_argument(
        "--min-count", type=int, default=2, help="drop endpoints/edges observed fewer than N times (default 2)"
    )
    args = ap.parse_args()

    paths = sorted(args.datapack_root.glob("*/normal_traces.parquet"))
    if not paths:
        sys.exit(f"no */normal_traces.parquet under {args.datapack_root}")
    print(f"mining {len(paths)} datapacks ...", file=sys.stderr)
    summary = dump(extract(paths), args.out, args.min_count)
    print(json.dumps(summary, indent=2, ensure_ascii=False))


if __name__ == "__main__":
    main()
