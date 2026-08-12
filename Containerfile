# syntax=docker/dockerfile:1

FROM golang:1.26 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
  go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
  --mount=type=cache,target=/root/.cache/go-build \
  CGO_ENABLED=0 go build \
  -trimpath \
  -ldflags="-s -w" \
  -o /out/ff-engine \
  ./cmd/fantasy-football-engine && \
  touch /out/.keep

FROM gcr.io/distroless/static-debian13:nonroot

COPY --chmod=0555 --from=builder /out/ff-engine /ff-engine

# Ensures a newly created Docker volume inherits writable ownership.
COPY --chown=65532:65532 --from=builder /out/.keep /data/.keep

USER 65532:65532

# Docker-only convenience: Kubernetes still needs an explicit volumeMount.
VOLUME ["/data"]

EXPOSE 3100

# Docker HEALTHCHECK: distroless has no shell/curl/wget, so exec the binary's
# `health` subcommand, which HTTP-probes the daemon's own /readyz endpoint.
# start-period gives migrations + scheduler init room before a miss counts.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["/ff-engine", "health"]

ENTRYPOINT ["/ff-engine"]
CMD ["-config", "/config/config.yaml"]
