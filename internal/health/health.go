// Package health provides readiness checking for the fantasy-football-engine
// daemon. The Checker pings the daemon's own Postgres connection pool with a
// bounded timeout and maps the result to an HTTP 200/503 response, so both
// Kubernetes HTTP probes and the built-in `health` subcommand (Docker
// HEALTHCHECK on distroless, where no curl/wget exists) converge on a single
// readiness definition exposed at GET /readyz.
package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"ff-engine/internal/db"
)

// checkTimeout bounds a single readiness probe so a hung DB connection can't
// back up Docker/Kubernetes probes (which default to short timeouts).
const checkTimeout = 2 * time.Second

// Checker runs readiness probes against the daemon's dependencies. Only the
// hard dependency (Postgres) is gated: every MCP tool routes through the pool,
// so a lost DB makes the server useless. External HTTP clients (embeddings,
// LLM, Sleeper) are not probed — they're touched by scheduled jobs and a few
// tools, and probing them on every check adds latency and flakiness from
// transient upstream errors.
type Checker struct {
	pool  *db.Pool
	start time.Time
}

// Report is the JSON body returned by GET /readyz.
type Report struct {
	Status  string            `json:"status"` // "ok" | "degraded"
	Service string            `json:"service"`
	Tools   int               `json:"tools"`
	Checks  map[string]string `json:"checks"` // name -> "ok" | "fail: <error>"
	UptimeS int64             `json:"uptimeS"`
}

// New creates a Checker backed by the given pool. start records daemon boot
// time so reports include uptime.
func New(pool *db.Pool) *Checker {
	return &Checker{pool: pool, start: time.Now()}
}

// SetStart records the daemon boot time for uptime reporting. Call once at
// startup if the Checker is constructed before main begins timing.
func (c *Checker) SetStart(t time.Time) { c.start = t }

// Check runs the readiness probes and returns a Report. The Postgres probe
// uses a context bounded by checkTimeout.
func (c *Checker) Check(ctx context.Context, toolCount int) Report {
	r := Report{
		Service: "fantasy-football-engine",
		Tools:   toolCount,
		Checks:  map[string]string{},
		Status:  "ok",
	}
	if !c.start.IsZero() {
		r.UptimeS = int64(time.Since(c.start).Seconds())
	}

	if c.pool == nil {
		r.Checks["postgres"] = "fail: no pool"
		r.Status = "degraded"
	} else {
		dctx, cancel := context.WithTimeout(ctx, checkTimeout)
		err := c.pool.Ping(dctx)
		cancel()
		if err != nil {
			r.Checks["postgres"] = "fail: " + err.Error()
			r.Status = "degraded"
		} else {
			r.Checks["postgres"] = "ok"
		}
	}
	return r
}

// Handler returns an http.HandlerFunc that runs Check and writes 200 (ok) or
// 503 (degraded) with the JSON report. It is safe to use as a readiness
// probe target.
func (c *Checker) Handler(getToolCount func() int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		toolCount := 0
		if getToolCount != nil {
			toolCount = getToolCount()
		}
		rep := c.Check(r.Context(), toolCount)
		code := http.StatusOK
		if rep.Status != "ok" {
			code = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		if err := json.NewEncoder(w).Encode(rep); err != nil {
			slog.Warn("health: encode report", "err", err)
		}
	}
}
