"""Generate markdown analysis report with matplotlib charts from judged evaluation samples."""

from __future__ import annotations

from collections import Counter, defaultdict
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

import matplotlib

matplotlib.use("Agg")

import matplotlib.pyplot as plt
import numpy as np

from ..db.eval_datapoint import EvaluationSample

_KNOWN_SYSTEMS = ["otel-demo", "sockshop", "tea", "ts", "hs", "sn", "mm"]

_MATCH_COLORS = {"HIT": "#2ecc71", "WRONG_KIND": "#f39c12", "MISS": "#e74c3c"}


def _extract_system(source: str) -> str:
    for prefix in sorted(_KNOWN_SYSTEMS, key=len, reverse=True):
        if source.startswith(prefix):
            return prefix
    return "other"


def _get_eval_metrics(sample: EvaluationSample) -> dict[str, Any] | None:
    ev = sample.eval_metrics
    if isinstance(ev, dict) and "error" not in ev:
        return ev
    if isinstance(sample.meta, dict):
        ev2 = sample.meta.get("eval_v2")
        if isinstance(ev2, dict) and "error" not in ev2:
            return ev2
    return None


class EvalReportGenerator:
    """Generate markdown analysis report from judged samples."""

    def __init__(
        self,
        samples: list[EvaluationSample],
        output_dir: Path,
        exp_id: str,
        agent_type: str | None = None,
        model_name: str | None = None,
    ) -> None:
        self.samples = samples
        self.output_dir = Path(output_dir)
        self.exp_id = exp_id
        self.agent_type = agent_type
        self.model_name = model_name

        self.scored: list[tuple[EvaluationSample, dict[str, Any]]] = []
        for s in samples:
            ev = _get_eval_metrics(s)
            if ev is not None:
                self.scored.append((s, ev))

    def generate(self) -> Path:
        self.output_dir.mkdir(parents=True, exist_ok=True)

        try:
            plt.style.use("seaborn-v0_8-whitegrid")
        except OSError:
            pass

        sections = [
            self._section_executive_summary(),
            self._section_capability_profile(),
            self._section_failure_mode(),
            self._section_stratified(),
            self._section_evidence_chain(),
            self._section_score_distribution(),
        ]

        report_path = self.output_dir / "report.md"
        report_path.write_text("\n\n".join(sections) + "\n")
        return report_path

    # ── Section 1: Executive Summary ────────────────────────────────────

    def _section_executive_summary(self) -> str:
        n = len(self.samples)
        scored = len(self.scored)
        if scored == 0:
            return self._heading(1, "Executive Summary") + "\n\nNo scored samples found."

        exact = sum(1 for _, ev in self.scored if ev.get("exact_match"))
        f1_sum = sum(float(ev.get("f1") or 0) for _, ev in self.scored)
        svc_f1_sum = sum(float(ev.get("service_f1") or 0) for _, ev in self.scored)
        pr_sum = sum(1 for _, ev in self.scored if ev.get("path_reachability"))
        sql_sum = sum(float(ev.get("sql_executable_rate") or 0) for _, ev in self.scored)
        esr_sum = sum(float(ev.get("evidence_support_rate") or 0) for _, ev in self.scored)
        parse_errors = sum(1 for _, ev in self.scored if ev.get("parse_error"))

        d = max(1, n)
        rows = [
            ("Total samples", str(n)),
            ("Scored samples", str(scored)),
            ("Exact match rate", f"{exact / d:.1%}"),
            ("Avg F1", f"{f1_sum / d:.4f}"),
            ("Avg service F1", f"{svc_f1_sum / d:.4f}"),
            ("Avg path reachability", f"{pr_sum / d:.1%}"),
            ("Avg SQL executable rate", f"{sql_sum / d:.4f}"),
            ("Avg evidence support rate", f"{esr_sum / d:.4f}"),
            ("Parse errors", str(parse_errors)),
        ]

        lines = [
            self._heading(1, "Executive Summary"),
            "",
            f"**exp_id:** `{self.exp_id}`  ",
            f"**agent_type:** `{self.agent_type or 'N/A'}`  ",
            f"**model_name:** `{self.model_name or 'N/A'}`  ",
            f"**generated:** {datetime.now(timezone.utc).strftime('%Y-%m-%d %H:%M UTC')}",
            "",
            "| Metric | Value |",
            "|--------|-------|",
        ]
        for label, val in rows:
            lines.append(f"| {label} | {val} |")

        return "\n".join(lines)

    # ── Section 2: Capability Profile (Radar) ───────────────────────────

    def _section_capability_profile(self) -> str:
        if not self.scored:
            return self._heading(2, "Capability Profile") + "\n\nInsufficient data."

        n = len(self.samples)
        d = max(1, n)

        f1_sum = sum(float(ev.get("f1") or 0) for _, ev in self.scored)
        svc_f1_sum = sum(float(ev.get("service_f1") or 0) for _, ev in self.scored)
        kind_vals = [
            float(ev["fault_kind_accuracy"]) for _, ev in self.scored if ev.get("fault_kind_accuracy") is not None
        ]
        pr_sum = sum(1 for _, ev in self.scored if ev.get("path_reachability"))
        sql_sum = sum(float(ev.get("sql_executable_rate") or 0) for _, ev in self.scored)
        esr_sum = sum(float(ev.get("evidence_support_rate") or 0) for _, ev in self.scored)

        axes = [
            ("Service Localization", svc_f1_sum / d),
            ("Fault Classification", (sum(kind_vals) / len(kind_vals)) if kind_vals else 0.0),
            ("Strict Accuracy", f1_sum / d),
            ("Causal Reasoning", pr_sum / d),
            ("SQL Quality", sql_sum / d),
            ("Evidence Quality", esr_sum / d),
        ]

        labels = [a[0] for a in axes]
        values = [a[1] for a in axes]

        N = len(labels)
        angles = np.linspace(0, 2 * np.pi, N, endpoint=False).tolist()
        values_closed = values + [values[0]]
        angles_closed = angles + [angles[0]]

        fig, ax = plt.subplots(figsize=(6, 6), subplot_kw={"polar": True})
        ax.plot(angles_closed, values_closed, "o-", linewidth=2, color="#3498db")
        ax.fill(angles_closed, values_closed, alpha=0.25, color="#3498db")
        ax.set_xticks(angles)
        ax.set_xticklabels(labels, size=9)
        ax.set_ylim(0, 1)
        ax.set_title("Capability Profile", size=13, pad=20)

        chart_path = self.output_dir / "capability_radar.png"
        fig.savefig(chart_path, dpi=150, bbox_inches="tight")
        plt.close(fig)

        lines = [
            self._heading(2, "Capability Profile"),
            "",
            "![](capability_radar.png)",
            "",
            "| Axis | Score |",
            "|------|-------|",
        ]
        for label, val in axes:
            lines.append(f"| {label} | {val:.4f} |")

        return "\n".join(lines)

    # ── Section 3: Failure Mode Analysis ────────────────────────────────

    def _section_failure_mode(self) -> str:
        all_per_fault: list[dict[str, Any]] = []
        for _, ev in self.scored:
            pf = ev.get("per_fault")
            if isinstance(pf, list):
                all_per_fault.extend(pf)

        if not all_per_fault:
            return self._heading(2, "Failure Mode Analysis") + "\n\nNo per-fault data."

        status_counts: Counter[str] = Counter()
        kind_by_status: dict[str, Counter[str]] = defaultdict(Counter)
        for entry in all_per_fault:
            st = entry.get("status", "MISS")
            kind = entry.get("gt_fault_kind", "unknown")
            status_counts[st] += 1
            kind_by_status[st][kind] += 1

        # Bar chart
        statuses = ["HIT", "WRONG_KIND", "MISS"]
        counts = [status_counts.get(s, 0) for s in statuses]
        colors = [_MATCH_COLORS.get(s, "#95a5a6") for s in statuses]

        fig, ax = plt.subplots(figsize=(8, 4))
        y_pos = range(len(statuses))
        ax.barh(y_pos, counts, color=colors)
        ax.set_yticks(list(y_pos))
        ax.set_yticklabels(statuses)
        ax.set_xlabel("Count")
        ax.set_title("Match Status Distribution (all per-fault entries)")
        for i, c in enumerate(counts):
            ax.text(c + 0.5, i, str(c), va="center")

        chart_path = self.output_dir / "match_status_dist.png"
        fig.savefig(chart_path, dpi=150, bbox_inches="tight")
        plt.close(fig)

        lines = [
            self._heading(2, "Failure Mode Analysis"),
            "",
            "### Match Status Distribution",
            "",
            "![](match_status_dist.png)",
            "",
            "| Status | Count |",
            "|--------|-------|",
        ]
        for s in statuses:
            lines.append(f"| {s} | {status_counts.get(s, 0)} |")

        # GT fault kind breakdown by status
        lines.append("")
        lines.append("### GT Fault Kind by Match Status")
        lines.append("")
        all_kinds = sorted({k for cnt in kind_by_status.values() for k in cnt})
        lines.append("| Fault Kind | " + " | ".join(statuses) + " |")
        lines.append("|" + "---|" * (len(statuses) + 1))
        for kind in all_kinds:
            row = " | ".join(str(kind_by_status[s].get(kind, 0)) for s in statuses)
            lines.append(f"| {kind} | {row} |")

        # Overclaim stats
        overclaim_cases = sum(1 for _, ev in self.scored if ev.get("overclaim_indices"))
        total_overclaims = sum(len(ev.get("overclaim_indices", [])) for _, ev in self.scored)
        lines.extend(
            [
                "",
                "### Overclaims",
                "",
                f"Cases with overclaims: **{overclaim_cases}** / {len(self.scored)}  ",
                f"Total overclaimed root causes: **{total_overclaims}**",
            ]
        )

        return "\n".join(lines)

    # ── Section 4: Stratified Analysis ──────────────────────────────────

    def _section_stratified(self) -> str:
        if not self.scored:
            return self._heading(2, "Stratified Analysis") + "\n\nInsufficient data."

        lines = [self._heading(2, "Stratified Analysis")]

        # By fault type
        fault_groups: dict[str, list[dict[str, Any]]] = defaultdict(list)
        for _, ev in self.scored:
            pf = ev.get("per_fault")
            if not isinstance(pf, list):
                continue
            kinds_seen: set[str] = set()
            for entry in pf:
                kind = entry.get("gt_fault_kind", "unknown")
                if kind not in kinds_seen:
                    kinds_seen.add(kind)
                    fault_groups[kind].append(ev)

        if fault_groups:
            lines.append("")
            lines.append("### By Fault Type")
            lines.append("")
            lines.append("| Fault Type | Cases | Avg F1 | Exact Match Rate |")
            lines.append("|------------|-------|--------|------------------|")

            ft_labels = []
            ft_f1s = []
            for kind in sorted(fault_groups):
                evs = fault_groups[kind]
                n_k = len(evs)
                avg_f1 = sum(float(e.get("f1") or 0) for e in evs) / max(1, n_k)
                em_rate = sum(1 for e in evs if e.get("exact_match")) / max(1, n_k)
                lines.append(f"| {kind} | {n_k} | {avg_f1:.4f} | {em_rate:.1%} |")
                ft_labels.append(kind)
                ft_f1s.append(avg_f1)

            fig, ax = plt.subplots(figsize=(10, max(4, len(ft_labels) * 0.4)))
            y_pos = range(len(ft_labels))
            ax.barh(list(y_pos), ft_f1s, color="#3498db")
            ax.set_yticks(list(y_pos))
            ax.set_yticklabels(ft_labels, fontsize=8)
            ax.set_xlabel("Avg F1")
            ax.set_xlim(0, 1)
            ax.set_title("Avg F1 by Fault Type")
            chart_path = self.output_dir / "by_fault_type.png"
            fig.savefig(chart_path, dpi=150, bbox_inches="tight")
            plt.close(fig)

            lines.extend(["", "![](by_fault_type.png)"])

        # By system
        system_groups: dict[str, list[dict[str, Any]]] = defaultdict(list)
        for s, ev in self.scored:
            sys = _extract_system(s.source)
            system_groups[sys].append(ev)

        if system_groups:
            lines.append("")
            lines.append("### By System")
            lines.append("")
            lines.append("| System | Cases | Avg F1 | Exact Match Rate | Avg Service F1 |")
            lines.append("|--------|-------|--------|------------------|----------------|")

            sys_labels = []
            sys_f1s = []
            for sys in sorted(system_groups):
                evs = system_groups[sys]
                n_s = len(evs)
                avg_f1 = sum(float(e.get("f1") or 0) for e in evs) / max(1, n_s)
                em_rate = sum(1 for e in evs if e.get("exact_match")) / max(1, n_s)
                avg_sf1 = sum(float(e.get("service_f1") or 0) for e in evs) / max(1, n_s)
                lines.append(f"| {sys} | {n_s} | {avg_f1:.4f} | {em_rate:.1%} | {avg_sf1:.4f} |")
                sys_labels.append(sys)
                sys_f1s.append(avg_f1)

            fig, ax = plt.subplots(figsize=(8, max(3, len(sys_labels) * 0.5)))
            y_pos = range(len(sys_labels))
            ax.barh(list(y_pos), sys_f1s, color="#2ecc71")
            ax.set_yticks(list(y_pos))
            ax.set_yticklabels(sys_labels)
            ax.set_xlabel("Avg F1")
            ax.set_xlim(0, 1)
            ax.set_title("Avg F1 by System")
            chart_path = self.output_dir / "by_system.png"
            fig.savefig(chart_path, dpi=150, bbox_inches="tight")
            plt.close(fig)

            lines.extend(["", "![](by_system.png)"])

        # By complexity
        single_fault = [(s, ev) for s, ev in self.scored if len(ev.get("per_fault") or []) == 1]
        multi_fault = [(s, ev) for s, ev in self.scored if len(ev.get("per_fault") or []) > 1]

        if single_fault or multi_fault:
            lines.append("")
            lines.append("### By Complexity (single vs multi-fault)")
            lines.append("")
            lines.append("| Complexity | Cases | Avg F1 | Exact Match Rate |")
            lines.append("|------------|-------|--------|------------------|")
            for label, group in [("Single-fault", single_fault), ("Multi-fault", multi_fault)]:
                n_g = len(group)
                if n_g == 0:
                    lines.append(f"| {label} | 0 | N/A | N/A |")
                    continue
                avg_f1 = sum(float(ev.get("f1") or 0) for _, ev in group) / n_g
                em_rate = sum(1 for _, ev in group if ev.get("exact_match")) / n_g
                lines.append(f"| {label} | {n_g} | {avg_f1:.4f} | {em_rate:.1%} |")

        return "\n".join(lines)

    # ── Section 5: Evidence Chain Analysis ──────────────────────────────

    def _section_evidence_chain(self) -> str:
        lines = [self._heading(2, "Evidence Chain Analysis")]

        evidence_counts: list[int] = []
        status_counter: Counter[str] = Counter()
        high_sql_low_support: list[str] = []

        for s, ev in self.scored:
            n_ev = int(ev.get("n_evidence") or 0)
            evidence_counts.append(n_ev)

            per_ev = ev.get("per_evidence")
            if isinstance(per_ev, list):
                for rec in per_ev:
                    st = rec.get("status", "UNKNOWN")
                    status_counter[st] += 1

            sql_rate = float(ev.get("sql_executable_rate") or 0)
            esr = ev.get("evidence_support_rate")
            esr_val = float(esr) if esr is not None else None
            if sql_rate >= 0.8 and esr_val is not None and esr_val < 0.4:
                high_sql_low_support.append(s.source)

        if not evidence_counts:
            lines.append("\nNo evidence data.")
            return "\n".join(lines)

        # Histogram
        fig, ax = plt.subplots(figsize=(8, 4))
        max_count = max(evidence_counts) if evidence_counts else 0
        bins = range(0, max_count + 2)
        ax.hist(evidence_counts, bins=list(bins), color="#3498db", edgecolor="white", align="left")
        ax.set_xlabel("Number of evidences per case")
        ax.set_ylabel("Frequency")
        ax.set_title("Evidence Count Distribution")
        chart_path = self.output_dir / "evidence_count_dist.png"
        fig.savefig(chart_path, dpi=150, bbox_inches="tight")
        plt.close(fig)

        lines.extend(
            [
                "",
                "### Evidence Count Distribution",
                "",
                "![](evidence_count_dist.png)",
                "",
                f"Mean evidences per case: **{np.mean(evidence_counts):.1f}**  ",
                f"Median: **{np.median(evidence_counts):.0f}**  ",
                f"Max: **{max(evidence_counts)}**",
            ]
        )

        # SQL status breakdown
        if status_counter:
            lines.extend(
                [
                    "",
                    "### SQL Status Breakdown",
                    "",
                    "| Status | Count | Share |",
                    "|--------|-------|-------|",
                ]
            )
            total_ev = sum(status_counter.values())
            for st in ["OK", "EMPTY", "SQL_ERROR"]:
                c = status_counter.get(st, 0)
                pct = c / max(1, total_ev)
                lines.append(f"| {st} | {c} | {pct:.1%} |")
            others = {k: v for k, v in status_counter.items() if k not in {"OK", "EMPTY", "SQL_ERROR"}}
            for st, c in sorted(others.items()):
                pct = c / max(1, total_ev)
                lines.append(f"| {st} | {c} | {pct:.1%} |")

        # Gap analysis
        if high_sql_low_support:
            lines.extend(
                [
                    "",
                    "### Gap Analysis: SQL executable but evidence unsupported",
                    "",
                    f"**{len(high_sql_low_support)}** cases have sql_executable_rate >= 0.8 "
                    f"but evidence_support_rate < 0.4 (SQL works but claims don't hold up):",
                    "",
                ]
            )
            for src in high_sql_low_support[:20]:
                lines.append(f"- `{src}`")
            if len(high_sql_low_support) > 20:
                lines.append(f"- ... and {len(high_sql_low_support) - 20} more")

        return "\n".join(lines)

    # ── Section 6: Score Distribution ───────────────────────────────────

    def _section_score_distribution(self) -> str:
        if not self.scored:
            return self._heading(2, "Score Distribution") + "\n\nInsufficient data."

        f1_values = [float(ev.get("f1") or 0) for _, ev in self.scored]

        fig, ax = plt.subplots(figsize=(8, 4))
        ax.hist(f1_values, bins=20, range=(0, 1), color="#3498db", edgecolor="white")
        ax.set_xlabel("F1 Score")
        ax.set_ylabel("Frequency")
        ax.set_title("F1 Score Distribution")
        ax.axvline(float(np.mean(f1_values)), color="#e74c3c", linestyle="--", label=f"Mean={np.mean(f1_values):.3f}")
        ax.legend()

        chart_path = self.output_dir / "f1_distribution.png"
        fig.savefig(chart_path, dpi=150, bbox_inches="tight")
        plt.close(fig)

        perfect = sum(1 for v in f1_values if v == 1.0)
        zero = sum(1 for v in f1_values if v == 0.0)
        partial = len(f1_values) - perfect - zero

        lines = [
            self._heading(2, "Score Distribution"),
            "",
            "![](f1_distribution.png)",
            "",
            "| Bucket | Count | Share |",
            "|--------|-------|-------|",
            f"| F1 = 1.0 (perfect) | {perfect} | {perfect / max(1, len(f1_values)):.1%} |",
            f"| 0 < F1 < 1 (partial) | {partial} | {partial / max(1, len(f1_values)):.1%} |",
            f"| F1 = 0.0 | {zero} | {zero / max(1, len(f1_values)):.1%} |",
            "",
            f"Mean F1: **{np.mean(f1_values):.4f}**  ",
            f"Std: **{np.std(f1_values):.4f}**  ",
            f"Median: **{np.median(f1_values):.4f}**",
        ]

        return "\n".join(lines)

    # ── Helpers ─────────────────────────────────────────────────────────

    @staticmethod
    def _heading(level: int, text: str) -> str:
        return "#" * level + " " + text
