# Multi-stage build for fantasy-football-engine
# Stage 1: Build the Go binary
FROM golang:1.24-bookworm AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /ff-engine ./cmd/fantasy-football-engine

# Stage 2: Minimal runtime image
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /ff-engine /ff-engine

# Config and data are mounted at runtime
VOLUME ["/data"]

EXPOSE 3100

ENTRYPOINT ["/ff-engine"]
CMD ["-config", "/data/config.yaml"]