package corpus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"
)

var (
	errPublishInProgress = errors.New("publish already in progress")
	errValidationFailed  = errors.New("validation failed")
)

// Publisher is the corpus publishing orchestrator.
type Publisher struct {
	common *Common
	mu     sync.Mutex
}

// NewPublisher creates a new publisher.
func NewPublisher(common *Common) *Publisher {
	return &Publisher{common: common}
}

// PublishResult is the result of a publish run.
type PublishResult struct {
	Mode               string `json:"mode"`
	Committed          bool   `json:"committed"`
	EvidenceCurrent    int    `json:"evidenceCurrent"`
	EvidenceSuperseded int    `json:"evidenceSuperseded"`
	Changes            int    `json:"changes"`
	Players            int    `json:"players"`
	Signals            int    `json:"signals"`
	Valuations         int    `json:"valuations"`
	Status             string `json:"status"`
}

// Publish runs the full render → validate → commit → push cycle. Publish
// runs are serialized: they're reachable from multiple, independently
// scheduled cron jobs plus a manual MCP trigger, and two concurrent runs
// would race on the same shared staging directory.
func (p *Publisher) Publish(ctx context.Context, mode string, dryRun bool) (*PublishResult, error) {
	if !p.mu.TryLock() {
		return nil, errPublishInProgress
	}
	defer p.mu.Unlock()

	result := &PublishResult{Mode: mode, Status: "ok"}

	staging := p.common.StagingDir()
	corpus := p.common.CorpusDir()

	if err := prepareStagingDir(staging); err != nil {
		return nil, err
	}

	slog.Info("rendering to staging", "mode", mode)
	summaries := p.renderStagingSections(ctx, staging, corpus)

	if err := validateStaging(staging, result); err != nil {
		return result, err
	}

	populatePublishResult(result, summaries)

	if mode == "export" || dryRun {
		slog.Info("export complete (no commit)")
		return result, nil
	}

	if err := p.finalizePublish(ctx, staging, corpus, result); err != nil {
		return nil, err
	}

	return result, nil
}

// renderSummaries collects the per-section summaries produced while
// rendering the staging tree, so Publish can populate its result in one
// place after all sections have run.
type renderSummaries struct {
	evidence map[string]any
	datasets map[string]any
	manifest map[string]any
}

