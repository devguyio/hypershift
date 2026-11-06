# Research: presubmit permafail as an elimination funnel

**Status:** research / proposal (v2). Supersedes `research/v1-wilson-permafail/`. Not wired into the
companion. Prototype: `permafail_prototype.py` (offline scenarios). Live driver: `live_main.py`
(real Prow data, read-only).

## The script's role

The deterministic script is an **elimination funnel**, not a green/red oracle. It:

- **eliminates** the clear non-issues (dropped — *not* reported; there is no "green" output),
- **reports** the two things it can be sure of — **under-observed** and a **sustained permafail**,
- hands everything genuinely ambiguous to the **LLM** as a **candidate**.

Reported outcomes: `under-observed`, `permafailing`, `candidate`. Everything else is eliminated.
**Recency is the spine, in business days** (presubmits barely run on weekends). This is **presubmits
only** — periodics are deterministic (see end).

Terms: **`f`** = observed distinct failing PRs for the job (fail-only `head_sha` in the window);
**`T`** = `MIN_FAILING_PRS`, the bar to auto-confirm.

## Decision tree

```
PRESUBMIT (required job) — ordered testable runs (SUCCESS / FAILURE; ERROR & ABORTED neutral)

GATE 0 · stale?        last run older than STALE_BD   (or no testable runs)
                          └─► UNDER-OBSERVED        (→ action item: open a fake PR)
   ↓ fresh
GATE 1 · recent pass?   last run SUCCESS, or a SUCCESS within GREEN_RECENCY_H
                          └─► ELIMINATE             (flowing — a PR got through)
   ↓ last result red, no recent pass
GATE 2 · one bad PR?    f ≤ 1
                          └─► ELIMINATE             (a bad PR, not a branch break)
   ↓ f ≥ 2 distinct failing PRs
GATE 3 · sustained?     f ≥ T  AND  red span since the last pass ≥ D
                          └─► PERMAFAILING          (auto-confirmed — proven stuck ≥ D)
   ↓ not yet sustained
GATE 4 · branch throughput ≥ HI ?            HI = T ÷ report_interval   (derived, not hand-set)
     NO  (low-throughput) ───────────────────────────► CANDIDATE   (→ LLM; sparse signal is all we get)
     YES (high-throughput):
          failing share  f / (distinct PRs)  ≥ OVERWHELM_RATE ?
             YES ──► CANDIDATE   (→ LLM; overwhelming fresh failures — suspicious, not yet proven stuck)
             NO  ──► ELIMINATE   (sparse noise; transient; will self-confirm at Gate 3 if real)
```

## Why each gate

- **G0 freshness first.** No fresh result (last run older than `STALE_BD`, or none) → can't judge →
  `under-observed`, and Phase 5 recommends opening a fake PR. Catches "last green 3–4 weekdays ago."
- **G1 recent pass.** A SUCCESS as the last run or within `GREEN_RECENCY_H` → a PR got through → not
  blocked → eliminate. Sparse-but-recent green is fine.
- **G2 one bad PR.** All failures on one `head_sha` → that PR's problem → eliminate. Kills the classic
  false positive (a PR retried red many times) **structurally** — by counting distinct PRs, no math.
- **G3 sustained permafail.** Auto-confirm only the proven-stuck case: `f ≥ T` distinct failing PRs and
  the red run **since the last pass** has lasted `≥ D`. Anchoring the span to the *last pass* (not a
  fixed calendar window) is what lets a green-for-weeks-then-break still confirm at the D mark — an
  earlier "no pass in N business days" formulation would have delayed it.
- **G4 throughput, with an overwhelming-volume carve-out.** The remaining case is `≥2 failing PRs, no
  recent pass, not yet sustained`.
  - **low-throughput** → `candidate`: the branch won't produce enough PRs before the next run to self-
    confirm, so this sparse sample is the best signal we have → LLM.
  - **high-throughput, sparse** (few of many PRs failing) → `eliminate`: noise; a real break will reach
    the Gate-3 bar by the next daily run anyway.
  - **high-throughput, overwhelming** (a large *share* of recent PRs failing, e.g. a sudden wall of
    fresh-PR failures) → `candidate`, **not** permafailing: too many to dismiss, but on a firehose branch
    a burst of fresh PRs can fail from their first run or two, so it is *suspicious, not proven*. The LLM
    judges it now; if it's real it auto-confirms at Gate 3 once it crosses `D`.

## Terms & knobs

| name | meaning | value |
|---|---|---|
| **`f`** | observed distinct failing PRs for the job | measured, per job/run |
| **`T`** (`MIN_FAILING_PRS`) | distinct failing PRs to auto-confirm (Gate 3 bar) | 3 |
| **`HI`** | high-throughput cutoff — **derived** `= T ÷ report_interval` | daily → 3 PRs/bd |
| `STALE_BD` | last-run age → under-observed | 2 bd |
| `GREEN_RECENCY_H` | a SUCCESS within this → eliminate | 24 h |
| `D` | sustained-permafail red span (since last pass) | 48 h |
| `OVERWHELM_RATE` | high-throughput: failing share → candidate not eliminate | 0.5 |

