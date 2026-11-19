# Security Policy

## Supported versions

`netctl` is pre-1.0. Security fixes are released for the latest minor version only. Please upgrade to the latest
release before reporting an issue.

| Version | Supported |
|---|---|
| Latest `v0.x` release | Yes |
| Older releases | No |

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues, pull requests, or discussions.**

Report them privately through
[GitHub private vulnerability reporting](https://github.com/vectorihub/netctl/security/advisories/new). Only the
maintainers can see the report.

Please include as much of the following as you can:

- The affected version, or the commit you tested against.
- The affected package and function.
- A description of the issue and its impact.
- Steps or a minimal input that reproduces it.
- Any suggested fix or mitigation.

## What to expect

- We aim to acknowledge your report within **3 business days**.
- We will confirm whether the issue is accepted, and keep you informed while a fix is prepared.
- Once a fix is released, we will publish a GitHub security advisory and credit you, unless you prefer to remain
  anonymous.

Please give us reasonable time to release a fix before disclosing the issue publicly.

## Scope

`netctl` is a library that computes plans from the input it is given. It does not open network connections, read
files, or execute programs. Reports are most useful when they show that valid or malicious input can cause incorrect
results, such as overlapping address allocations, a crash or panic, or excessive CPU or memory use.

Vulnerabilities in programs that use `netctl`, or in its dependencies, should be reported to those projects.
