#!/usr/bin/env python3
"""Phase 1 index transform: prepend workspace: block and normalize backend code: paths.

Run with: python3 _phase1_transform.py <in> <out>
Operates as a line-based transform to preserve byte-for-byte anything outside the targeted edits.
"""
import sys
import re

KNOWN_PREFIXES = ("AegisLab/", "AegisLab-frontend/", "chaos-experiment/", "rcabench-platform/")

WORKSPACE_BLOCK = """# Unified requirements index for the aegis workspace.
# Source of truth — symlinked from each of the 4 repos.
# See workspace.yaml (same directory) for repo layout and contracts.

workspace:
  root: .
  manifest: workspace.yaml
  schema_version: 2

"""


def normalize(inp: str, outp: str) -> dict:
    with open(inp) as f:
        lines = f.readlines()

    out_lines = [WORKSPACE_BLOCK]
    rewrites = 0
    frontend_anomalies = []

    # Track which array we are currently inside. The yaml is block-style; we detect
    # the start by seeing a bare "  field:" line (2-space indent) and assume subsequent
    # "    - path:" / "      path:" lines belong to it until indentation drops.
    current_field = None  # one of: code, frontend, chaos_code, rcabench_code
    field_indent = None

    for i, line in enumerate(lines):
        stripped = line.rstrip("\n")

        # Detect start of a path-bearing array.
        m = re.match(r"^(\s*)(code|frontend|chaos_code|rcabench_code):\s*$", stripped)
        if m:
            current_field = m.group(2)
            field_indent = len(m.group(1))
            out_lines.append(line)
            continue

        # Detect start of a different top-level-under-requirement field — exit the array.
        if current_field is not None:
            # Any non-empty line whose indent <= field_indent ends the array.
            if stripped and not stripped.startswith(" "):
                current_field = None
                field_indent = None
            else:
                leading = len(line) - len(line.lstrip(" "))
                if stripped and leading <= field_indent and stripped[leading] != "#":
                    # A sibling field (same or shallower indent) starts; array ended.
                    # Only reset if this line isn't itself a path: entry.
                    if not re.match(r"^\s*-\s+path:\s", stripped) and not re.match(r"^\s*path:\s", stripped):
                        current_field = None
                        field_indent = None

        # Path-line rewrite inside a known array.
        if current_field in ("code",) and current_field is not None:
            # Rewrite backend `code:` paths that lack a workspace-relative prefix.
            path_m = re.match(r"^(\s*(?:-\s+)?path:\s*)(.+?)\s*$", line.rstrip("\n"))
            if path_m:
                prefix_part, pval = path_m.group(1), path_m.group(2)
                pval_stripped = pval.strip()
                # Strip possible surrounding quotes; preserve quoting style.
                quoted = False
                quote_char = ""
                if pval_stripped.startswith('"') and pval_stripped.endswith('"'):
                    quoted, quote_char = True, '"'
                    inner = pval_stripped[1:-1]
                elif pval_stripped.startswith("'") and pval_stripped.endswith("'"):
                    quoted, quote_char = True, "'"
                    inner = pval_stripped[1:-1]
                else:
                    inner = pval_stripped

                if not inner.startswith(KNOWN_PREFIXES) and not inner.startswith("/"):
                    new_inner = "AegisLab/" + inner
                    new_val = f"{quote_char}{new_inner}{quote_char}" if quoted else new_inner
                    # Preserve original trailing whitespace / newline
                    original_eol = line[len(line.rstrip("\n")):]
                    line = f"{prefix_part}{new_val}{original_eol}"
                    rewrites += 1
        elif current_field == "frontend":
            # Audit only — do not silently rewrite.
            path_m = re.match(r"^(\s*(?:-\s+)?path:\s*)(.+?)\s*$", line.rstrip("\n"))
            if path_m:
                inner = path_m.group(2).strip().strip('"').strip("'")
                if inner and not inner.startswith("AegisLab-frontend/"):
                    frontend_anomalies.append((i + 1, inner))

        out_lines.append(line)

    with open(outp, "w") as f:
        f.writelines(out_lines)

    return {"rewrites": rewrites, "frontend_anomalies": frontend_anomalies}


if __name__ == "__main__":
    result = normalize(sys.argv[1], sys.argv[2])
    print(f"rewrites={result['rewrites']}")
    if result["frontend_anomalies"]:
        print(f"FRONTEND_ANOMALIES={len(result['frontend_anomalies'])}")
        for ln, p in result["frontend_anomalies"][:20]:
            print(f"  line {ln}: {p}")
        sys.exit(2)
