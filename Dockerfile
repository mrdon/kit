# Frontend build stage — produces web/app/dist which the Go binary embeds.
FROM node:22-alpine AS frontend
WORKDIR /app/web/app
COPY web/app/package.json web/app/package-lock.json ./
RUN npm ci
COPY web/app/ ./
RUN npm run build

# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Drop in the frontend bundle from the frontend stage so //go:embed picks it up.
COPY --from=frontend /app/web/app/dist ./web/app/dist

ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

RUN CGO_ENABLED=0 go build \
    -ldflags "-s -w -X github.com/mrdon/kit/internal/buildinfo.Version=$VERSION -X github.com/mrdon/kit/internal/buildinfo.Commit=$COMMIT -X github.com/mrdon/kit/internal/buildinfo.Date=$DATE" \
    -o /kit ./cmd/kit

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata poppler-utils

COPY --from=builder /kit /usr/local/bin/kit

EXPOSE 8488

CMD ["kit"]
