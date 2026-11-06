# Research: statistically-normalized permafail classification

**Status:** research / proposal (v1) — **superseded by `research/v2-elimination-funnel/`** (reframed as an elimination funnel with business-day recency + throughput). Kept for history. Not wired into the companion. Prototype + backtest in
`research/v1-wilson-permafail/permafail_prototype.py`; results folded in below.

## The problem

Today the merge-queue (Axis A) gate is an **absolute streak count** (`classify_presubmit_streak`):

```
r = consecutive reds since the last SUCCESS (ERROR/ABORTED neutral)
r <= 3                       -> not_flagged
r >= 60 AND span >= 48h      -> permafailing (merge-queue blocker)
otherwise                    -> candidate (LLM resolves)
```

A fixed count of `60` is not a sound detector of "this branch is broken," because it:

1. **Conflates volume with severity.** `60` is tuned for busy `main`; the 48h span cap makes
   `60`-in-`48h` **unreachable on low-volume release branches**, so a release-branch presubmit that is
   100% red for a week can never auto-flag — it maxes out as a *candidate*.
2. **Ignores exposure (sample size).** "60 reds" means something very different out of 60 runs
   (100% fail) vs 600 runs (10% fail). The count alone carries no confidence.
3. **Ignores independence (unique PRs).** 60 reds all on **one** `head_sha` is a *bad PR*, not a broken
   branch — yet the streak counts them as 60. The real branch-break signal is failures across
   **independent** PR revisions. (`distinct_heads` is already computed but unused in the gate.)

The periodic gate (`failures-since-last-pass >= 4 AND span >= 48h`) has the same shape — an absolute
count — just a lower bar. (Periodics have no PRs/heads, so only factors 1-2 apply to them.)

## What we're actually testing

"Is this presubmit **permafailing**?" = "is the branch's own health bad — bad enough that *independent,
plausibly-good PRs* fail — and has it persisted?" That is a hypothesis test on a Bernoulli process whose
trials are **PR revisions**, not raw runs. The three knobs you'd expect map to standard statistics:

| factor | statistic | what it buys |
|---|---|---|
| **sample size** `n` | binomial proportion CI (Wilson lower bound) / Beta-Binomial posterior | precision: small `n` → can't be confident → don't auto-flag |
| **unique PRs** `k` | per-head aggregation (Wilson on fail-only / tested heads) | branch-wide vs one bad PR; heads are the *effective* sample size |
| **period** `T` | window + persistence (`span >= D`) + recency | sustained break vs transient infra blip; discounts stale runs |

### 1. Sample size → a confident rate, not a count

Treat each trial as fail/pass and bound the **true** failure rate from below. Wilson score lower bound
(closed-form, stdlib):

```
p̂ = r/n
L = ( p̂ + z²/2n − z·sqrt( p̂(1−p̂)/n + z²/4n² ) ) / ( 1 + z²/n )
```

`L` rises only as `n` grows, so "100% red" clears a high bar only with enough evidence
(`3/3 → L≈0.36`, `20/20 → L≈0.79` at z=2.33). This is the auto-scaling you want.

### 2. Unique PRs → aggregate per head (the key move)

Count **distinct PR revisions**, not runs. For each `head_sha` seen in the window, label it a **failed
head** (≥1 FAILURE, 0 SUCCESS — a PR that never passed on this branch) or a **passed head** (≥1 SUCCESS;
mixed counts as passed — the branch let it through). Then:

- `n_eff` = distinct heads tested, `r_eff` = failed heads, `k = r_eff`.
- Run the rate test on `(r_eff, n_eff)` — the unit is PRs, so one bad PR spamming 60 reds contributes
  **1**, not 60.
- A tempting shortcut — "`k` independent heads all failing has null probability `≈ q^k`, flag when
  `q^k ≤ α`" — is **unsafe**: `q^k` counts only the fail-only heads and ignores the denominator, so it
  would flag `5 fail / 50 pass` (a healthy branch). The Wilson bound on `(r_eff, n_eff)` already encodes
  the independence evidence **and** the passes, so use it — don't add a separate `q^k` OR-path.

### 3. Period → persistence + recency

The window defines *over what* `n` is counted (use the SLO ~1-2w window, **not** a 48h cap). Require the
failures to span `>= D` (sustained, not a blip) and at least one recent failure (still broken).

## Proposed rule (deterministic — fits "companion owns the facts")

Over window `W`, on **per-head** counts, with persistence span of failing runs:

```
n_eff == 0                                                        -> unknown
span < D                                                          -> candidate (not yet sustained)
span >= D AND still_red AND k >= K_MIN AND L >= P*               -> permafailing
r_eff >= 1                                                        -> candidate   (LLM resolves)
otherwise                                                         -> not_flagged
# still_red = no SUCCESS in the last recovery_h hours (currency guard — see "Recency" below)
```

The test is closed-form and stdlib-only, so the companion stays deterministic and the LLM still owns
causation for the `candidate` band.

### Constants (defaults to **backtest**, not gospel)

