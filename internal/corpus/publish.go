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
	EvidenceCurrent    int    `json:"evidence_current"`
	EvidenceSuperseded int    `json:"evidence_superseded"`
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

	// Clear staging
	if err := os.RemoveAll(staging); err != nil {
		return nil, fmt.Errorf("clear staging: %w", err)
	}
	if err := os.MkdirAll(staging, 0o750); err != nil {
		return nil, fmt.Errorf("create staging: %w", err)
	}

	slog.Info("rendering to staging", "mode", mode)

	// Render all sections
	teamSummary, err := p.renderTeam(ctx, staging)
	if err != nil {
		slog.Warn("render team", "err", err)
	}

	evidenceSummary, err := p.renderEvidence(ctx, staging, corpus)
	if err != nil {
		slog.Warn("render evidence", "err", err)
	}

	datasetsSummary, err := p.renderDatasets(ctx, staging)
	if err != nil {
		slog.Warn("render datasets", "err", err)
	}

	currentSummary, err := p.renderCurrent(ctx, staging)
	if err != nil {
		slog.Warn("render current", "err", err)
	}

	leaguemateSummary, err := p.renderLeaguemates(ctx, staging)
	if err != nil {
		slog.Warn("render leaguemates", "err", err)
	}

	manifestSummary, err := p.renderManifest(ctx, staging, corpus, evidenceSummary)
	if err != nil {
		slog.Warn("render manifest", "err", err)
	}

	_ = teamSummary
	_ = currentSummary
	_ = leaguemateSummary

	// Validate
	slog.Info("validating staging...")
	errs := Validate(staging)
	if len(errs) > 0 {
		slog.Error("validation failed", "errors", len(errs))
		for _, e := range errs {
			slog.Error("validation error", "msg", e)
		}
		result.Status = "validation_failed"
		return result, fmt.Errorf("%w (%d errors)", errValidationFailed, len(errs))
	}
	slog.Info("validation OK")

	if evidenceSummary != nil {
		result.EvidenceCurrent = evidenceSummary["current_count"].(int)      //nolint:errcheck // Type assertion guaranteed by renderEvidence
		result.EvidenceSuperseded = evidenceSummary["superseded_count"].(int) //nolint:errcheck // Type assertion guaranteed by renderEvidence
	}
	if datasetsSummary != nil {
		result.Players = datasetsSummary["players"].(int) //nolint:errcheck // Type assertion guaranteed by renderDatasets
		result.Signals = datasetsSummary["signals"].(int) //nolint:errcheck // Type assertion guaranteed by renderDatasets
		result.Valuations = datasetsSummary["valuations"].(int) //nolint:errcheck // Type assertion guaranteed by renderDatasets
	}
	if manifestSummary != nil {
		result.Changes = manifestSummary["changes"].(int) //nolint:errcheck // Type assertion guaranteed by renderCurrentLeaguematesManifest
	}

	if mode == "export" || dryRun {
		slog.Info("export complete (no commit)")
		return result, nil
	}

	// Check for material changes
	if !p.materialChanges(staging, corpus) {
		slog.Info("no material changes — skipping commit/push")
		return result, nil
	}

	// Sync staging to corpus
	slog.Info("material changes detected — syncing")
	if err := p.sync(staging, corpus); err != nil {
		return nil, fmt.Errorf("sync: %w", err)
	}

	// Commit and push
	if err := p.commitAndPush(ctx, result); err != nil {
		slog.Warn("commit and push", "err", err)
	}
	result.Committed = true

	slog.Info("publish complete")
	return result, nil
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
		sHash, _ := FileSHA256Bytes(sfull) //nolint:errcheck // Both files exist; error is impossible
		cHash, _ := FileSHA256Bytes(cfull) //nolint:errcheck // Both files exist; error is impossible
		if sHash != cHash {
			return true
		}
	}
	return false
}

func (p *Publisher) sync(staging, corpus string) error {
	stagingFiles := listSubstanceFiles(staging)
	for rel, full := range stagingFiles {
		dst := filepath.Join(corpus, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return err
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			return err
		}
	}
	// Copy extras
	for _, extra := range []string{"evidence/index.md", "corpus-manifest.json", "datasets/change-log.jsonl"} {
		src := filepath.Join(staging, extra)
		if _, err := os.Stat(src); err == nil {
			dst := filepath.Join(corpus, extra)
			if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
				slog.Warn("failed to create destination directory", "path", filepath.Dir(dst), "err", err)
				continue
			}
			data, err := os.ReadFile(src)
			if err != nil {
				slog.Warn("failed to read extra file", "src", src, "err", err)
				continue
			}
			if err := os.WriteFile(dst, data, 0o600); err != nil {
				slog.Warn("failed to write extra file", "dst", dst, "err", err)
			}
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
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".staging" || entry.Name() == "schemas" {
				continue
			}
			sub, err := os.ReadDir(filepath.Join(root, prefix, entry.Name()))
			if err != nil {
				continue
			}
			walkDir(root, filepath.Join(prefix, entry.Name()), files, sub)
		} else {
			name := entry.Name()
			if name == ".gitignore" || name == "corpus-manifest.json" || name == ".publish.log" {
				continue
			}
			rel := filepath.Join(prefix, name)
			// Skip protected files
			if isProtected(rel) {
				continue
			}
			files[rel] = filepath.Join(root, rel)
		}
	}
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
