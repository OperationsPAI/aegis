import json

import duckdb

from ....logging import logger
from ....utils.env import debug
from .model import SDG, Observation, Symptom, SymptomType

# ============================================================================
# Module 3: Observation Detection (from conclusion.parquet)
# ============================================================================


class ObservationDetector:
    DEBUG_OBSERVATION_DISPLAY_COUNT = 5

    def __init__(self, con: duckdb.DuckDBPyConnection):
        self.con = con

    def detect(self) -> list[Observation]:
        observations = []

        query = """
        SELECT
            SpanName,
            Issues,
            AbnormalAvgDuration,
            NormalAvgDuration,
            AbnormalSuccRate,
            NormalSuccRate
        FROM conclusion
        WHERE Issues != '{}'  -- Has at least one issue
        """

        result = self.con.execute(query).fetchall()

        for row in result:
            span_name, issues_str, abn_dur, norm_dur, abn_succ, norm_succ = row
            issues = json.loads(issues_str.replace("'", '"'))

            # Extract service name from span name
            service_name = self._extract_service_name(span_name)
            if not service_name:
                continue

            # Get trace IDs for this observation (will be used for bootstrapping)
            trace_ids = self._get_trace_ids_for_observation(span_name)

            # Try to find the actual span_name in traces table
            trace_span_name = self._find_trace_span_name(span_name)

            # Check for latency issues (using keys from detector.py)
            latency_keys = {
                "avg_duration",
                "p90_duration",
                "p95_duration",
                "p99_duration",
                "hard_timeout",
            }
            if any(key in issues for key in latency_keys):
                observations.append(
                    Observation(
                        observation_type=SymptomType.LATENCY,
                        entry_span_name=span_name,
                        service_name=service_name,
                        trace_ids=trace_ids,
                        trace_span_name=trace_span_name,
                        abnormal_avg_duration=float(abn_dur) if abn_dur is not None else None,
                        normal_avg_duration=float(norm_dur) if norm_dur is not None else None,
                        abnormal_succ_rate=float(abn_succ) if abn_succ is not None else None,
                        normal_succ_rate=float(norm_succ) if norm_succ is not None else None,
                    )
                )

            # Check for success rate issues (using key from detector.py)
            if "succ_rate" in issues:
                observations.append(
                    Observation(
                        observation_type=SymptomType.ERROR_RATE,
                        entry_span_name=span_name,
                        service_name=service_name,
                        trace_ids=trace_ids,
                        trace_span_name=trace_span_name,
                        abnormal_avg_duration=float(abn_dur) if abn_dur is not None else None,
                        normal_avg_duration=float(norm_dur) if norm_dur is not None else None,
                        abnormal_succ_rate=float(abn_succ) if abn_succ is not None else None,
                        normal_succ_rate=float(norm_succ) if norm_succ is not None else None,
                    )
                )

        if debug():
            logger.debug(f"Detected {len(observations)} observations from conclusion.parquet")
            for obs in observations[: self.DEBUG_OBSERVATION_DISPLAY_COUNT]:
                metrics_info = ""
                if obs.observation_type == SymptomType.LATENCY and obs.abnormal_avg_duration is not None:
                    metrics_info = (
                        f" [Latency: {obs.abnormal_avg_duration / 1e9:.3f}s (abn) vs "
                        f"{obs.normal_avg_duration / 1e9:.3f}s (norm)]"
                    )
                elif obs.observation_type == SymptomType.ERROR_RATE and obs.abnormal_succ_rate is not None:
                    metrics_info = (
                        f" [SuccRate: {obs.abnormal_succ_rate:.2%} (abn) vs {obs.normal_succ_rate:.2%} (norm)]"
                    )
                logger.debug(
                    f"  Observation: {obs.service_name} ({obs.observation_type.name}): "
                    f"{obs.entry_span_name[:60]}... ({len(obs.trace_ids)} traces){metrics_info}"
                )

        return observations

    def _get_trace_ids_for_observation(self, entry_span_name: str) -> list[str]:
        """
        Get trace IDs for an observation span.

        The entry_span_name from conclusion.parquet might be in format:
        "HTTP POST http://ts-ui-dashboard:8080/api/v1/..."

        But traces table uses format:
        "POST /api/v1/..."

        We need to find the matching span in traces_bad.
        """
        # First try exact match
        query = f"""
        SELECT DISTINCT trace_id
        FROM traces_bad
        WHERE span_name = '{entry_span_name}'
        LIMIT 1000
        """
        result = self.con.execute(query).fetchall()
        trace_ids = [row[0] for row in result]

        if len(trace_ids) > 0:
            return trace_ids

        # If no exact match, try to extract the path part
        # From "HTTP POST http://ts-ui-dashboard:8080/api/v1/..."
        # Extract "POST /api/v1/..."
        if "http://" in entry_span_name or "https://" in entry_span_name:
            # Extract method (HTTP POST -> POST)
            method = entry_span_name.split()[0] if " " in entry_span_name else ""
            method = method.replace("HTTP ", "").replace("HTTPS ", "")

            # Extract path (/api/v1/...)
            try:
                if "http://" in entry_span_name:
                    url_part = entry_span_name.split("http://")[1]
                elif "https://" in entry_span_name:
                    url_part = entry_span_name.split("https://")[1]
                else:
                    return []

                # Skip hostname:port, get path
                if "/" in url_part:
                    path = "/" + "/".join(url_part.split("/")[1:])
                else:
                    return []

                # Construct alternative span name
                alt_span_name = f"{method} {path}"

                alt_query = f"""
                SELECT DISTINCT trace_id
                FROM traces_bad
                WHERE span_name = '{alt_span_name}'
                LIMIT 1000
                """
                alt_result = self.con.execute(alt_query).fetchall()
                alt_trace_ids = [row[0] for row in alt_result]

                if len(alt_trace_ids) > 0:
                    return alt_trace_ids
            except Exception:
                pass

        return trace_ids

    def _find_trace_span_name(self, entry_span_name: str) -> str | None:
        """
        Find the actual span_name in traces table that corresponds to entry_span_name.
        Converts from conclusion format to traces format if needed.

        Returns the actual span_name in traces format, or None if not found.
        """
        # Convert span name format from "HTTP POST http://service:port/path" to "POST /path"
        converted_name = self._convert_span_name_format(entry_span_name)

        # Check if converted name exists in traces tables
        query = """
        SELECT DISTINCT span_name
        FROM traces_bad
        WHERE span_name = ?
        UNION
        SELECT DISTINCT span_name
        FROM traces_good
        WHERE span_name = ?
        LIMIT 1
        """
        result = self.con.execute(query, [converted_name, converted_name]).fetchall()

        if len(result) > 0:
            return result[0][0]

        return None

    def _convert_span_name_format(self, span_name: str) -> str:
        """
        Convert span name from conclusion format to traces format.

        From: "HTTP POST http://service:port/path"
        To: "POST /path"

        If already in traces format, returns as-is.
        """
        if not span_name.startswith("HTTP"):
            return span_name

        try:
            parts = span_name.split()
            if len(parts) < 3:
                return span_name

            # Extract method (POST, GET, etc.)
            method = parts[1]

            # Extract URL
            url_str = parts[2]
            if "http://" in url_str:
                url_part = url_str.split("http://")[1]
            elif "https://" in url_str:
                url_part = url_str.split("https://")[1]
            else:
                return span_name

            # Skip hostname:port, get path
            if "/" in url_part:
                path = "/" + "/".join(url_part.split("/")[1:])
            else:
                return span_name

            # Construct traces format span name
            return f"{method} {path}"
        except Exception:
            return span_name

    def _extract_service_name(self, span_name: str) -> str | None:
        """
        Extract service name from span name.

        Examples:
        - "HTTP POST http://ts-ui-dashboard:8080/api/..." -> "ts-ui-dashboard"
        - "HTTP GET http://service:port/..." -> "service"
        """
        try:
            # Look for http:// or https:// pattern
            if "http://" in span_name:
                url_part = span_name.split("http://")[1]
            elif "https://" in span_name:
                url_part = span_name.split("https://")[1]
            else:
                return None

            # Extract hostname (before :port or /)
            hostname = url_part.split(":")[0].split("/")[0]
            return hostname
        except Exception:
            return None