| const | meaning | start |
|---|---|---|
| `W` | observation window | 14d |
| `z` | Wilson confidence | 2.33 (~99%) |
| `P*` | failure-rate lower-bound floor | 0.75 |
| `K_MIN` | min distinct failed heads | 3 |
| `D` | persistence span | 48h |
| `recovery_h` | no-pass recency window (currency guard) | 24h |

## Caveats (where naive statistics bite)

- **Non-independence / retests.** Runs on one head aren't independent; clustering by `head_sha` (above)
  neutralizes retries and a single infra-correlated batch.
- **Not all PRs are good.** A genuinely bad PR legitimately fails — `k ≥ K_MIN` distinct heads guards
  against one or two, but a cluster of bad PRs can still mislead; that's exactly why the `candidate` band
  hands causation to the LLM.
- **Per-job base rate.** The healthy flake rate varies by job, so one global `P*` is crude; ideally use a
  per-job prior (empirical-Bayes Beta on each job's healthy history) or a per-job `P*`.
- **Multiple testing.** ~45 jobs assessed per run; a one-sided `z=2.33` (~1%) is ~0.5 false flags/run by
  chance. Raise `z` or apply a Benjamini-Hochberg-style correction across the job set, or the blocker list
  slowly accrues noise.

## Recency / currency

The Wilson rate is window-wide and *position-blind* — a green doesn't reset it (unlike the current
streak), but on its own it can't tell "stuck red now" from "red for two weeks, green since yesterday." So
the rule gates it with a **recency guard**: `still_red` = **no SUCCESS in the last `recovery_h`** (default
24h). Persistence stays measured earliest-fail→now (a flaky green mid-window does not reset the
long-standing clock); recovery is handled by `still_red` flipping, **not** by shrinking the span. The
alternative **strict** guard — "most-recent run must be FAILURE" — is simpler but one flaky green at the
tip flips the verdict, so the `recovery_h` window is the robust choice.

Prototype, variants of the 28-PR break (`recovery_h = 24h`):

| variant | CURRENT | PROPOSED (with guard) |
|---|---|---|
| 28 red, newest red @12h | candidate (28) | **permafailing** |
| + green **now** (≤ 24h) | not_flagged | candidate — recovering |
| + green **@30h**, red since | not_flagged | **permafailing** — still red |
| + green **@12h** (≤ 24h) | not_flagged | candidate — recovering |
| + recovered (last 3 green) | not_flagged | candidate |

A pass within `recovery_h` downgrades to candidate (hand to Phase-2 to confirm the recovery); an older
pass with reds since stays permafailing — i.e. "green 2 runs ago but red now" still counts.

## Backtest (prototype)

`research/v1-wilson-permafail/permafail_prototype.py` runs the **real** `classify_presubmit_streak` against the proposed gate
(`P*=0.75, z=2.33, K_min=3, D=48h, recovery_h=24h, W=14d`) on synthetic streams that each isolate a factor:

| scenario | CURRENT (streak) | PROPOSED | n_eff | r_eff | Wilson L | still_red |
|---|---|---|---|---|---|---|
| release break, 4 PRs | candidate (8) | candidate | 4 | 4 | 0.42 | True |
| release break, 28 PRs / 14d | candidate (28) | **permafailing** | 28 | 28 | 0.84 | True |
| one bad PR, 60 reds | **permafailing** (60) | candidate | 31 | 1 | 0.00 | True |
| main break, 30 PRs / 50h | candidate (30) | **permafailing** | 30 | 30 | 0.85 | True |
| flaky, 2 fail / 16 green | not_flagged (0) | candidate | 18 | 2 | 0.03 | False |
| small all-fail, 3 PRs / 10h | not_flagged (3) | candidate | 3 | 3 | 0.36 | True |

**What the per-head gate fixes vs the absolute count:**
- **False positive — one bad PR.** 60 consecutive reds on a *single* `head_sha` auto-flags as a
  merge-queue blocker today; the per-head gate sees `r_eff = 1` failing PR out of 31 → candidate. (A PR
  stuck retrying in the queue is a real-world instance.)
- **False negatives — sub-60 breaks.** A sustained release-branch break (28 PRs) and a 30-PR main break
  are both only *candidates* today (28, 30 < 60); the per-head gate flags both permafailing — and the
  release branch can reach the bar **at all**, which it structurally cannot today.
- **Honest deferral.** 4 failing PRs stays a *candidate*: Wilson `L = 0.42` says there isn't yet enough
  evidence, so it routes to the LLM rather than auto-flagging. Low-evidence cases (flaky, tiny samples)
  surface as candidates instead of silent `not_flagged`.

**Constants are illustrative, not tuned.** `P*, z, K_min, D, recovery_h` must be fit against *real*
history — a labelled set of known-broken vs healthy branch-periods — ideally with a per-job prior (see
Caveats). The prototype proves the **shape** is right, not the numbers. Run:
`python3 -I research/v1-wilson-permafail/permafail_prototype.py`.
