# Quickstart: planning a network with the library

netctl is a library. It reads no file and runs no program: you decode a topology, call
`netctl.Plan`, and write the bytes `Result.Render` gives you. The command line is the
`beaver network` group of `beaver-cli`.

## Plan the three-site network

```go
topo, _, err := topology.Load("three-site.yaml", yamlBytes, 0) // you read the file
if err != nil { return err }
// A topology may name its address plan with $ref; netctl does not read it, so you decode
// the plan file and set topo.Addressing yourself, or write the plan inline.

result, err := netctl.Plan(netctl.Inputs{
    Topology: topo,
    Clock:    &types.FixedClock{T: now},   // the only clock netctl ever reads
})
if err != nil { return err }               // a refused plan has no configs: write nothing

files, err := result.Render()              // map of path -> bytes: plan.json, ipam.json,
if err != nil { return err }               // nodes/<node>.json, routes.txt, ...
for path, data := range files { /* write under your output directory */ }
```

The same inputs give the same bytes, whatever the order of the file.

## Plan again, with state

Keep three things between runs and hand them back: `result.IPAM` (the address plan, so addresses
never move), `result.Failover.State` (the health of every link and the routes in force), and
the result itself as `Previous`.

```go
next, err := netctl.Plan(netctl.Inputs{
    Topology: topo,
    IPAM:     last.IPAM,
    Previous: &last,
    Failover: &state,                      // from last.Failover.State
    Signals:  signals,                     // health.Signal values observed since
    Clock:    &types.FixedClock{T: now},
})
```

A direct link that must be proven (`health: required`) carries no traffic until five good
probes from both ends say so. Three failed probes take it Down, a hold-down keeps it down for a
minute, and the plan moves its traffic to the hub and back in one step for both ends.

## What a refusal looks like

`Plan` returns an error with a code (`types.HasCode(err, types.ErrValidationFailed)`) and the
result still carries the validator's report. If a plan was in force and the new one is worse,
`result.Decisions` holds a `FailoverRejected` and the plan in force stays.

## Testing your own use

`internal/e2e` and `test/e2e/phase1` are the end-to-end suite this repository runs; they show
every call above in use. `make e2e-phase1-fast` runs it.
