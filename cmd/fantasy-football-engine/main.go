package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"

	"github.com/markis/fantasy-football-engine/internal/config"
	"github.com/markis/fantasy-football-engine/internal/corpus"
	"github.com/markis/fantasy-football-engine/internal/db"
	"github.com/markis/fantasy-football-engine/internal/embed"
	"github.com/markis/fantasy-football-engine/internal/llm"
	"github.com/markis/fantasy-football-engine/internal/mcp"
	"github.com/markis/fantasy-football-engine/internal/pipeline"
	"github.com/markis/fantasy-football-engine/internal/query"
	"github.com/markis/fantasy-football-engine/internal/scheduler"
	"github.com/markis/fantasy-football-engine/internal/sleeper"
	ffsync "github.com/markis/fantasy-football-engine/internal/sync"
	"github.com/markis/fantasy-football-engine/internal/telemetry"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	flag.Parse()

	// Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	// Init telemetry
	telemetry.Init(cfg.Telemetry.ServiceName, cfg.Telemetry.OTelEndpoint)

	slog.Info("starting fantasy-football-engine", "mcp_addr", cfg.Server.MCPAddr)

	// Connect to database
	ctx := context.Background()
	pool, err := db.New(ctx, cfg.Database.DSN)
	if err != nil {
		slog.Error("database connection", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Run migrations (in-place upgrade: marks existing migrations as applied)
	if migrErr := pool.RunMigrations(ctx); migrErr != nil {
		slog.Error("migrations", "err", migrErr)
		os.Exit(1)
	}

	// Initialize clients
	embedClient := embed.New(cfg.Embeddings.URL, cfg.Embeddings.Model)
	llmClient := llm.New(cfg.LLM.URL, cfg.LLM.Model, cfg.LLM.APIKey, cfg.LLMTimeout())
	sleeperClient := sleeper.New(cfg.Sleeper.BaseURL)

	// Initialize pipeline components
	rssFetcher := pipeline.NewRSSFetcher(pool)
	bodyFetcher := pipeline.NewBodyFetcher(pool)
	enricher := pipeline.NewEnricher(pool, llmClient, cfg.LLM.MaxConcurrency)
	embedder := pipeline.NewEmbedder(pool, embedClient, cfg.Embeddings.BatchSize)
	dedupChecker := pipeline.NewDedupChecker(pool)
	clusterer := pipeline.NewClusterer(pool)
	factExtractor := pipeline.NewFactExtractor(pool, llmClient, embedClient)
	storyGenerator := pipeline.NewStoryGenerator(pool, llmClient)
	fpNewsFetcher := pipeline.NewFPNewsFetcher(pool, cfg)

	// Initialize sync components
	playerSyncer := ffsync.NewPlayerSyncer(pool, sleeperClient)
	rankingsSyncer := ffsync.NewRankingsSyncer(pool)
	fcSyncer := ffsync.NewFantasyCalcSyncer(pool)
	fpInjuriesSyncer := ffsync.NewFPInjuriesSyncer(pool, cfg)
	fpRankingsSyncer := ffsync.NewFPRankingsSyncer(pool, cfg)
	leaguemateSyncer := ffsync.NewLeaguemateSyncer(pool, sleeperClient)
	tradesSyncer := ffsync.NewLeaguemateTradesSyncer(pool, sleeperClient)
	leaguemateAssessor := ffsync.NewLeaguemateAssessor(pool, llmClient)
	teamAssessor := ffsync.NewTeamAssessor(pool, sleeperClient)

	// Initialize corpus publisher
	common := corpus.New(pool, sleeperClient, cfg.Corpus.RepoDir)
	common.SetGitIdentity(cfg.Corpus.AuthorName, cfg.Corpus.AuthorEmail, cfg.Corpus.GitPAT)
	publisher := corpus.NewPublisher(common)

	// Initialize query service
	queryService := query.New(pool, embedClient, sleeperClient)

	// Initialize MCP server
	mcpServer := mcp.New(cfg.Server.MCPAddr, queryService)

	// Initialize scheduler
	sched, err := scheduler.New(cfg.Scheduler.Timezone)
	if err != nil {
		slog.Error("scheduler init", "err", err)
		os.Exit(1)
	}

	// Register step functions
	registerSteps(sched, cfg, rssFetcher, bodyFetcher, enricher, embedder, dedupChecker,
		clusterer, factExtractor, storyGenerator, fpNewsFetcher,
		playerSyncer, rankingsSyncer, fcSyncer, fpInjuriesSyncer, fpRankingsSyncer,
		leaguemateSyncer, tradesSyncer, leaguemateAssessor, teamAssessor,
		publisher)

	// Set pipeline trigger for MCP
	mcpServer.SetTrigger(func(ctx context.Context, step string) error {
		job := config.JobConfig{Step: step, Name: "manual:" + step}
		return sched.TriggerStep(ctx, step, job)
	})

	// Register cron jobs from config
	for _, job := range cfg.Scheduler.Jobs {
		if err := sched.AddJob(job); err != nil {
			slog.Warn("add cron job", "name", job.Name, "err", err)
		}
	}

	// Start scheduler
	sched.Start()

	// Start MCP server in a goroutine
	go func() {
		if err := mcpServer.Start(); err != nil {
			slog.Error("MCP server", "err", err)
		}
	}()

	slog.Info("fantasy-football-engine running", "mcp_addr", cfg.Server.MCPAddr, "jobs", len(cfg.Scheduler.Jobs))

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigChan
	slog.Info("shutdown signal received", "signal", sig)

	sched.Stop()
	slog.Info("fantasy-football-engine stopped")
}

func registerSteps(
	sched *scheduler.Scheduler,
	cfg *config.Config,
	rssFetcher *pipeline.RSSFetcher,
	bodyFetcher *pipeline.BodyFetcher,
	enricher *pipeline.Enricher,
	embedder *pipeline.Embedder,
	dedupChecker *pipeline.DedupChecker,
	clusterer *pipeline.Clusterer,
	factExtractor *pipeline.FactExtractor,
	storyGenerator *pipeline.StoryGenerator,
	fpNewsFetcher *pipeline.FPNewsFetcher,
	playerSyncer *ffsync.PlayerSyncer,
	rankingsSyncer *ffsync.RankingsSyncer,
	fcSyncer *ffsync.FantasyCalcSyncer,
	fpInjuriesSyncer *ffsync.FPInjuriesSyncer,
	fpRankingsSyncer *ffsync.FPRankingsSyncer,
	leaguemateSyncer *ffsync.LeaguemateSyncer,
	tradesSyncer *ffsync.LeaguemateTradesSyncer,
	leaguemateAssessor *ffsync.LeaguemateAssessor,
	teamAssessor *ffsync.TeamAssessor,
	publisher *corpus.Publisher,
) {
	// Pipeline: fetch RSS. Sources are independent, so fetch them
	// concurrently (bounded) instead of one at a time; each worker logs its
	// own failure and returns nil so one bad feed can't cancel the rest.
	sched.RegisterStep("pipeline.fetch", func(ctx context.Context, job config.JobConfig) error {
		var g errgroup.Group
		g.SetLimit(8)
		for _, src := range cfg.Sources {
			g.Go(func() error {
				if _, err := rssFetcher.Fetch(ctx, src.URL, 45); err != nil {
					slog.Warn("fetch RSS", "url", src.URL, "err", err)
				}
				return nil
			})
		}
		return g.Wait()
	})

	// Pipeline: fetch bodies
	sched.RegisterStep("pipeline.fetch_bodies", func(ctx context.Context, job config.JobConfig) error {
		limit := 20
		if job.Limit > 0 {
			limit = job.Limit
		}
		_, err := bodyFetcher.FetchBatch(ctx, limit)
		return err
	})

	// Pipeline: fetch FP news
	sched.RegisterStep("pipeline.fetch_fp_news", func(ctx context.Context, job config.JobConfig) error {
		_, err := fpNewsFetcher.Fetch(ctx)
		return err
	})

	// Pipeline: enrich
	sched.RegisterStep("pipeline.enrich", func(ctx context.Context, job config.JobConfig) error {
		limit := 50
		if job.Limit > 0 {
			limit = job.Limit
		}
		_, err := enricher.EnrichBatch(ctx, limit)
		return err
	})

	// Pipeline: embed
	sched.RegisterStep("pipeline.embed", func(ctx context.Context, job config.JobConfig) error {
		limit := 50
		if job.Limit > 0 {
			limit = job.Limit
		}
		_, err := embedder.EmbedBatch(ctx, limit)
		return err
	})

	// Pipeline: dedup
	sched.RegisterStep("pipeline.dedup", func(ctx context.Context, job config.JobConfig) error {
		limit := 100
		if job.Limit > 0 {
			limit = job.Limit
		}
		_, err := dedupChecker.CheckBatch(ctx, limit)
		return err
	})

	// Pipeline: cluster
	sched.RegisterStep("pipeline.cluster", func(ctx context.Context, job config.JobConfig) error {
		_, err := clusterer.AssignBatch(ctx)
		return err
	})

	// Pipeline: facts
	sched.RegisterStep("pipeline.facts", func(ctx context.Context, job config.JobConfig) error {
		limit := 50
		if job.Limit > 0 {
			limit = job.Limit
		}
		_, err := factExtractor.ExtractBatch(ctx, limit)
		return err
	})

	// Pipeline: stories
	sched.RegisterStep("pipeline.stories", func(ctx context.Context, job config.JobConfig) error {
		limit := 10
		if job.Limit > 0 {
			limit = job.Limit
		}
		_, err := storyGenerator.GenerateBatch(ctx, limit)
		return err
	})

	// Sync: players
	sched.RegisterStep("sync.players", func(ctx context.Context, job config.JobConfig) error {
		_, err := playerSyncer.Sync(ctx)
		return err
	})

	// Sync: rankings
	sched.RegisterStep("sync.rankings", func(ctx context.Context, job config.JobConfig) error {
		_, err := rankingsSyncer.Sync(ctx, job.Market, job.Source)
		return err
	})

	// Sync: FantasyCalc
	sched.RegisterStep("sync.fantasycalc", func(ctx context.Context, job config.JobConfig) error {
		_, err := fcSyncer.SyncAll(ctx)
		return err
	})

	// Sync: FP injuries
	sched.RegisterStep("sync.fp_injuries", func(ctx context.Context, job config.JobConfig) error {
		_, err := fpInjuriesSyncer.Sync(ctx)
		return err
	})

	// Sync: FP rankings
	sched.RegisterStep("sync.fp_rankings", func(ctx context.Context, job config.JobConfig) error {
		_, err := fpRankingsSyncer.Sync(ctx)
		return err
	})

	// Sync: leaguemates
	sched.RegisterStep("sync.leaguemates", func(ctx context.Context, job config.JobConfig) error {
		_, err := leaguemateSyncer.Sync(ctx, 15, "")
		return err
	})

	// Sync: leaguemate trades
	sched.RegisterStep("sync.leaguemates_trades", func(ctx context.Context, job config.JobConfig) error {
		_, err := tradesSyncer.Sync(ctx, 3)
		return err
	})

	// Sync: assess leaguemates
	sched.RegisterStep("sync.assess_leaguemates", func(ctx context.Context, job config.JobConfig) error {
		batch := 12
		if job.Limit > 0 {
			batch = job.Limit
		}
		_, err := leaguemateAssessor.Assess(ctx, batch)
		return err
	})

	// Sync: assess teams
	sched.RegisterStep("sync.assess_teams", func(ctx context.Context, job config.JobConfig) error {
		_, err := teamAssessor.Assess(ctx)
		return err
	})

	// Corpus: publish
	sched.RegisterStep("corpus.publish", func(ctx context.Context, job config.JobConfig) error {
		mode := job.Mode
		if mode == "" {
			mode = "daily"
		}
		_, err := publisher.Publish(ctx, mode, false)
		return err
	})

	// Combined step: embed + dedup + enrich + cluster (cron #5).
	// Order matters: dedup and enrich both claim work from the same
	// `quality_score IS NULL` queue, so dedup must run first — otherwise
	// enrich (which unconditionally sets quality_score) empties that queue
	// before dedup ever sees it. embed runs before dedup so semantic dedup
	// has an embedding to compare; cluster runs last since it also reads
	// embeddings and is independent of quality_score.
	sched.RegisterStep("pipeline.enrich_embed_dedup_cluster", func(ctx context.Context, job config.JobConfig) error {
		limit := 10
		if job.Limit > 0 {
			limit = job.Limit
		}
		_, errEmbed := embedder.EmbedBatch(ctx, 50)
		_, errDedup := dedupChecker.CheckBatch(ctx, 50)
		_, errEnrich := enricher.EnrichBatch(ctx, limit)
		_, errCluster := clusterer.AssignBatch(ctx)
		return errors.Join(errEmbed, errDedup, errEnrich, errCluster)
	})

	// Combined step: assess_teams + publish weekly (cron #19)
	sched.RegisterStep("sync.assess_teams_publish_weekly", func(ctx context.Context, job config.JobConfig) error {
		_, errAssess := teamAssessor.Assess(ctx)
		_, errPublish := publisher.Publish(ctx, "weekly", false)
		return errors.Join(errAssess, errPublish)
	})
}
