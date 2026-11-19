# Versioning and release policy

How netctl is numbered, what may change between releases, and how to cut a tag. Decisions D5 and D6
in the architecture plan are the source; this page is the working procedure.

## 1. Module versions: `v0.x` until the Phase 5 gate (D6)

The module stays at `v0.N.0` until the Phase 5 gate is signed. Then it becomes `v1.0.0`.

| Rule | Meaning |
|---|---|
| Minor bump (`v0.N.0` to `v0.N+1.0`) | A new era shipped. The public surface may have changed in a breaking way. |
| Patch bump (`v0.N.P`) | Bug fixes only. No public name is added, removed or changed. |
| Breaking changes | Allowed in a minor bump while on `v0.x`. Each one is listed under **Changed** or **Removed** in the changelog. |
| `v1.0.0` | After the Phase 5 gate. From then on, normal Go semantic versioning: no breaking change without a new major path (`/v2`). |

Tags are cut from `main` only, and only after `make check lint race arch` is green. Tags are never moved or
deleted once pushed. A bad release is fixed with the next patch tag.

Each era that changes the public surface ends in a tag. The release list is in the architecture plan
under "Releases" (R1 `v0.1.0` through R11 `v0.11.0`). Releases that belong to other modules, such as
the agent or `beaver-cli`, do not tag netctl unless its API changed.

## 2. Schema strings version separately (D5)

The files netctl reads and writes carry their own version string. These move independently of the
module version:

| Schema string | What it describes |
|---|---|
| `beaver.netctl/v1alpha1` | `NodeConfig`, the per-machine configuration (the contract with agents) |
| `beaver.topology/v1alpha1` | The topology input file |
| `beaver.ipam/v1` | The IPAM snapshot |

Rules:

- A schema string changes **only** when a breaking change is made to that format. Adding an optional
  field is not breaking and keeps the string.
- A module release can ship with no schema change, and a schema change forces at least a minor module bump.
- **Readers refuse what they do not know.** Agents reject an unknown `NodeConfig` schema rather than
  guessing. Loaders reject unknown topology and snapshot schemas.
- When a schema string changes, the old reader behaviour and a migration note go in the changelog.

## 3. How a consumer upgrades

A program that imports netctl (the backend, the agent, `beaver-cli`) bumps it like this:

```
go get github.com/VectorIHub/netctl@v0.N.0
go mod tidy
go build ./... && go test ./...
```

- Pin an exact tag. Do not track `main`.
- **Never commit a `replace` directive** for netctl in a shipping commit. CI in the consumer must build
  against a real tag.
- Read the changelog entries between the old and new tag before upgrading. Look for **Changed**,
  **Removed** and any schema string change.

### Working on netctl and a consumer together

For local, multi-repo work, use a Go workspace instead of `replace`:

```
go work init ../beaver-netctl ../beaver-cli
```

`go.work` and `go.work.sum` are ignored by git (see `.gitignore`), so they cannot be committed by
accident. Delete the workspace file, or run with `GOWORK=off`, to confirm the consumer builds against the
real tag before merging.

## 4. Changelog format

`CHANGELOG.md` at the repository root follows [Keep a Changelog](https://keepachangelog.com), newest
release first, with an `Unreleased` section on top:

```
## [Unreleased]

## [0.2.0] - 2026-12-01
### Added
- ipam: Allocate, Reserve, Release, Snapshot.
### Changed
- types: LinkIDOf now rejects equal endpoints.
### Removed
### Fixed
### Schema
- beaver.ipam/v1: unchanged.
```

One bullet per user-visible change, written for the person upgrading, with the package name first. The
**Schema** section is always present and says "unchanged" when nothing moved. The changelog is created
with the first tag (`v0.1.0`).

## 5. The public surface

Everything outside `internal/` is public (D3). That means these packages and the root facade:

```
netctl (root facade)   types   cidr   ipam   topology   route   sim   health
failover   config   validate   plan   policy   identity
```

Test-support code lives under `internal/` and may change at any time. Anything exported from the
packages above is part of the surface: functions, types, constants, struct fields, and the **text of
coded errors** (`OVERLAP`, and so on), because callers match on them.

### The `apidiff` check

From V74, when the public API candidate exists, CI compares the exported API of a change against the
last tag with [`apidiff`](https://pkg.go.dev/golang.org/x/exp/cmd/apidiff):

1. Export the API of the last tag (`apidiff -w prev.api <package path at tag>`).
2. Compare the working tree against it (`apidiff -incompatible prev.api <package path>`).
3. Any incompatible change fails the check unless the change is on `v0.x`, is listed under
   **Changed** or **Removed** in the changelog, and is called out in the pull request.

Until V74 there is no public API to compare, so the check is not yet wired into CI.

## 6. Cutting a release

1. Confirm `main` is green on both operating systems.
2. Move the `Unreleased` changelog entries under the new version and date.
3. Check the schema section against the schema strings above.
4. Tag: `git tag -a v0.N.0 -m "netctl v0.N.0"` and push the tag.
5. Open the consumers' upgrade pull requests (section 3).
