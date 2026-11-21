# Phase 1 gate: evidence so far

**Decision: No-go. The gate is not passed.** This file records what the evidence shows today and
what is missing. It is not a sign-off.

Evidence collected on a laptop (4 cores) with Go 1.26.5; commit in `commit.txt` is the commit the
evidence was gathered from, not a release.

## Checklist

| Item | State | Evidence |
|---|---|---|
| Every build checklist in Eras 1-12 complete | **Open** | V98 is this file; the open rows below |
| ADRs 0001-0005 accepted | Done | `docs/adr` |
| JSON Schema `v1alpha1` published | Done | `config/schema/v1alpha1.json` |
| All `P1-*` tests pass with `-race -count=1` | Passing without `-race` today; the race run of the whole module is not recorded | `test-report.json` is the E2E suite under `-short` |
| Library-level `P1-E2E-*` pass | Done for 001-005, 020-029, 040-048, 060-064, 067, 090-092, 100-102, 120-124 | `test-report.json` (fast set); scale and chaos runs below |
| `beaver-cli` suite (`P1-CLI-*`, `P1-E2E-004/065/066`) green | **Not done** | beaver-cli work (V77-V84), not started |
| Chaos nightly green three nights in a row | **Not done** | 40 cases run once, no violations; the nightly workflow does not exist |
| Fuzz at least 30 minutes per target | **Not done** | each target ran seconds only |
| Coverage 85% overall, 90% ipam/route/sim/validate | Done | `coverage.txt`: overall 96.8%, ipam 99.1%, route 99.5%, sim 98.4%, validate 94.6% |
| Benchmarks within budget | Done on a laptop | `bench.txt` (all-pairs 500 nodes 1.4 s against 10 s). CI-runner numbers not recorded |
| AC1-AC9 ticked against evidence | See below | |
| Consumer-module test (V75) green; `docs/api-surface.txt` committed | Done | `make consumer`, `docs/api-surface.txt` |
| `beaver network plan` runs the three-site example | **Not done** | beaver-cli |
| Labs A1-A3 transcribed into golden fixtures; A4 in progress | **Not done** | owner step: `testdata/golden/lab-aN/observed` is empty; the golden lab tests compare only the reviewed output until then |
| Phase 2 prerequisites by hand (veth pair, WireGuard tunnel) | **Not done** | owner step |
| Actual time per era recorded | **Not done** | |

## Acceptance criteria

| AC | Evidence | Result |
|---|---|---|
| AC1 no overlaps | E2E 060, 061, 121 | Passing |
| AC2 no unaccounted space | E2E 005, 040, 042, 121 | Passing |
| AC3 correct route tables | E2E 001, 004, 020; oracle; labs A1-A3 against Packet Tracer **not yet** | Open until the labs are transcribed |
| AC4 no undetected loops | E2E 029, 120 | Passing (029 differs from the guide, see below) |
| AC5 no undetected missing return paths | E2E 020, 120 | Passing |
| AC6 no one-sided links | E2E 063 | Passing |
| AC7 correct failover | E2E 020-028, 124 | Passing |
| AC8 reproducible | E2E 002, 022, 042, 090-092; 093 clean checkout **not done** | Open until 093 |
| AC9 independent of the applying machine | E2E 001 (key scan), 091 on Linux and macOS in CI | Passing; Windows run not done |

## Where this implementation differs from the E2E guide

1. **Isolation is refused, not warned (E2E 027, 028).** When a failover leaves a spoke with no healthy chain to a
   hub, validation fails with V-TOPO-002 and the guard keeps the plan in force. The guide expects a plan with a
   V-SIM-004 warning and `NO_HEALTHY_PATH` verdicts. There is no V-SIM-004 rule. This needs a decision: either
   the guide is wrong or the validator is too strict for a network that is genuinely partitioned.
2. **The looping-override trap (E2E 029)** cannot be built as the guide describes; with the direct link healthy the
   planner already finds the route asymmetric, and a static route over a down link is dropped. The test uses a hub
   override and the guard's FailoverRejected, and the plan is produced from the routes in force instead of being refused
   (the guide expects exit 2).
3. The hub's config differs across plans in `plan_id` and `hash` only.
4. Decision reasons use this library's words ("consecutive failures reached the threshold").
5. Pins (`pin: {overlay}`) and `transit: true` are not in the topology format. The overlap fixtures are built as
   saved state; hub links get no transit /30 (only probed direct links do). A per-site `override` was added for E2E 048.
6. The fresh-plan typo is on line 17, not 15, in `testdata/e2e/three-site-typo.yaml`.
7. Timing limits of E2E 100-102 are enforced when `E2E_STRICT_TIMING` is set; on a loaded laptop they are logged.
   A 100-node fresh plan took 4.4 s with the tests running in parallel, against the guide's 2 s.
8. The chaos run takes about 3 s a case on four cores, so 2,000 cases per push and 20,000 nightly are set by
   `E2E_CHAOS_CASES`; the default is 300.

## Missing evidence files

`ci-run.txt`, `chaos-nightly/` (three nights), `e2e-artifacts/` and `e2e-summary.md` are not here.

## Tag

`v0.11.0` is not cut and must not be until the open rows above are closed.
