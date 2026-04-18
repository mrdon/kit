# syntax=docker/dockerfile:1.6
# ^^ enables RUN --mount=type=cache for Go/npm caches.

# Frontend build stage — produces web/app/dist which the Go binary embeds.
# npm cache is mounted so repeated deploys reuse the package tarballs
# without re-downloading them from the registry.
FROM node:22-alpine AS frontend
WORKDIR /app/web/app
COPY web/app/package.json web/app/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/app/ ./
RUN npm run build

# Whisper build stage — compiles whisper-cli and downloads the model.
# Pinned to a release tag so the layer cache is stable; bumping the tag
# is the only thing that invalidates this stage.
FROM alpine:3.19 AS whisper
ARG WHISPER_TAG=v1.8.4
RUN apk add --no-cache build-base cmake git curl
WORKDIR /src
RUN git clone --depth 1 --branch ${WHISPER_TAG} https://github.com/ggerganov/whisper.cpp .
RUN cmake -B build -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF \
 && cmake --build build --config Release -j --target whisper-cli
RUN curl -sSL -o /ggml-base.en.bin \
    https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.en.bin

# Build stage. GOCACHE/GOMODCACHE are cache-mounted so incremental Go
# builds don't re-download modules or recompile unchanged stdlib.
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
# Drop in the frontend bundle from the frontend stage so //go:embed picks it up.
COPY --from=frontend /app/web/app/dist ./web/app/dist

ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go build \
    -ldflags "-s -w -X github.com/mrdon/kit/internal/buildinfo.Version=$VERSION -X github.com/mrdon/kit/internal/buildinfo.Commit=$COMMIT -X github.com/mrdon/kit/internal/buildinfo.Date=$DATE" \
    -o /kit ./cmd/kit

# Runtime stage
FROM alpine:3.19

# ffmpeg normalizes browser uploads to 16kHz mono wav; libstdc++ is the
# whisper-cli runtime dep (whisper.cpp is C++).
RUN apk add --no-cache ca-certificates tzdata poppler-utils ffmpeg libstdc++

COPY --from=builder /kit /usr/local/bin/kit
COPY --from=whisper /src/build/bin/whisper-cli /usr/local/bin/whisper-cli
COPY --from=whisper /ggml-base.en.bin /models/ggml-base.en.bin

ENV WHISPER_BIN=/usr/local/bin/whisper-cli \
    WHISPER_MODEL=/models/ggml-base.en.bin

EXPOSE 8488

CMD ["kit"]
