#!/usr/bin/env python3
"""
validate_workspace_index.py — health check for the aegis workspace.

Reads workspace.yaml + project-index.yaml from the workspace root,
then validates:

  A. every path under a repo-scoped field (`code`, `frontend`,
     `chaos_code`, `rcabench_code`) resolves to a file or directory on disk,
  B. every requirement path belongs to the repo that `path_field` in
     workspace.yaml claims owns that field (prefix check),
  C. every `depends_on` target id exists in the index,
  D. every `contracts:` reference on a requirement resolves to a contract
     key declared in workspace.yaml,
  E. every requirement has a `status` value that matches the allowed set,
  F. every requirement has a valid `type`, every non-feature requirement
     has a valid `repo`, and every referenced repo key exists in workspace.yaml,
  G. every `parent` / `decomposes_into` relationship is internally consistent.

Exit codes:
  0  clean
  1  violations found (list is printed)
  2  setup problem (missing workspace.yaml / unreadable index)

Usage:
  python3 validate_workspace_index.py [--workspace-root <path>] [--json]
"""
from __future__ import annotations

import argparse
import json
import os
import sys
from typing import Any

import yaml

ALLOWED_STATUS = {
    "draft", "planned", "implementing", "implemented", "tested",
    "blocked", "deferred", "disabled", "removed-frontend",
}
ALLOWED_TYPES = {
    "feature", "capability", "api", "ui", "library", "pipeline", "contract", "service",
}
PATH_FIELDS = ("code", "frontend", "chaos_code", "rcabench_code")


def load_yaml(path: str) -> Any:
    with open(path) as f:
        return yaml.safe_load(f)


def iter_paths(requirement: dict, field: str):
    for entry in requirement.get(field) or []:
        if isinstance(entry, dict):
            p = entry.get("path")
        else:
            p = entry
        if p:
            yield p


def validate(workspace_root: str) -> dict:
    ws_manifest_path = os.path.join(workspace_root, "workspace.yaml")
    index_path = os.path.join(workspace_root, "project-index.yaml")

    if not os.path.isfile(ws_manifest_path):
        return {"error": f"workspace.yaml not found at {ws_manifest_path}"}
    if not os.path.isfile(index_path):
        return {"error": f"project-index.yaml not found at {index_path}"}

    manifest = load_yaml(ws_manifest_path)
    index = load_yaml(index_path)

    # Build field-name → expected path prefix (repo root) map
    declared_repos = set((manifest.get("repos") or {}).keys())
    field_to_prefix: dict[str, str] = {}
    for repo_key, repo in (manifest.get("repos") or {}).items():
        field = repo.get("path_field")
        pfx = repo.get("path")
        if field and pfx:
            field_to_prefix[field] = pfx.rstrip("/") + "/"

    declared_contracts = set((manifest.get("contracts") or {}).keys())
    requirements = index.get("requirements", [])
    req_ids = {r.get("id") for r in requirements}

    missing_paths: list[tuple[str, str, str]] = []   # (req_id, field, path)
    prefix_mismatches: list[tuple[str, str, str, str]] = []  # (req_id, field, path, expected_prefix)
    bad_depends_on: list[tuple[str, str]] = []        # (req_id, target_id)
    bad_contract_refs: list[tuple[str, str]] = []     # (req_id, contract_key)
    bad_status: list[tuple[str, str]] = []            # (req_id, status)
    bad_type: list[tuple[str, str]] = []              # (req_id, type)
    bad_repo: list[tuple[str, str]] = []              # (req_id, repo)
    bad_parent_refs: list[tuple[str, str]] = []       # (req_id, parent_id)
    bad_parent_links: list[tuple[str, str]] = []      # (req_id, parent_id)
    bad_decompose_refs: list[tuple[str, str]] = []    # (req_id, child_id)
    bad_feature_shape: list[tuple[str, str]] = []     # (req_id, issue)

    req_map = {r.get("id"): r for r in requirements}

    for req in requirements:
        rid = req.get("id", "<no id>")
        req_type = req.get("type")
        repo = req.get("repo")

        for field in PATH_FIELDS:
            expected_prefix = field_to_prefix.get(field)
            for p in iter_paths(req, field):
                full = os.path.join(workspace_root, p)
                if not os.path.exists(full):
                    missing_paths.append((rid, field, p))
                if expected_prefix and not p.startswith(expected_prefix):
                    prefix_mismatches.append((rid, field, p, expected_prefix))

        for dep in req.get("depends_on") or []:
            if dep not in req_ids:
                bad_depends_on.append((rid, dep))

        for c in req.get("contracts") or []:
            if c not in declared_contracts:
                bad_contract_refs.append((rid, c))

        status = req.get("status")
        if status not in ALLOWED_STATUS:
            bad_status.append((rid, status or "<unset>"))

        if req_type not in ALLOWED_TYPES:
            bad_type.append((rid, req_type or "<unset>"))

        if req_type == "feature":
            if repo not in (None, "null"):
                bad_feature_shape.append((rid, "feature requirements must omit repo or set it to null"))
            children = req.get("decomposes_into") or []
            if not children:
                bad_feature_shape.append((rid, "feature requirements must declare decomposes_into"))
            for child_id in children:
                if child_id not in req_ids:
                    bad_decompose_refs.append((rid, child_id))
                    continue
                child = req_map[child_id]
                if child.get("parent") != rid:
                    bad_parent_links.append((child_id, rid))
        else:
            if repo not in declared_repos:
                bad_repo.append((rid, repo or "<unset>"))
            if req.get("decomposes_into"):
                bad_feature_shape.append((rid, "only feature requirements may declare decomposes_into"))

        parent = req.get("parent")
        if parent:
            if parent not in req_ids:
                bad_parent_refs.append((rid, parent))
            else:
                parent_req = req_map[parent]
                if parent_req.get("type") != "feature":
                    bad_parent_links.append((rid, parent))
                elif rid not in (parent_req.get("decomposes_into") or []):
                    bad_parent_links.append((rid, parent))

    report = {
        "workspace_root": workspace_root,
        "requirements_total": len(requirements),
        "missing_paths": missing_paths,
        "prefix_mismatches": prefix_mismatches,
        "bad_depends_on": bad_depends_on,
        "bad_contract_refs": bad_contract_refs,
        "bad_status": bad_status,
        "bad_type": bad_type,
        "bad_repo": bad_repo,
        "bad_parent_refs": bad_parent_refs,
        "bad_parent_links": bad_parent_links,
        "bad_decompose_refs": bad_decompose_refs,
        "bad_feature_shape": bad_feature_shape,
    }
    report["violations_total"] = sum(
        len(report[k]) for k in
        ("missing_paths", "prefix_mismatches", "bad_depends_on",
         "bad_contract_refs", "bad_status", "bad_type", "bad_repo",
         "bad_parent_refs", "bad_parent_links", "bad_decompose_refs",
         "bad_feature_shape")
    )
    return report


