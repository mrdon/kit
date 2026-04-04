# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

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
