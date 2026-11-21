# Lab A4 observations — owner step

Lab A4 runs OSPF. netctl computes paths statically, so only the final converged paths are
compared. Transcribe the traceroutes of each state (`all-up`, `one-failed`, `two-failed`) as
`state:` entries in per-device YAML files, in the format of `internal/ptimport`. The fixture
`testdata/e2e/lab-a4.yaml` is provisional (three spokes in a triangle of direct links plus the
hub) until the real lab is transcribed; change it to match the lab you built.
