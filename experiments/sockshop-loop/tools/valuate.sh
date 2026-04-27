#!/usr/bin/env bash
# Annotate enumerated candidates (one JSON per line on stdin) with a
# repo-derived `valuation` score, then write to stdout.
#
# JVM (params has class+method):
#   +1 if class file located, +1 if method body found,
#   +1 per loop / try-catch / DB call / HTTP-client call,
#   -2 for trivial getters/setters/equals/hashCode/toString.
# HTTP* (params has route+method):
#   +2 if loadgen exercises this route (substring match),
#   +1 if route appears in a Java @Path annotation.
# Pod/Container/Memory/CPU/DNS/etc on app:
#   +1 if app's loadgen lines contain the service name as endpoint root,
#   else +0.
#
# Usage: cat enum.jsonl | tools/valuate.sh > scored.jsonl
# Env: SRC_DIR (default: experiments/sockshop-loop/cache/sockshop-src)
set -euo pipefail
SRC_DIR="${SRC_DIR:-/home/ddq/AoyangSpace/aegis/experiments/sockshop-loop/cache/sockshop-src}"
LOADGEN="$SRC_DIR/loadgen/locustfile.py"

[[ -d "$SRC_DIR" ]] || { echo "missing SRC_DIR=$SRC_DIR" >&2; exit 1; }

while IFS= read -r line; do
  app=$(jq -r '.app' <<<"$line")
  ct=$(jq -r '.chaos_type' <<<"$line")
  cls=$(jq -r '.params.class // empty' <<<"$line")
  method=$(jq -r '.params.method // empty' <<<"$line")
  endpoint=$(jq -r '.params.route // empty' <<<"$line")
  http_method=$(jq -r '.params.http_method // empty' <<<"$line")
  target=$(jq -r '.params.target_service // empty' <<<"$line")

  score=0; reasons=()

  case "$ct" in
    JVM*)
      if [[ -n "$cls" ]]; then
        # Penalty for test classes (production injection, not tests)
        case "$cls" in
          *IT|*Test|*.test.*|*TestData*|*Mock*) score=$((score-3)); reasons+=("test_class") ;;
        esac
        # Convert com.foo.Bar to com/foo/Bar.java; for inner classes (Foo$Bar) use Foo.java
        outer_cls="${cls%%\$*}"
        relpath="${outer_cls//./\/}.java"
        srcfile=$(find "$SRC_DIR/$app/src/main" -name "$(basename "$relpath")" 2>/dev/null | head -1)
        if [[ -n "$srcfile" && -f "$srcfile" ]]; then
          score=$((score+1)); reasons+=("class_found")
          # Find method body (rough: lines from "method(" until a balanced brace)
          body=$(awk -v m="$method" '
            $0 ~ "[[:space:]]" m "[[:space:]]*\\(" { in_body=1; depth=0 }
            in_body { print; for(i=1;i<=length($0);i++){c=substr($0,i,1); if(c=="{") depth++; else if(c=="}"){depth--; if(depth==0) {in_body=0; exit}}} }
          ' "$srcfile" 2>/dev/null)
          if [[ -n "$body" ]]; then
            score=$((score+1)); reasons+=("method_body")
            # heuristic complexity signals
            loc=$(echo "$body" | wc -l)
            [[ $loc -gt 5 ]] && { score=$((score+1)); reasons+=("loc${loc}"); }
            [[ $loc -gt 20 ]] && { score=$((score+1)); reasons+=("complex"); }
            grep -qE '\bfor\s*\(|\bwhile\s*\(|\.stream\(|\.forEach' <<<"$body" && { score=$((score+1)); reasons+=("loop"); }
            grep -qE '\btry\s*\{|\bcatch\s*\(' <<<"$body" && { score=$((score+1)); reasons+=("trycatch"); }
            grep -qE '\b(Repository|EntityManager|jdbc|prepareStatement|query|select|insert|update|delete)\b' <<<"$body" && { score=$((score+2)); reasons+=("db_io"); }
            grep -qE '(WebClient|HttpClient|RestTemplate|WebTarget|Async)' <<<"$body" && { score=$((score+2)); reasons+=("net_io"); }
            # trivial penalties
            case "$method" in
              get*|set*|is*|toString|hashCode|equals|main)
                if [[ $loc -le 3 ]]; then score=$((score-2)); reasons+=("trivial_${method}"); fi
                ;;
            esac
          fi
        fi
      fi
      ;;
    HTTP*)
      if [[ -n "$endpoint" && -f "$LOADGEN" ]]; then
        # Try to match the route in loadgen (escape special chars roughly)
        route_lit=$(sed 's/[][\.*^$/]/\\&/g' <<<"$endpoint")
        if grep -qE "\"$route_lit\"|name=\"$route_lit" "$LOADGEN" 2>/dev/null; then
          score=$((score+2)); reasons+=("loadgen_route")
        fi
        # Always +1 if route looks like a real path (starts with /)
        [[ "$endpoint" == /* ]] && { score=$((score+1)); reasons+=("path_like"); }
      fi
      ;;
    Network*)
      if [[ -n "$target" && -f "$LOADGEN" ]]; then
        # Network pair value = is the caller (app) hitting target via loadgen?
        grep -qiE "$app|$target" "$LOADGEN" && { score=$((score+1)); reasons+=("loadgen_app_or_target"); }
      fi
      ;;
    PodFailure|PodKill|ContainerKill|CPUStress|MemoryStress|DNSError|DNSRandom|TimeSkew)
      if [[ -f "$LOADGEN" ]]; then
        # Service-level: how often is the app touched in loadgen
        hits=$(grep -ciE "\"$app|/$app|self\\.client\\.[a-z]+\\([^)]*$app" "$LOADGEN" 2>/dev/null) || hits=0
        if [[ ${hits:-0} -gt 0 ]]; then
          # Cap at +3
          add=$((hits<3 ? hits : 3))
          score=$((score+add)); reasons+=("loadgen_hits${hits}")
        fi
      fi
      ;;
  esac

  jq -c --arg sc "$score" --argjson r "$(printf '%s\n' "${reasons[@]:-}" | jq -R . | jq -s .)" \
    '.valuation = ($sc | tonumber) | .valuation_reasons = $r' <<<"$line"
done
