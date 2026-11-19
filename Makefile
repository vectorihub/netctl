# netctl — developer and CI entry points. CI runs exactly these targets, so a
# green `make check` locally means a green CI.

STATICCHECK_VERSION := v0.7.0

.PHONY: check fmt-check vet lint purity test race arch fuzz e2e-phase1 repro windows

## check: the everyday gate — formatting, vet, tests. A red result fails CI.
check: fmt-check vet test

## fmt-check: fail if any file is not gofmt-clean (prints the offenders).
fmt-check:
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

## purity: no clock, file or environment reads in library code (D7). Test files
## are exempt. Any match fails the build.
PURITY_PATTERN := time\.(Now|Since|Until)\(|os\.(Open|OpenFile|Create|ReadFile|WriteFile|ReadDir|Stat|Getenv|LookupEnv|Environ)\b
purity:
	@out="$$(grep -rnE --include='*.go' --exclude='*_test.go' --exclude-dir=.git --exclude-dir=features '$(PURITY_PATTERN)' . || true)"; \
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
FUZZ_TARGETS := cidr:FuzzCanonical cidr:FuzzChildren
fuzz:
	@for target in $(FUZZ_TARGETS); do \
	  pkg=$${target%%:*}; name=$${target##*:}; \
	  echo "fuzz: $$pkg $$name for $(FUZZTIME)"; \
	  go test ./$$pkg -run='^$$' -fuzz="^$$name$$" -fuzztime=$(FUZZTIME) || exit 1; \
	done

## e2e-phase1: the Phase 1 end-to-end suite (empty until Era 12).
e2e-phase1:
	go test -count=1 ./test/e2e/phase1/...

## repro: repeatability smoke test — the suite passes twice in shuffled order.
## The byte-for-byte determinism proof is V61.
repro:
	go test -count=2 -shuffle=on ./...
