# Contributing to netctl

Thank you for your interest in contributing. This guide explains how to report problems, propose changes, and get a
pull request merged.

By participating, you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## Ways to contribute

- **Report a bug.** Search the [existing issues](https://github.com/vectorihub/netctl/issues) first. If none match,
  open a new one with the `netctl` version, Go version, operating system, the smallest input that reproduces the
  problem, and the output you expected.
- **Request a feature.** Open an issue describing the problem you are trying to solve, not only the solution you have
  in mind.
- **Improve the documentation.** Fixes to comments, examples, and the README are always welcome.
- **Send code.** Small fixes can go straight to a pull request. For anything larger, open an issue first so the
  approach can be agreed before you spend time on it.

Looking for somewhere to start? Try an issue labelled
[`good first issue`](https://github.com/vectorihub/netctl/labels/good%20first%20issue).

Security vulnerabilities must **not** be reported in public issues. See the [security policy](SECURITY.md).

## Development setup

You need:

- Go 1.25 or newer
- `make`
- [`staticcheck`](https://staticcheck.dev): `go install honnef.co/go/tools/cmd/staticcheck@v0.7.0`

Then fork the repository, clone your fork, and confirm everything passes:

```sh
$ git clone https://github.com/<your-username>/netctl.git
$ cd netctl
$ make check lint race arch
```

| Target | What it runs |
|---|---|
| `make check` | Formatting, `go vet`, and the test suite. |
| `make lint` | Project lint rules and `staticcheck`. |
| `make race` | The test suite under the race detector. |
| `make arch` | Architecture checks and a cross-platform build. |
| `make bench` | Benchmarks. |
| `make fuzz` | Fuzz targets. Set `FUZZTIME=120s` for a longer run. |

CI runs the same targets on Linux and macOS, so a green local run means a green build.

## Coding guidelines

- **Keep the library pure.** Library code must not read files, the clock, environment variables, or the network.
  Pass these in as values instead. `make lint` and `make arch` enforce this.
- **Keep output deterministic.** The same input must produce the same output. Sort before iterating over a map.
- **Use `net/netip`** for addresses and prefixes, never `net.IP`.
- **Return typed errors** with a stable code, so callers can match them with `errors.Is` instead of message text.
- **Document every function,** including unexported helpers and tests. Exported identifiers start their comment with
  their name and describe inputs, outputs, and errors. Add a runnable `Example` test for new exported API.
- **Avoid new dependencies.** A new runtime dependency needs a strong reason, discussed in an issue first.
- Format with `gofmt`. `make check` fails on unformatted code.

## Tests

- Every change in behaviour needs a test.
- Tests exercise the exported API from the package's `tests/` directory, for example `ipam/tests/`.
- A test that must reach unexported state may sit beside the code, with a comment at the top of the file saying why.
- Runnable examples are the one exception to the `tests/` rule. They live in an `example_test.go` file beside the
  code, declared as `package <name>_test`, because pkg.go.dev only shows examples from the package's own directory.
  Every example ends with an `// Output:` comment, so `go test` checks it.

## Commit messages

Commits follow [Conventional Commits](https://www.conventionalcommits.org):

```text
<type>(<optional scope>): <summary in the imperative mood>
```

Common types are `feat`, `fix`, `docs`, `test`, `refactor`, `ci`, and `chore`. The scope is usually the package name,
for example `fix(ipam): reject a pool outside the supernet`. Keep the summary under 72 characters and explain the
reason for the change in the body.

## Pull requests

1. Create a branch from `main` in your fork.
2. Make your change, with tests and documentation.
3. Run `make check lint race arch`.
4. Open a pull request against `main` that explains what changed and why, and links any related issue.

Every pull request needs passing CI checks and a maintainer's review before it is merged. Breaking changes to the
public API must be called out in the pull request description so they can be recorded in the
[changelog](CHANGELOG.md).

## License

By contributing, you agree that your contributions are licensed under the [Apache License 2.0](LICENSE), the same
license that covers the project.