// prepareStagingDir clears and recreates the staging directory.
//
// os.RemoveAll can fail with ENOTEMPTY on Docker named volumes (overlayfs/
// btrfs backed) when the kernel hasn't yet flushed unlinkat from a concurrent
// process or a previous run. Retrying with a short backoff reliably clears
// the directory in that case.
func prepareStagingDir(staging string) error {
	const maxRetries = 3
	var lastErr error
	for attempt := range maxRetries {
		if err := os.RemoveAll(staging); err != nil {
			lastErr = err
			if !errors.Is(err, syscall.ENOTEMPTY) {
				return fmt.Errorf("clear staging: %w", err)
			}
			time.Sleep(100 * time.Millisecond << attempt) // 100ms, 200ms, 400ms
			continue
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		return fmt.Errorf("clear staging: %w", lastErr)
	}
	if err := os.MkdirAll(staging, 0o750); err != nil {
		return fmt.Errorf("create staging: %w", err)
	}
	return nil
}

// renderStagingSections renders every corpus section into staging. Each
// section's error is logged and swallowed — a single section failing
// shouldn't abort the rest of the render — and later validation is what
// catches an incomplete/broken staging tree.
func (p *Publisher) renderStagingSections(ctx context.Context, staging, corpus string) renderSummaries {
	var s renderSummaries

	if _, err := p.renderTeam(ctx, staging); err != nil {
		slog.Warn("render team", "err", err)
	}

	evidenceSummary, err := p.renderEvidence(ctx, staging, corpus)
	if err != nil {
		slog.Warn("render evidence", "err", err)
	}
	s.evidence = evidenceSummary

	s.datasets, err = p.renderDatasets(ctx, staging)
	if err != nil {
		slog.Warn("render datasets", "err", err)
	}

	if _, currentErr := p.renderCurrent(ctx, staging); currentErr != nil {
		slog.Warn("render current", "err", currentErr)
	}

	if _, leaguemateErr := p.renderLeaguemates(ctx, staging); leaguemateErr != nil {
		slog.Warn("render leaguemates", "err", leaguemateErr)
	}

	s.manifest, err = p.renderManifest(ctx, staging, corpus, evidenceSummary)
	if err != nil {
		slog.Warn("render manifest", "err", err)
	}

	return s
}

// validateStaging runs corpus validation over the staging tree, recording
// failure status on result before returning the (wrapped) validation error.
func validateStaging(staging string, result *PublishResult) error {
	slog.Info("validating staging...")
	errs := Validate(staging)
	if len(errs) > 0 {
		slog.Error("validation failed", "errors", len(errs))
		for _, e := range errs {
			slog.Error("validation error", "msg", e)
		}
		result.Status = "validation_failed"
		return fmt.Errorf("%w (%d errors)", errValidationFailed, len(errs))
	}
	slog.Info("validation OK")
	return nil
}

// populatePublishResult copies counters out of the per-section summaries
// into the PublishResult returned to callers.
func populatePublishResult(result *PublishResult, s renderSummaries) {
	if s.evidence != nil {
		if v, ok := s.evidence["current_count"].(int); ok {
			result.EvidenceCurrent = v
		}
		if v, ok := s.evidence["superseded_count"].(int); ok {
			result.EvidenceSuperseded = v
		}
	}
	if s.datasets != nil {
		if v, ok := s.datasets["players"].(int); ok {
			result.Players = v
		}
		if v, ok := s.datasets["signals"].(int); ok {
			result.Signals = v
		}
		if v, ok := s.datasets["valuations"].(int); ok {
			result.Valuations = v
		}
	}
	if s.manifest != nil {
		if v, ok := s.manifest["changes"].(int); ok {
			result.Changes = v
		}
	}
}

// finalizePublish syncs staging into the corpus (when there are material
// changes) and commits/pushes the result.
func (p *Publisher) finalizePublish(ctx context.Context, staging, corpus string, result *PublishResult) error {
	if !p.materialChanges(staging, corpus) {
		slog.Info("no material changes — skipping commit/push")
		return nil
	}

	slog.Info("material changes detected — syncing")
	if err := p.sync(staging, corpus); err != nil {
		return fmt.Errorf("sync: %w", err)
	}

	if err := p.commitAndPush(ctx, result); err != nil {
		slog.Warn("commit and push", "err", err)
	}
	result.Committed = true

	slog.Info("publish complete")
	return nil
}

func (p *Publisher) materialChanges(staging, corpus string) bool {
	// Simplified: if corpus dir doesn't exist or is empty, there are changes
	entries, err := os.ReadDir(corpus)
	if err != nil || len(entries) == 0 {
		return true
	}
	// Compare file lists and hashes
	stagingFiles := listSubstanceFiles(staging)
	corpusFiles := listSubstanceFiles(corpus)
	if len(stagingFiles) != len(corpusFiles) {
		return true
	}
	for rel, sfull := range stagingFiles {
		cfull, ok := corpusFiles[rel]
		if !ok {
			return true
		}
		sHash, err := FileSHA256Bytes(sfull)
		if err != nil {
			return true
		}
		cHash, err := FileSHA256Bytes(cfull)
		if err != nil {
			return true
		}
		if sHash != cHash {
			return true
		}
	}
	return false
}

func (p *Publisher) sync(staging, corpus string) error {
	// os.Root (Go 1.24) confines file access to each directory tree, blocking
	// path traversal and symlinks that escape it. gosec recognizes Root methods
	// as safe (G304 only flags package-level os.ReadFile/Open/OpenFile/Create),
	// so this needs no //nolint directives.
	if err := os.MkdirAll(corpus, 0o750); err != nil {
		return fmt.Errorf("create corpus root: %w", err)
	}
	corpusRoot, err := os.OpenRoot(corpus)
	if err != nil {
		return fmt.Errorf("open corpus root: %w", err)
	}
	defer corpusRoot.Close()

	stagingRoot, err := os.OpenRoot(staging)
	if err != nil {
		return fmt.Errorf("open staging root: %w", err)
	}
	defer stagingRoot.Close()

	stagingFiles := listSubstanceFiles(staging)
	for rel := range stagingFiles {
		data, err := rootReadAll(stagingRoot, rel)
		if err != nil {
			return err
		}
		if err := rootWriteAll(corpusRoot, rel, data, 0o600); err != nil {
			return err
		}
	}

	// Copy extras
	for _, extra := range []string{"evidence/index.md", fileManifest, "datasets/change-log.jsonl"} {
		if _, err := stagingRoot.Stat(extra); err != nil {
			continue
		}
		data, err := rootReadAll(stagingRoot, extra)
		if err != nil {
			slog.Warn("failed to read extra file", "src", extra, "err", err)
			continue
		}
		if err := rootWriteAll(corpusRoot, extra, data, 0o600); err != nil {
			slog.Warn("failed to write extra file", "dst", extra, "err", err)
		}
	}
	return nil
}

func (p *Publisher) commitAndPush(ctx context.Context, result *PublishResult) error {
	// In the Go version, we use go-git for commit + push.
	// This is handled by the git.go module.
	return p.gitCommitAndPush(ctx, result)
}

// listSubstanceFiles walks a directory and returns relative path -> full path.
func listSubstanceFiles(root string) map[string]string {
	files := make(map[string]string)
	entries, err := os.ReadDir(root)
	if err != nil {
		return files
	}
	walkDir(root, "", files, entries)
	return files
}

func walkDir(root, prefix string, files map[string]string, entries []os.DirEntry) {
	for _, entry := range entries {
		if !entry.IsDir() {
			processFile(root, prefix, files, entry)
			continue
		}
		if entry.Name() == ".git" || entry.Name() == ".staging" || entry.Name() == "schemas" {
			continue
		}
		sub, err := os.ReadDir(filepath.Join(root, prefix, entry.Name()))
		if err != nil {
			continue
		}
		walkDir(root, filepath.Join(prefix, entry.Name()), files, sub)
	}
}

func processFile(root, prefix string, files map[string]string, entry os.DirEntry) {
	name := entry.Name()
	if name == fileGitignore || name == fileManifest || name == ".publish.log" {
		return
	}
	rel := filepath.Join(prefix, name)
	// Skip protected files
	if isProtected(rel) {
		return
	}
	files[rel] = filepath.Join(root, rel)
}

func isProtected(rel string) bool {
	protected := []string{
		"README.md", "AGENTS.md", "source-registry.csv",
		"strategy/decision-log.jsonl", "strategy/outcome-review.jsonl",
		"team/league-constitution.md", "team/league-context.md",
		"strategy/dynasty-playbook.md", "strategy/trade-policy.md",
		"strategy/draft-policy.md", "strategy/waiver-policy.md",
		"strategy/lineup-policy.md",
	}
	if slices.Contains(protected, rel) {
		return true
	}
	// Skip .gitkeep
	if filepath.Base(rel) == ".gitkeep" {
		return true
	}
	// Skip archive dir
	if filepath.Dir(rel) == "archive" {
		return true
	}
	return false
}
