package corpus

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	gitAuth "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// gitCommitAndPush commits the corpus changes and pushes to the remote.
func (p *Publisher) gitCommitAndPush(ctx context.Context, result *PublishResult) error {
	corpus := p.common.CorpusDir()

	// Open the repo
	repo, err := git.PlainOpen(corpus)
	if err != nil {
		slog.Warn("git open (repo may not exist yet)", "err", err)
		return nil // non-fatal for now
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("git worktree: %w", err)
	}

	// Add all changes
	if addErr := worktree.AddWithOptions(&git.AddOptions{All: true}); addErr != nil {
		return fmt.Errorf("git add: %w", addErr)
	}

	status, err := worktree.Status()
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if status.IsClean() {
		slog.Info("no staged changes; skipping commit")
		return nil
	}

	// Commit
	msg := fmt.Sprintf("fantasy-corpus: refresh team state, evidence, and daily brief\n\nexport_time: %s\nchange_count: %d\nevidence_current: %d\nplayers: %d\n",
		p.common.NowISO(), result.Changes, result.EvidenceCurrent, result.Players)
	commit, err := worktree.Commit(msg, &git.CommitOptions{
		Author: &object.Signature{
			Name:  p.common.gitAuthorName,
			Email: p.common.gitAuthorEmail,
			When:  p.common.NowTime(),
		},
	})
	if err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	slog.Info("committed", "sha", commit.String())

	// Push
	auth := &gitAuth.BasicAuth{
		Username: "x-access-token",
		Password: p.common.gitPAT,
	}
	if err := repo.PushContext(ctx, &git.PushOptions{
		Auth: auth,
	}); err != nil {
		return fmt.Errorf("git push: %w", err)
	}

	slog.Info("pushed to remote", "sha", commit.String())
	return nil
}
