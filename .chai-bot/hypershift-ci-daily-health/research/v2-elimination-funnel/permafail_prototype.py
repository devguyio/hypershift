#!/usr/bin/env python3
"""Prototype v2: presubmit permafail as an ELIMINATION FUNNEL.

Run from the skill dir:  python3 -I research/v2-elimination-funnel/permafail_prototype.py

Reported outcomes: UNDER-OBSERVED / PERMAFAILING / CANDIDATE  (eliminate = dropped, not reported).
Recency in BUSINESS DAYS. Terms: f = observed distinct failing PRs for the job (fail-only head_sha);
T = MIN_FAILING_PRS, the auto-confirm bar. The high/low throughput split is derived HI = T / report
interval (daily -> T per business day). Compared against the current classify_presubmit_streak.
"""
from __future__ import annotations

import datetime as dt
import importlib.util
from pathlib import Path

HERE = Path(__file__).resolve().parent
SCRIPT = HERE.parent.parent / "scripts" / "hypershift-ci-daily-health.py"
_spec = importlib.util.spec_from_file_location("hcih", SCRIPT)
M = importlib.util.module_from_spec(_spec)
assert _spec.loader is not None
_spec.loader.exec_module(M)

UTC = dt.timezone.utc
AS_OF = dt.datetime(2026, 10, 7, 18, tzinfo=UTC)
while AS_OF.weekday() >= 5:
    AS_OF -= dt.timedelta(days=1)

# ---- knobs (tune against real history) ----
STALE_BD = 2             # last run older than this many business days -> under-observed
GREEN_RECENCY_H = 24.0   # a SUCCESS within this many hours -> eliminate (flowing)
MIN_FAILING_PRS = 3      # T: distinct failing PRs needed to auto-confirm / reach the Gate-3 bar
D = 48.0                 # sustained permafail: red span (since last pass) >= this many hours
OVERWHELM_RATE = 0.5     # high-throughput: failing PRs >= this share of recent PRs -> candidate
REPORT_INTERVAL_BD = 1   # report cadence (business days); HI is derived from it
# derived, NOT hand-set: a branch is "high" if a real break would reach the Gate-3 bar before the
# next run -> it must produce at least T PRs per report interval.
HI_PR_PER_BD = MIN_FAILING_PRS / REPORT_INTERVAL_BD


def bdays_between(a, b):
    if b <= a:
        return 0
    cur, end, n = a.date(), b.date(), 0
    while cur < end:
        cur += dt.timedelta(days=1)
        if cur.weekday() < 5:
            n += 1
    return n


def minus_bdays(d, n):
    cur = d
    while n > 0:
        cur -= dt.timedelta(days=1)
        if cur.weekday() < 5:
            n -= 1
    return cur


def hours_between(a, b):
    return (b - a).total_seconds() / 3600.0


def classify_funnel(runs, as_of, *, volume):
    """volume: 'high'/'low' (from measured throughput vs HI). Returns (outcome, why)."""
    testable = sorted((r for r in runs if str(r.get("state")) in {"SUCCESS", "FAILURE"}),
                      key=lambda r: M.parse_rfc3339(r["completed"]), reverse=True)
    # GATE 0 — freshness
    if not testable:
        return ("under-observed", "G0 no runs")
    last = testable[0]
    if bdays_between(M.parse_rfc3339(last["completed"]), as_of) > STALE_BD:
        return ("under-observed", f"G0 last run {bdays_between(M.parse_rfc3339(last['completed']), as_of)}bd old")
    # per-head aggregation
    heads: dict[str, dict] = {}
    fail_times, pass_times = [], []
    for r in testable:
        slot = heads.setdefault(r.get("head_sha") or "?", {"p": False, "f": False})
        if str(r.get("state")) == "SUCCESS":
            slot["p"] = True
            pass_times.append(M.parse_rfc3339(r["completed"]))
        else:
            slot["f"] = True
            fail_times.append(M.parse_rfc3339(r["completed"]))
    n_eff = len(heads)
    f = sum(1 for v in heads.values() if v["f"] and not v["p"])   # distinct failing PRs
    last_pass = max(pass_times) if pass_times else None
    # GATE 1 — recent pass
    if str(last.get("state")) == "SUCCESS" or (
            last_pass is not None and hours_between(last_pass, as_of) <= GREEN_RECENCY_H):
        return ("eliminated", "G1 recent pass")
    # GATE 2 — one bad PR
    if f <= 1:
        return ("eliminated", f"G2 one bad PR (f={f})")
    # GATE 3 — sustained permafail (span measured since the last pass)
    streak_start = last_pass if last_pass else (min(fail_times) if fail_times else as_of)
    streak_span_h = hours_between(streak_start, as_of)
    if f >= MIN_FAILING_PRS and streak_span_h >= D:
        return ("PERMAFAILING", f"G3 sustained f={f} span={streak_span_h:.0f}h")
    # GATE 4 — throughput
    if volume == "low":
        return ("CANDIDATE", f"G4 low-throughput (f={f})")
    fail_rate = f / n_eff if n_eff else 0.0
    if f >= MIN_FAILING_PRS and fail_rate >= OVERWHELM_RATE:
        return ("CANDIDATE", f"G4 overwhelming f={f}/{n_eff}={fail_rate:.0%} (fresh, not yet sustained)")
    return ("eliminated", f"G4 high-throughput sparse f={f}/{n_eff}={fail_rate:.0%}")


