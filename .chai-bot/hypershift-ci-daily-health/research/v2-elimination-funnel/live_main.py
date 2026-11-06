#!/usr/bin/env python3
"""LIVE run of the v2 funnel against one or more branches' required presubmits (real Prow data).

  python3 -I research/v2-elimination-funnel/live_main.py [branch ...]      # default: main
  e.g. python3 -I research/v2-elimination-funnel/live_main.py release-4.22 release-4.21 release-4.20

Classification uses the companion's ~72h Prow window (per job). Branch throughput (for the Gate-4
high/low split) is measured separately over a FIXED ~10-business-day window as
distinct-PRs ÷ window, via the branch's most active required job — stable against backport bursts.
Hits the network (dashboard + Prow). Read-only; nothing is written.
"""
from __future__ import annotations

import datetime as dt
import importlib.util
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent


def _load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    mod = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(mod)
    return mod


P = _load("permafail_prototype", HERE / "permafail_prototype.py")
M = P.M
UTC = dt.timezone.utc
BRANCHES = sys.argv[1:] or ["main"]

THROUGHPUT_WINDOW_BD = 10          # fixed baseline window (business days)
HI_PR_PER_BD = P.HI_PR_PER_BD      # derived = T / report interval (>= this PRs/bd -> high)


def branch_pr_count(collector, url, as_of, window_bd):
    """Distinct PRs on a job's Prow history within the last `window_bd` business days."""
    if not url:
        return 0, 0, False
    window_start = P.minus_bdays(as_of, window_bd)
    prs, page_url, pages, capped = set(), url, 0, False
    while page_url:
        if pages >= M.MAX_HISTORY_PAGES:
            capped = True
            break
        pages += 1
        try:
            text = collector.transport.get_text(page_url)
        except M.NoDataError:
            break
        rows, older, _ = M.parse_prow_history(text, page_url)
        reached_old = False
        for row in rows:
            started = row.get("_started")
            if isinstance(started, dt.datetime) and started < window_start:
                reached_old = True
                continue
            pulls = (row.get("Refs") or {}).get("pulls") or []
            if pulls:
                key = pulls[0].get("number") or pulls[0].get("sha")
                if key:
                    prs.add(str(key))
        if reached_old or not older:
            break
        page_url = older
    return len(prs), pages, capped


def run_branch(collector, as_of, plan, branch):
    bps = sorted((p for p in plan["presubmits"] if p.get("target_branch") == branch),
                 key=lambda x: x["name"])
    print(f"\n==================== {branch}  ({len(bps)} required presubmits) ====================")
    if not bps:
        print("  (no in-scope required presubmits for this branch)")
        return
    jobinfo = []  # (presubmit, name, current, runs)
    for p in bps:
        try:
            hist = collector._presubmit_history(p)
            jobinfo.append((p, p["name"], hist["classification"]["classification"], hist["runs"]))
        except Exception as exc:  # noqa: BLE001
            jobinfo.append((p, p["name"], "ERR:" + str(exc)[:30], []))

    # throughput over a FIXED 10bd window via the branch's most active required job
    active = [ji for ji in jobinfo if not ji[2].startswith("ERR") and ji[3]]
    rep = max(active, key=lambda ji: len(ji[3]), default=None)
    if rep:
        pr_count, pages, capped = branch_pr_count(collector, rep[0].get("prow_job_history_url"),
                                                  as_of, THROUGHPUT_WINDOW_BD)
        pr_per_bd = pr_count / THROUGHPUT_WINDOW_BD
    else:
        pr_count, pages, capped, pr_per_bd = 0, 0, False, 0.0
    volume = "high" if pr_per_bd >= HI_PR_PER_BD else "low"
    rep_name = rep[1].replace("pull-ci-openshift-hypershift-", "") if rep else "(none)"
    print(f"  throughput (via {rep_name}): {pr_count} distinct PRs / {THROUGHPUT_WINDOW_BD} bd "
          f"= {pr_per_bd:.2f} PRs/bd{' [page-cap hit]' if capped else ''}  →  volume={volume}")

    hdr = f"  {'presubmit':40} | {'#runs':>5} {'#PRs':>4} | {'CURRENT':>12} | {'v2 OUTCOME':>14}  {'gate/why':34}  prow history"
    print(hdr)
    print("  " + "-" * (len(hdr) - 2))
    tally = {}
    for p, name, cur, runs in jobinfo:
        short = name.replace("pull-ci-openshift-hypershift-", "")
        url = p.get("prow_job_history_url") or ""
        if cur.startswith("ERR"):
            print(f"  {short:40} | {'-':>5} {'-':>4} | {'ERR':>12} | {'-':>14}  {cur:34}  {url}")
            continue
        outcome, why = P.classify_funnel(runs, as_of, volume=volume)
        nprs = len({r.get("head_sha") for r in runs if r.get("head_sha")})
        tally[outcome] = tally.get(outcome, 0) + 1
        print(f"  {short:40} | {len(runs):>5} {nprs:>4} | {cur:>12} | {outcome:>14}  {why:34}  {url}")
    print("  tally:", {k: tally[k] for k in sorted(tally)})


def main():
    as_of = dt.datetime.now(tz=UTC)
    transport = M.HTTPTransport(timeout=25.0)
    collector = M.Collector(transport, as_of, M.checked_out_source_revision(),
                            deadline_seconds=450.0, max_requests=8000)
    jobs = collector._fetch_job_registry()
    plan = collector._select_plan(jobs)
    print(f"as_of = {as_of:%Y-%m-%d %a %H:%MZ}   dev={plan['dev_release']}   scope={plan['releases']}")
    print(f"classification window ≈ 72h; throughput window = {THROUGHPUT_WINDOW_BD} bd, HI = {HI_PR_PER_BD} PRs/bd")
    for b in BRANCHES:
        run_branch(collector, as_of, plan, b)


if __name__ == "__main__":
    main()
