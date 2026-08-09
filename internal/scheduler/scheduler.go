package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/markis/fantasy-football-engine/internal/config"
	"github.com/robfig/cron/v3"
)

// StepFunc is a function that runs a pipeline step.
type StepFunc func(ctx context.Context, job config.JobConfig) error

// Scheduler manages cron jobs for the daemon.
type Scheduler struct {
	cron    *cron.Cron
	steps   map[string]StepFunc
	mu      sync.RWMutex
	status  map[string]JobStatus
}

// JobStatus tracks the last run of a job.
type JobStatus struct {
	Name       string    `json:"name"`
	LastRun    time.Time `json:"last_run"`
	LastStatus string    `json:"last_status"` // ok | error
	LastDuration string  `json:"last_duration"`
	NextRun    time.Time `json:"next_run"`
}

// New creates a new scheduler.
func New(tz string) (*Scheduler, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	c := cron.New(cron.WithLocation(loc), cron.WithSeconds())
	s := &Scheduler{
		cron:   c,
		steps:  make(map[string]StepFunc),
		status: make(map[string]JobStatus),
	}
	return s, nil
}

// RegisterStep registers a step function.
func (s *Scheduler) RegisterStep(name string, fn StepFunc) {
	s.mu.Lock()
	s.steps[name] = fn
	s.mu.Unlock()
}

// AddJob adds a cron job.
func (s *Scheduler) AddJob(job config.JobConfig) error {
	s.mu.RLock()
	stepFn, ok := s.steps[job.Step]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("step %s not registered", job.Step)
	}

	_, err := s.cron.AddFunc(job.Schedule, func() {
		s.runJob(job, stepFn)
	})
	if err != nil {
		return fmt.Errorf("add cron job %s: %w", job.Name, err)
	}
	slog.Info("registered cron job", "name", job.Name, "schedule", job.Schedule, "step", job.Step)
	return nil
}

func (s *Scheduler) runJob(job config.JobConfig, fn StepFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Hour)
	defer cancel()

	start := time.Now()
	slog.Info("running cron job", "name", job.Name, "step", job.Step)
	err := fn(ctx, job)
	duration := time.Since(start)

	status := "ok"
	if err != nil {
		status = "error"
		slog.Error("cron job failed", "name", job.Name, "err", err, "duration", duration)
	} else {
		slog.Info("cron job complete", "name", job.Name, "duration", duration)
	}

	s.mu.Lock()
	s.status[job.Name] = JobStatus{
		Name:         job.Name,
		LastRun:      start,
		LastStatus:   status,
		LastDuration: duration.String(),
	}
	s.mu.Unlock()
}

// Start starts the cron scheduler.
func (s *Scheduler) Start() {
	s.cron.Start()
	slog.Info("scheduler started", "jobs", len(s.cron.Entries()))
}

// Stop stops the cron scheduler.
func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
	slog.Info("scheduler stopped")
}

// GetStatus returns the status of all jobs.
func (s *Scheduler) GetStatus() []JobStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]JobStatus, 0, len(s.status))
	for _, st := range s.status {
		result = append(result, st)
	}
	return result
}

// TriggerStep manually triggers a step.
func (s *Scheduler) TriggerStep(ctx context.Context, step string, job config.JobConfig) error {
	s.mu.RLock()
	fn, ok := s.steps[step]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("step %s not registered", step)
	}
	job.Step = step
	go s.runJob(job, fn)
	return nil
}