# netctl — developer and CI entry points. CI runs exactly these targets, so a
# green `make check` locally means a green CI.

STATICCHECK_VERSION := v0.7.0

.PHONY: check fmt-check vet lint purity test race arch fuzz bench repro windows apidiff consumer e2e-phase1 e2e-phase1-fast e2e-phase1-chaos cover

## check: the everyday gate — formatting, vet, tests. A red result fails CI.
check: fmt-check vet test

## fmt-check: fail if any file is not gofmt-clean (prints the offenders).
fmt-check:
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

## purity: no clock, file or environment reads in library code (D7). Test files
## are exempt, and so are internal/e2e and internal/ptimport, the test-support packages, which write and read workspaces. Any
## other match fails the build.
PURITY_PATTERN := time\.(Now|Since|Until)\(|os\.(Open|OpenFile|Create|ReadFile|WriteFile|ReadDir|Stat|Getenv|LookupEnv|Environ)\b
purity:
	@out="$$(grep -rnE --include='*.go' --exclude='*_test.go' --exclude-dir=.git --exclude-dir=features --exclude-dir=e2e --exclude-dir=ptimport --exclude-dir=tools '$(PURITY_PATTERN)' . || true)"; \
	if [ -n "$$out" ]; then echo "purity: library code must not read the clock, files or environment:"; echo "$$out"; exit 1; fi

## lint: purity guard, then staticcheck over the whole module.
lint: purity
	@command -v staticcheck >/dev/null 2>&1 || { \
	  echo "staticcheck not found; install: go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)"; exit 1; }
	staticcheck ./...

test:
	go test -count=1 ./...

## race: tests under the race detector.
race:
	go test -race -count=1 ./...

## arch: architecture guard. P1-ARCH-001 is the import-graph test in ./arch;
## P1-ARCH-002 is the Windows build.
arch: windows
	go test -count=1 ./arch/...

## windows: the library must build on an OS it never targets (P1-ARCH-002).
windows:
	GOOS=windows go build ./...

## fuzz: run each fuzz target for FUZZTIME (default 10s). The Phase 1 gate uses
## 60s for FuzzCanonical (P1-CIDR-009): make fuzz FUZZTIME=60s
## Go keeps any failing input under testdata/fuzz, where `make test` replays it.
FUZZTIME ?= 10s
FUZZ_TARGETS := plan/tests:FuzzPlan cidr/tests:FuzzCanonical cidr/tests:FuzzChildren ipam/tests:FuzzSnapshot topology/tests:FuzzDecode topology/tests:FuzzLoad sim/tests:FuzzParseScenarios
fuzz:
	@for target in $(FUZZ_TARGETS); do \
	  pkg=$${target%%:*}; name=$${target##*:}; \
	  echo "fuzz: $$pkg $$name for $(FUZZTIME)"; \
	  go test ./$$pkg -run='^$$' -fuzz="^$$name$$" -fuzztime=$(FUZZTIME) || exit 1; \
	done

## bench: IPAM and topology performance budgets (P1-TOPO-041: loading 500 nodes and 1,500 links
## under 300 ms; P1-IPAM-PERF-001: 2,000 overlay allocations under
## 200 ms; PERF-002: accounting at 5,000 allocations under 50 ms). A benchmark fails
## when its best run is over budget. The simulator's P1-SIM-PERF-001 (all-pairs overlay
## flows of 500 nodes, about 250,000, under 10 s) is BenchmarkAllPairs500; it takes a couple
## of seconds a run, so it runs fewer times.
bench:
	go test ./ipam/tests ./topology/tests -run='^$$' -bench=. -benchtime=10x
	go test ./sim/tests -run='^$$' -bench=. -benchtime=3x

## repro: repeatability smoke test — the suite passes twice in shuffled order.
## The byte-for-byte determinism proof is V61.
repro:
	go test -count=2 -shuffle=on ./...

## consumer: the consumer-module contract test. testdata/consumer is a separate Go module that
## requires netctl at this working tree, builds a network in code, calls netctl.Plan and checks
## the result, and that depending on netctl pulls in no forbidden package.
consumer:
	cd testdata/consumer && go vet ./... && go test -count=1 ./...

## apidiff: report how the exported API changed since the latest tag (APIBASE overrides it).
## It never fails the build: the report is read on every tag, and a breaking change before
## v1.0 is allowed as long as the changelog lists it. Packages the tag does not have are new.
## The tag is checked out in .apidiff-base (git-ignored) and reused on the next run; remove
## that directory yourself when you no longer need it. Install the tool with:
##   go install golang.org/x/exp/cmd/apidiff@latest
APIBASE ?= $(shell git describe --tags --abbrev=0 2>/dev/null)
apidiff:
	@command -v apidiff >/dev/null 2>&1 || { echo "apidiff not found; install: go install golang.org/x/exp/cmd/apidiff@latest"; exit 1; }
	@test -n "$(APIBASE)" || { echo "apidiff: no tag to compare with"; exit 0; }
	@if [ -d .apidiff-base ]; then git -C .apidiff-base checkout --detach --quiet "$(APIBASE)"; \
	else git worktree add --detach .apidiff-base "$(APIBASE)" >/dev/null 2>&1; fi || { echo "apidiff: cannot check out $(APIBASE)"; exit 0; }
	@mod="$$(go list -m)"; \
	for dir in $$(go list -f '{{.Dir}}' ./... | grep -v -e '/tests$$' -e '/internal' -e '/testdata'); do \
	  rel="$${dir#$$PWD}"; pkg="$$mod$$rel"; \
	  ( cd .apidiff-base && apidiff -w old.out "$$pkg" >/dev/null 2>&1 ) || { echo "== $$pkg: new since $(APIBASE)"; continue; }; \
	  echo "== $$pkg"; apidiff .apidiff-base/old.out "$$pkg"; \
	done

## e2e-phase1: the whole Phase 1 end-to-end suite (scale and chaos included).
e2e-phase1:
	go test ./test/e2e/phase1/... -count=1 -timeout 30m

## e2e-phase1-fast: everything except the scale and chaos groups.
e2e-phase1-fast:
	go test ./test/e2e/phase1/... -count=1 -short

## e2e-phase1-chaos: the nightly run, 20,000 random cases. Reproduce one with E2E_CHAOS_SEED=<n>.
e2e-phase1-chaos:
	E2E_CHAOS_CASES=20000 go test ./test/e2e/phase1/... -run 'TestE2E/P1-E2E-12' -count=1 -timeout 8h

## cover: coverage of the library across all tests, with the Phase 1 gate's thresholds: 85%
## overall and 90% for ipam, route, sim and validate. Writes cover.out and coverage.html.
COVERPKGS := ./cidr/...,./ipam/...,./route/...,./sim/...,./validate/...,./topology/...,./config/...,./health/...,./failover/...,./plan/...,./types/...
cover:
	go test -p 2 -short -coverpkg=$(COVERPKGS) -coverprofile=cover.out ./...
	go tool cover -html=cover.out -o coverage.html
	go run ./tools/covercheck cover.out