def at(h_ago):
    return (AS_OF - dt.timedelta(hours=h_ago)).strftime("%Y-%m-%dT%H:%M:%SZ")


def at_bd(n, hour=12):
    return minus_bdays(AS_OF, n).replace(hour=hour, minute=0, second=0).strftime("%Y-%m-%dT%H:%M:%SZ")


def R(state, head, when):
    return {"state": state, "head_sha": head, "completed": when}


def build():
    return [
        ("no runs at all", [], "low"),
        ("last run green 3 bdays ago", [R("SUCCESS", "g", at_bd(3))], "low"),
        ("green today, prior reds ~4d apart",
         [R("SUCCESS", "g0", at(2)), R("FAILURE", "f1", at(96)), R("FAILURE", "f2", at(192))], "high"),
        ("red today, green ~12h ago",
         [R("FAILURE", "r0", at(1)), R("SUCCESS", "g1", at(12)), R("FAILURE", "r1", at(26))], "high"),
        ("1 PR retried red 60x / 50h", [R("FAILURE", "bad", at(i * 0.85)) for i in range(60)], "high"),
        ("release-4.20: 2 PRs red today",
         [R("FAILURE", "p1", at(1)), R("FAILURE", "p2", at(5))], "low"),
        ("main: 3 fresh reds among 20 older passes", (
            [R("FAILURE", f"r{i}", at(2 + i * 4)) for i in range(3)]
            + [R("SUCCESS", f"ok{i}", at(30 + i * 2)) for i in range(20)]), "high"),
        ("main: 20 fresh red PRs, span 30h (break just started)", (
            [R("FAILURE", f"R{i}", at(1 + i * 1.4)) for i in range(20)]
            + [R("SUCCESS", "g", at(30)), R("SUCCESS", "g2", at(36))]), "high"),
        ("main: 40 red PRs, span 52h (break sustained)", (
            [R("FAILURE", f"R{i}", at(1 + i * 1.25)) for i in range(40)]
            + [R("SUCCESS", "g", at(52))]), "high"),
    ]


def main():
    print(f"AS_OF = {AS_OF:%Y-%m-%d %a %H:%MZ}   T(MIN_FAILING_PRS)={MIN_FAILING_PRS}  D={D:.0f}h  "
          f"STALE_BD={STALE_BD}  GREEN_RECENCY_H={GREEN_RECENCY_H:.0f}  OVERWHELM_RATE={OVERWHELM_RATE:.0%}")
    print(f"HI = T / report_interval = {HI_PR_PER_BD:.0f} PRs/bd (derived)")
    print("v2 outcomes: UNDER-OBSERVED / PERMAFAILING / CANDIDATE  (eliminated = dropped)\n")
    hdr = f"{'scenario':48} {'vol':>5} | {'CURRENT':>12} | {'v2 OUTCOME':>13}  gate / why"
    print(hdr)
    print("-" * len(hdr))
    for name, runs, vol in build():
        cur = M.classify_presubmit_streak(runs, AS_OF)["classification"]
        outcome, why = classify_funnel(runs, AS_OF, volume=vol)
        print(f"{name:48} {vol:>5} | {cur:>12} | {outcome:>13}  {why}")


if __name__ == "__main__":
    main()
