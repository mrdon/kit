# builder/runtime

Sandboxed script engine for the `builder` app. Admin-written Python runs here, server-side, with a host-function allowlist and native resource limits.

## Engine: Monty

[Monty](https://github.com/pydantic/monty) is pydantic's Rust-based Python-subset sandbox. It gives us real-Python fluency (not a Python-shaped dialect), native memory/CPU/allocation/recursion limits, host-function allowlist by default, and microsecond per-call startup.

Kit owns the full pipeline:

- **Go glue** lives in this directory (`monty.go`, `wasm.go`, `errors.go`, `engine.go`, `monty_engine.go`). Originally forked from [monty-go](https://github.com/fugue-labs/monty-go) v0.2.0 (MIT).
- **Rust shim** lives at `../../../../third_party/monty-wasm/`. Compiles Monty to `wasm32-wasip1`.
- **Build** via `make monty-wasm` at the repo root. Runs the Rust toolchain inside a Docker container so Kit devs never install Rust. `monty.wasm` (~4.8 MB) is committed to this directory so day-to-day dev and CI don't need Docker either.

Bump the Monty version in `third_party/monty-wasm/crates/monty-wasm/Cargo.toml`, then run `make monty-wasm`.

## Execution model

- One `Runner` per process. Created once at startup; compiles the WASM module and caches it to `$TMPDIR/kit-monty-wasm-cache` so subsequent process starts are sub-second.
- Each `Runner.Execute` (or `MontyEngine.Run`) spins up a fresh WASM instance with its own linear memory — pure isolation between calls, no state leak.
- Host functions are passed per call via `WithExternalFunc`. Script code that calls a name not in the allowlist gets a `NameError`.
- Resource limits (`Limits`) enforce memory, wall clock, allocations, and recursion depth. Exceeding any of them returns a `*MontyError`.
- `ctx.Cancel()` tears down the instance within a few ms.

## Limits scripts run under

| | |
|---|---|
| Memory | `Limits.MaxMemoryBytes` |
| Wall clock | `Limits.MaxDuration` + `ctx.Done()` |
| Allocations | `Limits.MaxAllocations` |
| Recursion depth | `Limits.MaxRecursionDepth` |

## What scripts can't do

Monty is a subset; admin code can't use:

- Classes (`class`)
- `import` of any kind — scripts get only the allowlisted host functions
- `try/except` around host-call errors — a Go callback returning an error unwinds the interpreter

Numbers round-trip across the WASM boundary as `float64` (JSON wire format). Code that needs int semantics coerces Go-side.

## Test layout

- `testmain_test.go` — builds one shared `Runner` + `MontyEngine` for the whole package via `TestMain`. Every test uses the shared instance to avoid paying the wazero compile cost more than once.
- `monty_hello_test.go` — smoke test.
- `monty_hostcall_test.go` — host function dispatch round-trips.
- `monty_limits_test.go` — resource-limit enforcement.
- `monty_acceptance_test.go` — Python idioms and plan-level acceptance scripts.
- `monty_engine_test.go` — `Engine` interface tests.
