# Lab A1 observations — owner step

Transcribe the Packet Tracer results of lab A1 here, one YAML file per device
(`show ip route`, traceroutes), plus an optional `mapping.yaml` for host names and for
hub-link addresses the plan allocates no transit /30 for. The format and the match rules are
in `internal/ptimport`. Lab A3 files carry a `state:` of `baseline`, `shutdown` or `restored`.

Until a `.yaml` file is here, the golden test for this lab checks the reviewed golden output
only and logs that the observation comparison was skipped. The Phase 1 gate (V98) needs these
files.
