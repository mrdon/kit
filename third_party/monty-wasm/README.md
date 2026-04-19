# monty-wasm

Rust shim that wraps [pydantic/monty](https://github.com/pydantic/monty) and compiles it to `wasm32-wasip1`. Forked from [fugue-labs/monty-go](https://github.com/fugue-labs/monty-go) at tag `v0.2.0` (MIT, see `LICENSE`).

## Build

From the repo root:

```
make monty-wasm
```

That runs `docker buildx` against the `Dockerfile` here and drops the compiled `monty.wasm` into `internal/apps/builder/runtime/monty.wasm`, which the Go loader embeds via `//go:embed`.

The built binary is checked into the tree so day-to-day dev and CI don't need a Rust toolchain. Rerun `make monty-wasm` only when you bump the `monty` dependency in `crates/monty-wasm/Cargo.toml`.