`HI` is not a magic number: a branch is "high" exactly when a real break would reach the Gate-3 bar
(`T` failing PRs) before the next run — i.e. it produces ≥ `T` PRs per report interval. Change `T` or the
cadence and `HI` moves with them.

## Throughput measurement (every run, cheaply)

Throughput is a **per-branch** property, measured once per branch over a **fixed ~10-business-day
window**: distinct PRs ÷ window, via the branch's most active required job (a required e2e job runs on ~every
PR, so one job's PR count ≈ the branch's). A fixed window (not the observed min–max span) avoids a backport
burst reading as "high." Early-exit: stop paging once the count clears `HI`, so `main` resolves in a page
or two. Cost ≈ one extra fetch per branch, run daily — negligible.

## Backtest (offline prototype)

`python3 -I research/v2-elimination-funnel/permafail_prototype.py` (T=3, D=48h, STALE_BD=2,
GREEN_RECENCY_H=24, OVERWHELM_RATE=50%, HI=3/bd):

| scenario | vol | CURRENT | v2 | gate |
|---|---|---|---|---|
| no runs / last run 3 bdays ago | low | unknown/not_flagged | **under-observed** | G0 |
| green today (prior reds ~4d apart) | high | not_flagged | eliminated | G1 |
| red today, green ~12h ago | high | not_flagged | eliminated | G1 |
| 1 PR retried red 60× | high | **permafailing** | eliminated | G2 (f=1) |
| release-4.20: 2 PRs red today | low | not_flagged | **candidate** | G4 low |
| main: 3 reds among 20 passes | high | not_flagged | eliminated | G4 sparse (13%) |
| main: 20 fresh reds, span 30h | high | candidate | **candidate** | G4 overwhelming (91%) |
| main: 40 reds, span 52h | high | candidate | **permafailing** | G3 sustained |

The last two are the "main breaks after weeks green" scenario: **day 1** (20 fresh reds, < D) →
`candidate` (overwhelming, LLM looks); **day 2** (40 reds, ≥ D) → `permafailing` (proven stuck). The 40-red
case also shows v2 catching a sustained break that the current absolute gate misses (40 < 60).

## Live validation (real Prow data)

`python3 -I research/v2-elimination-funnel/live_main.py main release-4.22 release-4.21 release-4.20`
(2026-10-09; dev=5.1):

| branch | throughput (10 bd) | vol | v2 tally |
|---|---|---|---|
| main | ~firehose (≥ HI) | high | 9 eliminated (recent pass), 2 under-observed (override jobs) |
| release-4.22 | 0.90 PRs/bd | low | 6 eliminated, 4 under-observed |
| release-4.21 | 1.80 PRs/bd | low | **2 candidates** (`e2e-aks` f=3, `e2e-aws` f=2), 3 eliminated, 2 under-observed |
| release-4.20 | 1.70 PRs/bd | low | 5 eliminated (incl. `e2e-aks`: current **candidate** → v2 **eliminated**, one bad PR), 1 under-observed |

Confirms on real data: no over-fire on healthy `main` (overwhelming path dormant); derived `HI=3` puts all
release branches on `low` so their sparse reds surface as candidates; the one-bad-PR FP fix fires
(`4.20 e2e-aks`); and `under-observed` catches the `-override`/`-4-xx` jobs that never run.

## Periodics

Out of scope — periodics run on a schedule (no staleness, no candidate band): deterministic
`green`/`permafailing` (today's Design-B binary + SLO).

## Open / next

### Idea (parked 2026-10-10): attach a failure signature to *eliminated* jobs

Even when a job is eliminated, have the tool extract a short failure signature from its latest red run(s)
and surface it **next to the eliminated job's name**. Rationale: an eliminated job annotated with e.g.
`aws key expired` instantly tells the LLM (and a human) it's actually permafailing — the deterministic
gate dropped it, but the one-line evidence lets the LLM catch it anyway. This is the cheap backstop for
the reviewers' #1 objection — silent elimination violates "fail closed" (C1 flaky-but-blocking, C3
textbook break eliminated before the LLM looks, H5 no audit trail): eliminated jobs stop being invisible;
they carry a scannable cause. Open questions for later: extra fetch cost (opening logs for dropped jobs);
whether to annotate all eliminates or only the non-recent-pass ones; how terse the signature should be;
and reuse of `diagnostic-hints.md` infra signatures.

- Tune `T, D, STALE_BD, GREEN_RECENCY_H, OVERWHELM_RATE` and the 10-bd throughput window against labelled
  history; `HI` follows from `T` and the cadence.
- Consider a per-job healthy base-rate for `OVERWHELM_RATE` (empirical Bayes) rather than a flat 0.5.
- Decide the `under-observed → fake-PR` action-item wording for Phase 5.
- If accepted: wire into the companion behind a config flag, emit per-branch `{throughput, volume}` and the
  per-job outcome in `data.json`, add tests, and rename emitted `not_flagged`/`unknown` → `eliminated`/`under-observed`.
