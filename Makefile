# netctl — developer and CI entry points. CI runs exactly these targets, so a
# green `make check` locally means a green CI.

STATICCHECK_VERSION := v0.7.0

.PHONY: check fmt-check vet lint test race arch fuzz e2e-phase1 repro windows

## check: the everyday gate — formatting, vet, tests. A red result fails CI.
check: fmt-check vet test

## fmt-check: fail if any file is not gofmt-clean (prints the offenders).
fmt-check:
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

## lint: staticcheck over the whole module.
lint:
	@command -v staticcheck >/dev/null 2>&1 || { \
	  echo "staticcheck not found; install: go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)"; exit 1; }
	staticcheck ./...

test:
	go test -count=1 ./...

## race: tests under the race detector.
race:
	go test -race -count=1 ./...

## arch: architecture guard (P1-ARCH-001/002). Real checks arrive in V5; until
## then this only proves the module still builds for Windows.
arch: windows
	@echo "arch: dependency and package-main guards arrive in V5"

## windows: the library must build on an OS it never targets (P1-ARCH-002).
windows:
	GOOS=windows go build ./...

## fuzz: run each fuzz target briefly. No fuzz targets exist yet (first in V13).
fuzz:
	@echo "fuzz: no fuzz targets yet"

## e2e-phase1: the Phase 1 end-to-end suite (empty until Era 12).
e2e-phase1:
	go test -count=1 ./test/e2e/phase1/...

## repro: repeatability smoke test — the suite passes twice in shuffled order.
## The byte-for-byte determinism proof is V61.
repro:
	go test -count=2 -shuffle=on ./...
