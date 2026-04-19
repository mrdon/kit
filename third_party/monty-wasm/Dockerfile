# syntax=docker/dockerfile:1.6
# Build stage: compile the Rust shim to wasm32-wasip1.
# Cargo's registry and the build target dir are cache-mounted so repeated
# rebuilds skip re-downloading crates and reuse incremental artifacts.
FROM rust:1.91-slim-bookworm AS builder

RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates pkg-config python3 \
 && rm -rf /var/lib/apt/lists/*

RUN rustup target add wasm32-wasip1

WORKDIR /src
COPY Cargo.toml ./
COPY crates ./crates

RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/usr/local/cargo/git \
    --mount=type=cache,target=/src/target \
    cargo build --target wasm32-wasip1 -p monty-wasm --release \
 && cp target/wasm32-wasip1/release/monty_wasm.wasm /monty.wasm

# Export stage: `docker buildx build --output type=local --target export`
# copies just the .wasm out of this stage.
FROM scratch AS export
COPY --from=builder /monty.wasm /monty.wasm
