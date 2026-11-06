#!/usr/bin/env python3
"""Prototype: statistically-normalized permafail gate vs the current absolute-count gate.

Run from the skill dir:  python3 -I research/permafail_prototype.py

Imports the companion module for the CURRENT classifier (classify_presubmit_streak) and
implements the PROPOSED gate: a per-head Wilson lower bound (breadth/independence, accounts for
sample size and unique PRs) GATED by a recency guard (currency) and a persistence span. Pure stdlib.
"""
from __future__ import annotations

import datetime as dt
import importlib.util
import math
from pathlib import Path

HERE = Path(__file__).resolve().parent
SCRIPT = HERE.parent.parent / "scripts" / "hypershift-ci-daily-health.py"
_spec = importlib.util.spec_from_file_location("hcih", SCRIPT)
M = importlib.util.module_from_spec(_spec)
assert _spec.loader is not None
_spec.loader.exec_module(M)

UTC = dt.timezone.utc
AS_OF = dt.datetime(2026, 10, 9, tzinfo=UTC)


def wilson_lower(r: int, n: int, z: float) -> float:
    if n == 0:
        return 0.0
    p = r / n
    denom = 1.0 + z * z / n
    centre = p + z * z / (2 * n)
    margin = z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n))
    return max(0.0, (centre - margin) / denom)


def classify_stat(runs, as_of, *, window_h=14 * 24, z=2.33, p_star=0.75,
                  k_min=3, d_hours=48.0, recovery_h=24.0):
    """Per-head Wilson lower bound, gated by a recency guard (currency) + persistence span.

    breadth  : L = WilsonLower(failed_heads / tested_heads)  — accounts for n and unique PRs
    currency : still_red = no SUCCESS in the last recovery_h — "broken now", not recovering
    period   : span (earliest fail -> now) >= d_hours        — long-standing, not a fresh blip
    """
    lo = as_of - dt.timedelta(hours=window_h)
    testable = [r for r in runs
                if str(r.get("state")) in {"SUCCESS", "FAILURE"}
                and M.parse_rfc3339(r["completed"]) >= lo]
    heads: dict[str, dict] = {}
    fail_times, pass_times = [], []
    for r in testable:
        h = r.get("head_sha") or "?"
        slot = heads.setdefault(h, {"pass": False, "fail": False})
        if str(r.get("state")) == "SUCCESS":
            slot["pass"] = True
            pass_times.append(M.parse_rfc3339(r["completed"]))
        else:
            slot["fail"] = True
            fail_times.append(M.parse_rfc3339(r["completed"]))
    n_eff = len(heads)
    r_eff = sum(1 for v in heads.values() if v["fail"] and not v["pass"])  # PRs that never passed
    k = r_eff
    L = wilson_lower(r_eff, n_eff, z)
    span = ((as_of - min(fail_times)).total_seconds() / 3600.0) if fail_times else 0.0
    last_pass = max(pass_times) if pass_times else None
    hrs_since_pass = ((as_of - last_pass).total_seconds() / 3600.0) if last_pass else None
    still_red = (last_pass is None) or (hrs_since_pass >= recovery_h)

    if n_eff == 0:
        cls = "unknown"
    elif not still_red:                      # a pass within recovery_h -> recovering
        cls = "candidate" if r_eff else "not_flagged"
    elif span < d_hours:                     # not yet sustained
        cls = "candidate" if r_eff else "not_flagged"
    elif k >= k_min and L >= p_star:         # broad, confident, sustained, still red
        cls = "permafailing"
    elif r_eff:
        cls = "candidate"
    else:
        cls = "not_flagged"
    return {"class": cls, "n_eff": n_eff, "r_eff": r_eff, "L": round(L, 3),
            "still_red": still_red,
            "hrs_since_pass": round(hrs_since_pass, 1) if hrs_since_pass is not None else None,
            "span_h": round(span, 1)}


def mk(state, head, h_ago):
    return {"state": state, "head_sha": head,
            "completed": (AS_OF - dt.timedelta(hours=h_ago)).strftime("%Y-%m-%dT%H:%M:%SZ")}


def build():
    s = {}
    # --- factor isolation ---
    s["A release break (4 PRs)"] = [
        mk("FAILURE", h, t) for h, ts in
        {"A1": (72, 66), "A2": (54, 48), "A3": (30, 24), "A4": (6, 2)}.items() for t in ts]
    s["A2 release break (28 PRs/14d)"] = [mk("FAILURE", f"S{i}", 336 - i * 12) for i in range(28)]
    s["B one bad PR (60 reds)"] = (
        [mk("FAILURE", "bad", 59 - i) for i in range(60)]
        + [mk("SUCCESS", f"ok{i}", 60 + i * 8) for i in range(30)])
    s["C main break (30 PRs/50h)"] = [mk("FAILURE", f"C{i}", round(50 - i * 1.7, 1)) for i in range(30)]
    s["D flaky (2 fail / 16 green)"] = (
        [mk("SUCCESS", f"P{i}", 60 - i * 3) for i in range(15)]
        + [mk("SUCCESS", "P15", 2), mk("FAILURE", "F1", 56), mk("FAILURE", "F2", 50)])
    s["E small all-fail (3 PRs/10h)"] = [mk("FAILURE", "E1", 10), mk("FAILURE", "E2", 6), mk("FAILURE", "E3", 2)]
    # --- recency guard: variants of A2 (base is permafailing) ---
    base = s["A2 release break (28 PRs/14d)"]
    s["  A2 + green NOW (new PR @0h)"] = base + [mk("SUCCESS", "G", 0)]
    s["  A2 + green @30h (red since; >recovery)"] = base + [mk("SUCCESS", "G", 30)]
    s["  A2 + green @12h (within recovery)"] = base + [mk("SUCCESS", "G", 12)]
    s["  A2 + RECOVERED (last 3 green @0/6/12h)"] = base + [
        mk("SUCCESS", "G1", 12), mk("SUCCESS", "G2", 6), mk("SUCCESS", "G3", 0)]
    return s


def main():
    print(f"CURRENT : tolerance={M.POST_GREEN_RED_TOLERANCE}  V={M.PERMAFAIL_VOLUME}  D={M.PERMAFAIL_DURATION_HOURS}h"
          "  (consecutive reds since last SUCCESS)")
    print("PROPOSED: per-head Wilson L  P*=0.75 z=2.33 K_min=3 D=48h  recovery_h=24h  window=14d\n")
    hdr = f"{'scenario':42} | {'CURRENT':>12}(strk) | {'PROPOSED':>12}  [n_eff r_eff  L   still_red  span]"
    print(hdr); print("-" * len(hdr))
    for name, runs in build().items():
        cur = M.classify_presubmit_streak(runs, AS_OF)
        new = classify_stat(runs, AS_OF)
        cs = cur.get("streak"); cs = len(cs) if isinstance(cs, list) else cs
        print(f"{name:42} | {cur['classification']:>12}({str(cs):>3}) | "
              f"{new['class']:>12}  [{new['n_eff']:>3} {new['r_eff']:>4}  {new['L']:.2f}   "
              f"{str(new['still_red']):>5}   {new['span_h']:>5}h]")


if __name__ == "__main__":
    main()
