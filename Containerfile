# Multi-stage build for fantasy-football-engine
# Stage 1: Build the Go binary
FROM golang:1.24-bookworm AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /ff-engine ./cmd/fantasy-football-engine

# Stage 2: Minimal runtime image
# The :nonroot variant ships a non-root user (UID 65532, name "nonroot").
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /ff-engine /ff-engine

# Run as the unprivileged nonroot user (UID 65532).
USER nonroot

# Config and data are mounted at runtime. This path remains writable even
# when the container is started with a read-only root filesystem
# (e.g. `docker run --read-only` or Kubernetes readOnlyRootFilesystem: true),
# because declared VOLUMEs get their own writable overlay.
VOLUME ["/data"]

EXPOSE 3100

ENTRYPOINT ["/ff-engine"]
CMD ["-config", "/data/config.yaml"]