# ============================================================================
# Module 4: Symptom Bootstrapping (Finding INTERNAL symptoms from observations)
# ============================================================================


class SymptomBootstrapper:
    """
    Bootstraps INTERNAL symptoms from high-level observations.

    This is the critical "malloc/mark discovery" step from the paper.
    Given "heap_size is high" (observation), find the "malloc" events (symptoms).

    For microservices:
    - Observation: Entry span has high latency/errors
    - Symptom: INTERNAL span that CAUSES the high latency/errors

    Strategies:
    1. LATENCY: Find spans with max exclusive duration increase (bad vs good)
    2. ERROR: Find deepest error-generating spans (error source, not propagator)
    """

    # Minimum number of traces required for statistical analysis
    MIN_TRACE_COUNT = 1

    # Top N symptoms to return per observation
    TOP_N_SYMPTOMS = 5

    # HTTP error status code threshold
    HTTP_ERROR_STATUS_CODE = 400

    def __init__(self, con: duckdb.DuckDBPyConnection, sdg: SDG):
        self.con = con
        self.sdg = sdg

    def bootstrap(self, observations: list[Observation]) -> list[Symptom]:
        all_symptoms = []

        for obs in observations:
            if obs.observation_type == SymptomType.LATENCY:
                symptoms = self._bootstrap_latency_symptoms(obs)
            elif obs.observation_type == SymptomType.ERROR_RATE:
                symptoms = self._bootstrap_error_symptoms(obs)
            else:
                symptoms = []

            all_symptoms.extend(symptoms)

        return all_symptoms

    def _bootstrap_latency_symptoms(self, obs: Observation) -> list[Symptom]:
        """
        Find INTERNAL spans causing latency increase (3-step process for better observability).

        Step 1: Calculate exclusive durations in BAD period
        Step 2: Calculate exclusive durations in GOOD period (baseline)
        Step 3: Calculate DELTA and filter
        """
        if len(obs.trace_ids) < self.MIN_TRACE_COUNT:
            return []

        trace_ids_str = "','".join(obs.trace_ids[:1000])

        # ====================================================================
        # DEBUGGING: Analyze entry span and trace structure
        # ====================================================================
        if debug():
            logger.debug("=" * 80)
            logger.debug(f"🔍 DEBUGGING Latency Observation: {obs.entry_span_name}")
            logger.debug(f"   Service: {obs.service_name}")
            abn_dur_str = f"{obs.abnormal_avg_duration / 1e9:.3f}s" if obs.abnormal_avg_duration else "N/A"
            norm_dur_str = f"{obs.normal_avg_duration / 1e9:.3f}s" if obs.normal_avg_duration else "N/A"
            logger.debug(f"   Abnormal avg duration: {abn_dur_str}")
            logger.debug(f"   Normal avg duration: {norm_dur_str}")
            if obs.abnormal_avg_duration and obs.normal_avg_duration:
                expected_delta = (obs.abnormal_avg_duration - obs.normal_avg_duration) / 1e9
                logger.debug(f"   Expected delta: ~{expected_delta:.3f}s")
            logger.debug(f"   Analyzing {len(obs.trace_ids)} traces")

            # Sample a few traces to understand structure
            debug_query = f"""
            WITH sample_traces AS (
                SELECT trace_id, span_id, parent_span_id, span_name, service_name, duration
                FROM traces_bad
                WHERE trace_id IN ('{trace_ids_str}')
            ),
            entry_spans AS (
                SELECT trace_id, span_id, duration, span_name
                FROM sample_traces
                WHERE span_name = '{obs.entry_span_name}'
            ),
            all_spans_per_trace AS (
                SELECT
                    e.trace_id,
                    e.duration AS entry_duration,
                    COUNT(s.span_id) AS total_span_count,
                    SUM(s.duration) AS total_span_duration,
                    COUNT(CASE WHEN s.parent_span_id IS NULL THEN 1 END) AS root_span_count,
                    COUNT(CASE WHEN s.parent_span_id IS NOT NULL THEN 1 END) AS child_span_count
                FROM entry_spans e
                LEFT JOIN sample_traces s ON e.trace_id = s.trace_id
                GROUP BY e.trace_id, e.duration
            )
            SELECT
                trace_id,
                entry_duration,
                total_span_count,
                total_span_duration,
                root_span_count,
                child_span_count
            FROM all_spans_per_trace
            ORDER BY entry_duration DESC
            LIMIT 5
            """

            try:
                debug_results = self.con.execute(debug_query).fetchall()
                logger.debug("   Top 5 slowest traces:")
                for (
                    trace_id,
                    entry_dur,
                    span_count,
                    total_dur,
                    root_count,
                    child_count,
                ) in debug_results:
                    logger.debug(
                        f"     Trace {trace_id[:16]}...: entry={entry_dur / 1e9:.3f}s, "
                        f"spans={span_count} (root={root_count}, child={child_count}), "
                        f"total_duration={total_dur / 1e9:.3f}s"
                    )

                # Detailed analysis of the slowest trace
                if debug_results:
                    slowest_trace_id = debug_results[0][0]
                    logger.debug(f"   Analyzing slowest trace {slowest_trace_id[:16]}... in detail:")

                    span_call_count_query = f"""
                    SELECT
                        span_name,
                        service_name,
                        COUNT(*) AS call_count,
                        AVG(duration) AS avg_duration,
                        SUM(duration) AS total_duration,
                        MAX(duration) AS max_duration
                    FROM traces_bad
                    WHERE trace_id = '{slowest_trace_id}'
                    GROUP BY span_name, service_name
                    ORDER BY SUM(duration) DESC
                    LIMIT 15
                    """

                    span_counts = self.con.execute(span_call_count_query).fetchall()
                    logger.debug("     Top spans by TOTAL duration (sum across all calls in this trace):")
                    for (
                        span_name,
                        service_name,
                        count,
                        avg_dur,
                        total_dur,
                        max_dur,
                    ) in span_counts:
                        logger.debug(
                            f"       {service_name}.{span_name[:35]}... "
                            f"called {count}x, total={total_dur / 1e9:.3f}s, "
                            f"avg={avg_dur / 1e9:.3f}s, max={max_dur / 1e9:.3f}s"
                        )

                    # ============================================================
                    # CRITICAL: Check timeline - are spans overlapping or exceeding entry span?
                    # ============================================================
                    timeline_query = f"""
                    WITH entry_span AS (
                        SELECT
                            time AS entry_start,
                            time + INTERVAL (duration || ' microseconds') AS entry_end,
                            duration AS entry_duration
                        FROM traces_bad
                        WHERE trace_id = '{slowest_trace_id}'
                        AND span_name = '{obs.entry_span_name}'
                        LIMIT 1
                    ),
                    trace_timeline AS (
                        SELECT
                            span_name,
                            service_name,
                            duration,
                            time AS start_time,
                            time + INTERVAL (duration || ' microseconds') AS end_time,
                            parent_span_id,
                            span_id,
                            EPOCH_MS(time - (SELECT entry_start FROM entry_span)) / 1000.0 AS start_offset,
                            EPOCH_MS(
                                (time + INTERVAL (duration || ' microseconds')) -
                                (SELECT entry_start FROM entry_span)
                            ) / 1000.0 AS end_offset
                        FROM traces_bad
                        WHERE trace_id = '{slowest_trace_id}'
                    )
                    SELECT
                        t.span_name,
                        t.service_name,
                        t.duration,
                        t.start_offset,
                        t.end_offset,
                        e.entry_duration,
                        CASE
                            WHEN t.start_time < e.entry_start THEN 'BEFORE_ENTRY'
                            WHEN t.end_time > e.entry_end THEN 'AFTER_ENTRY'
                            ELSE 'WITHIN_ENTRY'
                        END AS timeline_position
                    FROM trace_timeline t
                    CROSS JOIN entry_span e
                    WHERE t.duration > 1000000000  -- Only spans > 1 second
                    ORDER BY t.duration DESC
                    LIMIT 10
                    """

                    try:
                        timeline_results = self.con.execute(timeline_query).fetchall()
                        logger.debug("     Timeline analysis (spans > 1s):")
                        for (
                            span_name,
                            service_name,
                            duration,
                            start_offset,
                            end_offset,
                            entry_duration,
                            position,
                        ) in timeline_results:
                            entry_total = entry_duration / 1e9
                            logger.debug(
                                f"       {service_name}.{span_name[:30]}... "
                                f"dur={duration / 1e9:.3f}s, "
                                f"start={start_offset:+.3f}s, end={end_offset:+.3f}s "
                                f"(entry: 0s to {entry_total:.3f}s) [{position}]"
                            )

                        # ============================================================
                        # CRITICAL: Analyze AFTER_ENTRY spans and their impact
                        # ============================================================
                        after_entry_spans = [
                            (span_name, service_name, duration)
                            for span_name, service_name, duration, _, _, _, position in timeline_results
                            if position == "AFTER_ENTRY"
                        ]

                        if after_entry_spans:
                            logger.debug(
                                f"     ⚠️  Found {len(after_entry_spans)} spans that extend AFTER entry span ends!"
                            )
                            logger.debug(
                                "     These spans are included in exclusive duration calculation but SHOULD NOT be!"
                            )
                            total_after_entry_duration = sum(d for _, _, d in after_entry_spans) / 1e9
                            logger.debug(f"     Total duration of AFTER_ENTRY spans: {total_after_entry_duration:.3f}s")
                            logger.debug("     This explains why the algorithm misses the latency!")
                    except Exception as e:
                        logger.warning(f"     Timeline analysis failed: {e}")

                # Check for missing parent-child relationships
                missing_parent_query = f"""
                SELECT COUNT(*) AS orphan_count
                FROM traces_bad t1
                WHERE t1.trace_id IN ('{trace_ids_str}')
                AND t1.parent_span_id IS NOT NULL
                AND NOT EXISTS (
                    SELECT 1 FROM traces_bad t2
                    WHERE t2.trace_id = t1.trace_id
                    AND t2.span_id = t1.parent_span_id
                )
                """
                orphan_result = self.con.execute(missing_parent_query).fetchone()
                if orphan_result:
                    orphan_count = orphan_result[0]
                    if orphan_count > 0:
                        logger.warning(
                            f"   ⚠️  Found {orphan_count} orphan spans (parent_span_id references missing spans)"
                        )

            except Exception as e:
                logger.warning(f"   Debug query failed: {e}")

            logger.debug("=" * 80)

        # ====================================================================
        # STEP 1: Calculate exclusive durations in BAD period
        # ====================================================================
        if debug():
            logger.debug("[Step 1/3] Calculating exclusive durations in BAD period...")

        query_bad = f"""
        WITH
        bad_spans_raw AS (
            SELECT
                trace_id,
                span_id,
                span_name,
                service_name,
                duration,
                parent_span_id
            FROM traces_bad
            WHERE trace_id IN ('{trace_ids_str}')
        ),
        bad_exclusive_per_span AS (
            SELECT
                parent.trace_id,
                parent.span_id,
                parent.span_name,
                parent.service_name,
                parent.duration - COALESCE(SUM(child.duration), 0) AS exclusive_duration
            FROM bad_spans_raw parent
            LEFT JOIN bad_spans_raw child
                ON parent.span_id = child.parent_span_id
                AND parent.trace_id = child.trace_id
            GROUP BY parent.trace_id, parent.span_id, parent.span_name, parent.service_name, parent.duration
        )
        SELECT
            span_name,
            service_name,
            AVG(exclusive_duration) AS avg_exclusive,
            COUNT(span_id) AS sample_count
        FROM bad_exclusive_per_span
        GROUP BY span_name, service_name
        HAVING COUNT(span_id) >= {self.MIN_TRACE_COUNT}
        """

        try:
            bad_latency_stats = self.con.execute(query_bad).fetchall()
        except Exception as e:
            if debug():
                logger.warning(f"Step 1 failed: {e}")
            return []

        if not bad_latency_stats:
            if debug():
                logger.debug("  No spans found in bad period")
            return []

        if debug():
            logger.debug(f"  Found {len(bad_latency_stats)} span types in bad period")

            # Sort by avg exclusive duration to see what's contributing most
            sorted_stats = sorted(bad_latency_stats, key=lambda x: x[2], reverse=True)

            logger.debug("  Top 10 spans by exclusive duration in BAD period:")
            for i, (span_name, service_name, avg_exc, count) in enumerate(sorted_stats[:10], 1):
                logger.debug(
                    f"    #{i}: {service_name}.{span_name[:50]}... "
                    f"(avg_exclusive={avg_exc / 1e9:.3f}s, samples={count})"
                )

            # Also check total duration (not just exclusive) for these spans
            top_spans_filter = " OR ".join(
                [f"(span_name = '{sn}' AND service_name = '{sv}')" for sn, sv, _, _ in sorted_stats[:10]]
            )
            if top_spans_filter:
                total_duration_query = f"""
                SELECT
                    span_name,
                    service_name,
                    AVG(duration) AS avg_total_duration,
                    MAX(duration) AS max_total_duration,
                    MIN(duration) AS min_total_duration
                FROM traces_bad
                WHERE trace_id IN ('{trace_ids_str}')
                AND ({top_spans_filter})
                GROUP BY span_name, service_name
                """
                try:
                    total_duration_results = self.con.execute(total_duration_query).fetchall()
                    logger.debug("  Comparing TOTAL vs EXCLUSIVE duration for top spans:")
                    for (
                        span_name,
                        service_name,
                        avg_total,
                        max_total,
                        min_total,
                    ) in total_duration_results:
                        # Find corresponding exclusive duration
                        exc_duration = next(
                            (avg_exc for sn, sv, avg_exc, _ in sorted_stats if sn == span_name and sv == service_name),
                            0,
                        )
                        logger.debug(
                            f"    {service_name}.{span_name[:40]}... "
                            f"total_avg={avg_total / 1e9:.3f}s "
                            f"(max={max_total / 1e9:.3f}s, min={min_total / 1e9:.3f}s), "
                            f"exclusive_avg={exc_duration / 1e9:.3f}s"
                        )
                except Exception as e:
                    logger.warning(f"  Total duration query failed: {e}")

        # ====================================================================
        # STEP 2: Calculate exclusive durations in GOOD period (baseline)
        # ====================================================================
        if debug():
            logger.debug("[Step 2/3] Calculating baseline exclusive durations in GOOD period...")

        # Build list of (span_name, service_name) to search for in good period
        bad_span_signatures = [(span_name, service_name) for span_name, service_name, _, _ in bad_latency_stats]

        if not bad_span_signatures:
            good_latency_stats = []
        else:
            # Build SQL IN clause for filtering
            span_filters = " OR ".join(
                [f"(span_name = '{sn}' AND service_name = '{sv}')" for sn, sv in bad_span_signatures[:100]]
            )

            query_good = f"""
            WITH
            good_trace_ids AS (
                SELECT DISTINCT trace_id
                FROM traces_good
                WHERE span_name = '{obs.entry_span_name}'  -- Same entry span as observation
                LIMIT {len(obs.trace_ids) * 10}            -- Sample more to get better coverage
            ),
            good_spans_raw AS (
                SELECT
                    t.trace_id,
                    t.span_id,
                    t.span_name,
                    t.service_name,
                    t.duration,
                    t.parent_span_id
                FROM traces_good t
                INNER JOIN good_trace_ids g ON t.trace_id = g.trace_id
                WHERE ({span_filters})
            ),
            good_exclusive_per_span AS (
                SELECT
                    parent.trace_id,
                    parent.span_id,
                    parent.span_name,
                    parent.service_name,
                    parent.duration - COALESCE(SUM(child.duration), 0) AS exclusive_duration
                FROM good_spans_raw parent
                LEFT JOIN good_spans_raw child
                    ON parent.span_id = child.parent_span_id
                    AND parent.trace_id = child.trace_id
                GROUP BY parent.trace_id, parent.span_id, parent.span_name, parent.service_name, parent.duration
            )
            SELECT
                span_name,
                service_name,
                AVG(exclusive_duration) AS avg_exclusive,
                COUNT(span_id) AS sample_count
            FROM good_exclusive_per_span
            GROUP BY span_name, service_name
            """

            try:
                good_latency_stats = self.con.execute(query_good).fetchall()
            except Exception as e:
                if debug():
                    logger.warning(f"Step 2 failed: {e}")
                good_latency_stats = []

        # Build index for quick lookup
        good_latency_index = {
            (span_name, service_name): (avg_exc, count)
            for span_name, service_name, avg_exc, count in good_latency_stats
        }

        if debug():
            logger.debug(f"  Found {len(good_latency_stats)} span types with baseline latency")
            for span_name, service_name, avg_exc, count in good_latency_stats[:3]:
                logger.debug(f"    {service_name}.{span_name[:50]} (avg={avg_exc / 1e9:.3f}s, n={count})")

        # ====================================================================
        # STEP 3: Calculate DELTA (bad - good) using EXCLUSIVE duration
        # ====================================================================
        if debug():
            logger.debug("[Step 3/4] Calculating DELTA (bad - good) for EXCLUSIVE duration...")

        symptoms = []
        exclusive_deltas = []

        for span_name, service_name, bad_avg, bad_count in bad_latency_stats:
            good_avg, good_count = good_latency_index.get((span_name, service_name), (0, 0))
            delta = bad_avg - good_avg

            if delta <= 0:
                continue
            if span_name == obs.entry_span_name:
                continue
            if span_name in (
                "GET",
                "POST",
                "PUT",
                "DELETE",
                "PATCH",
                "HEAD",
                "OPTIONS",
            ):
                continue

            exclusive_deltas.append(
                (
                    span_name,
                    service_name,
                    delta,
                    bad_avg,
                    good_avg,
                    bad_count,
                    good_count,
                )
            )

        exclusive_deltas.sort(key=lambda x: x[2], reverse=True)

        # ====================================================================
        # STEP 4: Calculate DELTA using TOTAL duration (for timeout scenarios)
        # ====================================================================
        if debug():
            logger.debug("[Step 4/4] Calculating DELTA (bad - good) for TOTAL duration...")

        # Calculate total duration stats for bad period
        query_bad_total = f"""
        SELECT
            span_name,
            service_name,
            AVG(duration) AS avg_total,
            COUNT(span_id) AS sample_count
        FROM traces_bad
        WHERE trace_id IN ('{trace_ids_str}')
        GROUP BY span_name, service_name
        HAVING COUNT(span_id) >= {self.MIN_TRACE_COUNT}
        """

        try:
            bad_total_stats = self.con.execute(query_bad_total).fetchall()
        except Exception as e:
            if debug():
                logger.warning(f"Step 4 (total duration) failed: {e}")
            bad_total_stats = []

        # Calculate total duration stats for good period
        if bad_total_stats:
            span_filter = " OR ".join(
                [f"(span_name = '{sn}' AND service_name = '{sv}')" for sn, sv, _, _ in bad_total_stats]
            )
            query_good_total = f"""
            SELECT
                span_name,
                service_name,
                AVG(duration) AS avg_total,
                COUNT(span_id) AS sample_count
            FROM traces_good
            WHERE ({span_filter})
            GROUP BY span_name, service_name
            """
            try:
                good_total_stats = self.con.execute(query_good_total).fetchall()
                good_total_index = {(sn, sv): (avg, cnt) for sn, sv, avg, cnt in good_total_stats}
            except Exception as e:
                if debug():
                    logger.warning(f"Step 4 (good period total duration) failed: {e}")
                good_total_index = {}
        else:
            good_total_index = {}

        # Calculate deltas for total duration
        total_deltas = []
        for span_name, service_name, bad_avg, bad_count in bad_total_stats:
            good_avg, good_count = good_total_index.get((span_name, service_name), (0, 0))
            delta = bad_avg - good_avg

            if delta <= 0:
                continue
            if span_name == obs.entry_span_name:
                continue
            if span_name in (
                "GET",
                "POST",
                "PUT",
                "DELETE",
                "PATCH",
                "HEAD",
                "OPTIONS",
            ):
                continue

            total_deltas.append(
                (
                    span_name,
                    service_name,
                    delta,
                    bad_avg,
                    good_avg,
                    bad_count,
                    good_count,
                )
            )

        total_deltas.sort(key=lambda x: x[2], reverse=True)

        # ====================================================================
        # DECISION: Choose between exclusive and total duration deltas
        # ====================================================================
        # Use total duration if the top deltas are significantly larger
        # This handles timeout scenarios where exclusive duration is misleading

        top_exclusive_delta = exclusive_deltas[0][2] if exclusive_deltas else 0
        top_total_delta = total_deltas[0][2] if total_deltas else 0

        if debug():
            logger.debug(f"  Top exclusive delta: {top_exclusive_delta / 1e9:.3f}s")
            logger.debug(f"  Top total delta: {top_total_delta / 1e9:.3f}s")

        # If total delta is more than 10x larger than exclusive delta, use total
        if top_total_delta > top_exclusive_delta * 10:
            if debug():
                logger.warning("  ⚠️  Total duration delta is significantly larger than exclusive delta!")
                logger.warning("  This suggests a timeout scenario - using TOTAL duration for symptom detection.")
            deltas = total_deltas
            use_total_duration = True
        else:
            if debug():
                logger.debug("  Using EXCLUSIVE duration for symptom detection (normal case).")
            deltas = exclusive_deltas
            use_total_duration = False

        top_deltas = deltas[: self.TOP_N_SYMPTOMS]

        if debug():
            logger.debug(f"  Filtered results: {len(deltas)} candidates, taking top {len(top_deltas)}")

            if use_total_duration:
                logger.debug("  📊 Using TOTAL duration deltas (timeout scenario detected):")
            else:
                logger.debug("  📊 Using EXCLUSIVE duration deltas (normal scenario):")

            # Show all deltas to understand what's being filtered
            if len(deltas) > 0:
                logger.debug("  All DELTA results (sorted by delta):")
                for i, (
                    span_name,
                    service_name,
                    delta,
                    bad_avg,
                    good_avg,
                    bad_count,
                    good_count,
                ) in enumerate(deltas[:20], 1):
                    logger.debug(
                        f"    #{i}: {service_name}.{span_name[:40]}... "
                        f"Δ={delta / 1e9:.3f}s (bad={bad_avg / 1e9:.3f}s [{bad_count}], "
                        f"good={good_avg / 1e9:.3f}s [{good_count}])"
                    )

                # Calculate sum of all deltas to see coverage
                total_delta = sum(d[2] for d in deltas)
                logger.debug(f"  Total DELTA across all spans: {total_delta / 1e9:.3f}s")

                if obs.abnormal_avg_duration and obs.normal_avg_duration:
                    expected_delta = obs.abnormal_avg_duration - obs.normal_avg_duration
                    coverage = (total_delta / expected_delta * 100) if expected_delta > 0 else 0
                    logger.debug(
                        f"  Coverage: {coverage:.1f}% "
                        f"(sum_of_deltas={total_delta / 1e9:.3f}s / expected={expected_delta / 1e9:.3f}s)"
                    )

        for (
            span_name,
            service_name,
            delta,
            bad_avg,
            good_avg,
            bad_count,
            good_count,
        ) in top_deltas:
            symptoms.append(
                Symptom(
                    symptom_type=SymptomType.LATENCY,
                    service_name=service_name,
                    span_name=span_name,
                    impact_score=float(delta / 1e9),  # Delta in duration (seconds)
                )
            )

            if debug():
                duration_type = "total" if use_total_duration else "exclusive"
                logger.debug(
                    f"  ✓ Latency symptom ({duration_type}): {service_name}.{span_name[:50]} "
                    f"(Δ={delta / 1e9:.3f}s, bad={bad_avg / 1e9:.3f}s [{bad_count} samples], "
                    f"good={good_avg / 1e9:.3f}s [{good_count} samples])"
                )

        return symptoms

    def _bootstrap_error_symptoms(self, obs: Observation) -> list[Symptom]:
        """
        Find INTERNAL spans generating errors (3-step process for better observability).

        Step 1: Find leaf error generators in BAD period
        Step 2: Find baseline errors in GOOD period
        Step 3: Calculate DELTA and filter
        """
        if len(obs.trace_ids) < self.MIN_TRACE_COUNT:
            return []

        trace_ids_str = "','".join(obs.trace_ids[:1000])

        # ====================================================================
        # STEP 1: Find leaf error generators in BAD period
        # ====================================================================
        if debug():
            logger.debug("[Step 1/3] Finding leaf error generators in BAD period...")

        query_bad = f"""
        WITH
        -- 1. Error spans from TRACES (status code based)
        trace_error_spans AS (
            SELECT
                trace_id,
                span_id,
                span_name,
                service_name,
                "attr.http.response.status_code" AS status_code
            FROM traces_bad
            WHERE trace_id IN ('{trace_ids_str}')
            AND ("attr.status_code" = 'Error'
                OR "attr.http.response.status_code" >= {self.HTTP_ERROR_STATUS_CODE})
        ),
        -- 2. Error spans from LOGS (ERROR/FATAL level)
        --    Gold standard: logs with trace_id + span_id
        log_error_spans_gold AS (
            SELECT DISTINCT
                L.trace_id,
                L.span_id,
                T.span_name,
                T.service_name
            FROM logs_bad L
            INNER JOIN traces_bad T
            ON L.trace_id = T.trace_id
            AND L.span_id = T.span_id
            WHERE L.trace_id IN ('{trace_ids_str}')
            AND L.level NOT IN ('INFO')
            AND L.trace_id IS NOT NULL
            AND L.span_id IS NOT NULL
        ),

        -- 3. Error spans from LOGS (fallback: pod-level correlation)
        --    When logs don't have trace_id/span_id
        log_error_spans_fallback AS (
            SELECT DISTINCT
                T.trace_id,
                T.span_id,
                T.span_name,
                T.service_name
            FROM logs_bad L
            INNER JOIN traces_bad T
            ON L."attr.k8s.pod.name" = T."attr.k8s.pod.name"
            AND L.time BETWEEN T.time - INTERVAL 1 SECOND AND T.time + INTERVAL 1 SECOND
            WHERE L.trace_id IN ('{trace_ids_str}')
            AND L.level NOT IN ('INFO')
            AND L."attr.k8s.pod.name" IS NOT NULL
            AND (L.trace_id IS NULL OR L.span_id IS NULL)
        ),

        -- 4. UNION all error sources (trace + log gold + log fallback)
        all_error_spans AS (
            SELECT trace_id, span_id, span_name, service_name, 'trace' AS source
            FROM trace_error_spans
            UNION
            SELECT trace_id, span_id, span_name, service_name, 'log_gold' AS source
            FROM log_error_spans_gold
            UNION
            SELECT trace_id, span_id, span_name, service_name, 'log_fallback' AS source
            FROM log_error_spans_fallback
        ),

        -- 5. Find DEEPEST error generators (no child with errors)
        error_span_children AS (
            SELECT DISTINCT
                T.parent_span_id,
                T.trace_id
            FROM all_error_spans child
            INNER JOIN traces_bad T
            ON child.trace_id = T.trace_id AND child.span_id = T.span_id
            WHERE T.parent_span_id IS NOT NULL
        )
        SELECT
            parent.span_name,
            parent.service_name,
            COUNT(DISTINCT parent.trace_id || '::' || parent.span_id) AS error_count,
            ARRAY_AGG(DISTINCT parent.source) AS error_sources
        FROM all_error_spans parent
        LEFT JOIN error_span_children esc
        ON parent.span_id = esc.parent_span_id
        AND parent.trace_id = esc.trace_id
        WHERE esc.parent_span_id IS NULL  -- No children with errors
        GROUP BY parent.span_name, parent.service_name
        """

        try:
            bad_error_generators = self.con.execute(query_bad).fetchall()
        except Exception as e:
            if debug():
                logger.warning(f"Step 1 failed: {e}")
            return []

        if not bad_error_generators:
            if debug():
                logger.debug("  No leaf error generators found in bad period")
            return []

        if debug():
            logger.debug(f"  Found {len(bad_error_generators)} leaf error generators")
            for span_name, service_name, count, sources in bad_error_generators[:3]:
                sources_str = ",".join(sources) if isinstance(sources, list) else str(sources)
                logger.debug(f"    {service_name}.{span_name[:50]} (count={count}, sources=[{sources_str}])")

        # ====================================================================
        # STEP 2: Find baseline errors in GOOD period
        # ====================================================================
        if debug():
            logger.debug("[Step 2/3] Finding baseline errors in GOOD period...")

        # Build list of (span_name, service_name) to search for in good period
        bad_span_signatures = [(span_name, service_name) for span_name, service_name, _, _ in bad_error_generators]

        if not bad_span_signatures:
            good_error_generators = []
        else:
            # Build SQL IN clause for filtering
            span_filters = " OR ".join(
                [f"(span_name = '{sn}' AND service_name = '{sv}')" for sn, sv in bad_span_signatures[:100]]
            )

            query_good = f"""
            WITH good_trace_ids AS (
                SELECT DISTINCT trace_id
                FROM traces_good
                WHERE span_name = '{obs.entry_span_name}'
                LIMIT {len(obs.trace_ids) * 10}
            ),
            -- Error spans from TRACES in good period (for the spans we found in bad period)
            good_trace_errors AS (
                SELECT trace_id, span_id, span_name, service_name
                FROM traces_good
                WHERE trace_id IN (SELECT trace_id FROM good_trace_ids)
                AND ({span_filters})
                AND ("attr.status_code" = 'Error'
                    OR "attr.http.response.status_code" >= {self.HTTP_ERROR_STATUS_CODE})
            ),
            -- Error spans from LOGS (gold standard) in good period
            good_log_errors_gold AS (
                SELECT DISTINCT L.trace_id, L.span_id, T.span_name, T.service_name
                FROM logs_good L
                INNER JOIN traces_good T ON L.trace_id = T.trace_id AND L.span_id = T.span_id
                WHERE L.trace_id IN (SELECT trace_id FROM good_trace_ids)
                AND L.level NOT IN ('INFO')
                AND L.trace_id IS NOT NULL AND L.span_id IS NOT NULL
            ),
            good_log_errors_gold_filtered AS (
                SELECT trace_id, span_id, span_name, service_name
                FROM good_log_errors_gold
                WHERE ({span_filters})
            ),
            -- Error spans from LOGS (fallback) in good period
            good_log_errors_fallback AS (
                SELECT DISTINCT T.trace_id, T.span_id, T.span_name, T.service_name
                FROM logs_good L
                INNER JOIN traces_good T
                ON L."attr.k8s.pod.name" = T."attr.k8s.pod.name"
                AND L.time BETWEEN T.time - INTERVAL 1 SECOND AND T.time + INTERVAL 1 SECOND
                WHERE L.trace_id IN (SELECT trace_id FROM good_trace_ids)
                AND L.level NOT IN ('INFO')
                AND L."attr.k8s.pod.name" IS NOT NULL
                AND (L.trace_id IS NULL OR L.span_id IS NULL)
            ),
            good_log_errors_fallback_filtered AS (
                SELECT trace_id, span_id, span_name, service_name
                FROM good_log_errors_fallback
                WHERE ({span_filters})
            ),
            -- UNION all error sources
            all_good_errors AS (
                SELECT trace_id, span_id, span_name, service_name FROM good_trace_errors
                UNION
                SELECT trace_id, span_id, span_name, service_name FROM good_log_errors_gold_filtered
                UNION
                SELECT trace_id, span_id, span_name, service_name FROM good_log_errors_fallback_filtered
            )
            SELECT
                span_name,
                service_name,
                COUNT(DISTINCT trace_id || '::' || span_id) AS error_count
            FROM all_good_errors
            GROUP BY span_name, service_name
            """

            try:
                good_error_generators = self.con.execute(query_good).fetchall()
            except Exception as e:
                if debug():
                    logger.warning(f"Step 2 failed: {e}")
                good_error_generators = []

        # Build index for quick lookup
        good_errors_index = {
            (span_name, service_name): count for span_name, service_name, count in good_error_generators
        }

        if debug():
            logger.debug(f"  Found {len(good_error_generators)} span types with baseline errors")
            for span_name, service_name, count in good_error_generators[:3]:
                logger.debug(f"    {service_name}.{span_name[:50]} (count={count})")

        # ====================================================================
        # STEP 3: Calculate DELTA and filter
        # ====================================================================
        if debug():
            logger.debug("[Step 3/3] Calculating DELTA (bad - good) and filtering...")

        symptoms = []
        deltas = []

        for span_name, service_name, bad_count, sources in bad_error_generators:
            good_count = good_errors_index.get((span_name, service_name), 0)
            delta = bad_count - good_count

            if delta <= 0:
                continue
            if span_name == obs.entry_span_name:
                continue

            deltas.append((span_name, service_name, delta, bad_count, good_count, sources))

        deltas.sort(key=lambda x: x[2], reverse=True)
        top_deltas = deltas[: self.TOP_N_SYMPTOMS]

        if debug():
            logger.debug(f"  Filtered results: {len(deltas)} candidates, taking top {len(top_deltas)}")

        for (
            span_name,
            service_name,
            delta,
            bad_count,
            good_count,
            sources,
        ) in top_deltas:
            symptoms.append(
                Symptom(
                    symptom_type=SymptomType.ERROR_RATE,
                    service_name=service_name,
                    span_name=span_name,
                    impact_score=float(delta),
                )
            )

            if debug():
                sources_str = ",".join(sources) if isinstance(sources, list) else str(sources)
                logger.debug(
                    f"  ✓ Error symptom: {service_name}.{span_name[:50]} "
                    f"(Δ={delta}, bad={bad_count}, good={good_count}, "
                    f"sources=[{sources_str}])"
                )

        return symptoms