def print_text(report: dict) -> None:
    if "error" in report:
        print(f"ERROR: {report['error']}", file=sys.stderr)
        return
    print(f"workspace: {report['workspace_root']}")
    print(f"requirements: {report['requirements_total']}")
    print(f"violations:   {report['violations_total']}")

    if report["missing_paths"]:
        print(f"\n[A] missing_paths ({len(report['missing_paths'])}):")
        for rid, field, p in report["missing_paths"]:
            print(f"  {rid}  {field}  {p}")

    if report["prefix_mismatches"]:
        print(f"\n[B] prefix_mismatches ({len(report['prefix_mismatches'])}):")
        for rid, field, p, exp in report["prefix_mismatches"]:
            print(f"  {rid}  {field}  {p}  (expected to start with {exp})")

    if report["bad_depends_on"]:
        print(f"\n[C] bad_depends_on ({len(report['bad_depends_on'])}):")
        for rid, dep in report["bad_depends_on"]:
            print(f"  {rid}  -> unknown {dep}")

    if report["bad_contract_refs"]:
        print(f"\n[D] bad_contract_refs ({len(report['bad_contract_refs'])}):")
        for rid, c in report["bad_contract_refs"]:
            print(f"  {rid}  -> unknown contract {c}")

    if report["bad_status"]:
        print(f"\n[E] bad_status ({len(report['bad_status'])}):")
        for rid, s in report["bad_status"]:
            print(f"  {rid}  status={s}  (allowed: {sorted(ALLOWED_STATUS)})")

    if report["bad_type"]:
        print(f"\n[F] bad_type ({len(report['bad_type'])}):")
        for rid, req_type in report["bad_type"]:
            print(f"  {rid}  type={req_type}  (allowed: {sorted(ALLOWED_TYPES)})")

    if report["bad_repo"]:
        print(f"\n[G] bad_repo ({len(report['bad_repo'])}):")
        for rid, repo in report["bad_repo"]:
            print(f"  {rid}  repo={repo}")

    if report["bad_parent_refs"]:
        print(f"\n[H] bad_parent_refs ({len(report['bad_parent_refs'])}):")
        for rid, parent in report["bad_parent_refs"]:
            print(f"  {rid}  -> unknown parent {parent}")

    if report["bad_parent_links"]:
        print(f"\n[I] bad_parent_links ({len(report['bad_parent_links'])}):")
        for rid, parent in report["bad_parent_links"]:
            print(f"  {rid}  parent/decomposes_into mismatch with {parent}")

    if report["bad_decompose_refs"]:
        print(f"\n[J] bad_decompose_refs ({len(report['bad_decompose_refs'])}):")
        for rid, child in report["bad_decompose_refs"]:
            print(f"  {rid}  -> unknown child {child}")

    if report["bad_feature_shape"]:
        print(f"\n[K] bad_feature_shape ({len(report['bad_feature_shape'])}):")
        for rid, issue in report["bad_feature_shape"]:
            print(f"  {rid}  {issue}")


def main() -> int:
    default_root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    ap = argparse.ArgumentParser()
    ap.add_argument("--workspace-root", default=default_root,
                    help=f"workspace root (default: {default_root})")
    ap.add_argument("--json", action="store_true", help="emit JSON report")
    args = ap.parse_args()

    report = validate(args.workspace_root)

    if "error" in report:
        print_text(report)
        return 2

    if args.json:
        print(json.dumps(report, indent=2))
    else:
        print_text(report)

    return 1 if report["violations_total"] > 0 else 0


if __name__ == "__main__":
    sys.exit(main())
