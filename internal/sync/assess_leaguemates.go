package sync

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/markis/fantasy-football-engine/internal/db"
	"github.com/markis/fantasy-football-engine/internal/llm"
)

// LeaguemateAssessor computes tendency signals and LLM dossiers.
type LeaguemateAssessor struct {
	pool *db.Pool
	llm  *llm.Client
}

// NewLeaguemateAssessor creates a new leaguemate assessor.
func NewLeaguemateAssessor(pool *db.Pool, llmClient *llm.Client) *LeaguemateAssessor {
	return &LeaguemateAssessor{pool: pool, llm: llmClient}
}

// AssessResult is the result of a leaguemate assessment batch.
type AssessResult struct {
	Assessed int    `json:"assessed"`
	Status   string `json:"status"`
}

// Assess computes signals and LLM dossiers for leaguemates not yet snapshotted this week.
func (a *LeaguemateAssessor) Assess(ctx context.Context, batch int) (*AssessResult, error) {
	if batch == 0 {
		batch = 12
	}
	result := &AssessResult{Status: "ok"}

	// Get current ISO week (anchored to Monday)
	now := time.Now().UTC()
	isoWeekStart := now.AddDate(0, 0, -int(now.Weekday())+1) // Monday
	snapshotDate := isoWeekStart

	// Get leaguemates not yet snapshotted for this week
	rows, err := a.pool.Query(ctx, `
		SELECT DISTINCT lm.user_id
		FROM league_manager lm
		JOIN league l ON l.league_id = lm.league_id
		WHERE l.is_markis_league AND lm.user_id != $1
		AND NOT EXISTS (
			SELECT 1 FROM leaguemate_signal ls
			WHERE ls.user_id = lm.user_id AND ls.snapshot_date = $2
		)
		LIMIT $3
	`, "558115100726579200", snapshotDate, batch)
	if err != nil {
		return nil, fmt.Errorf("query leaguemates for assessment: %w", err)
	}
	defer rows.Close()

	var userIDs []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			continue
		}
		userIDs = append(userIDs, uid)
	}

	for _, uid := range userIDs {
		signals := a.computeSignals(ctx, uid)
		dossier := a.generateDossier(ctx, signals)
		a.snapshotSignal(ctx, snapshotDate, uid, signals, dossier)
		result.Assessed++
	}

	slog.Info("leaguemate assessment complete", "assessed", result.Assessed)
	return result, nil
}

type leaguemateSignals struct {
	Username       string
	DisplayName    string
	LeaguesCount   int
	WinPct         float32
	AvgCoreAge     float32
	ContenderScore int
	PositionBias   map[string]int
	TradeCount     int
	TradeCount30d  int
	OverpayDelta   float32
	TradesValuated int
	PicksAcquired  int
	PicksTraded    int
	NetFirsts      int
	RecentTrades   string
}

func (a *LeaguemateAssessor) computeSignals(ctx context.Context, userID string) *leaguemateSignals {
	s := &leaguemateSignals{PositionBias: make(map[string]int)}

	// Get username/display_name
	_ = a.pool.QueryRow(ctx,
		"SELECT COALESCE(username, ''), COALESCE(display_name, '') FROM sleeper_user WHERE user_id = $1",
		userID).Scan(&s.Username, &s.DisplayName)

	// League count
	_ = a.pool.QueryRow(ctx,
		"SELECT count(DISTINCT league_id) FROM league_manager WHERE user_id = $1", userID).Scan(&s.LeaguesCount)

	// Win pct
	var wins, losses, ties int
	_ = a.pool.QueryRow(ctx,
		"SELECT COALESCE(sum(wins), 0), COALESCE(sum(losses), 0), COALESCE(sum(ties), 0) FROM league_manager WHERE user_id = $1",
		userID).Scan(&wins, &losses, &ties)
	total := wins + losses + ties
	if total > 0 {
		s.WinPct = float32(wins) / float32(total)
	}

	// Contender score: based on win pct + roster value
	s.ContenderScore = int(s.WinPct * 100)
	if s.ContenderScore > 100 {
		s.ContenderScore = 100
	}

	// Trade counts
	_ = a.pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE created_at > now() - interval '30 days')
		FROM leaguemate_transaction t
		JOIN leaguemate_trade_asset a ON a.transaction_id = t.transaction_id
		WHERE t.type = 'trade' AND t.status = 'complete'
		AND (a.from_roster_id IN (SELECT roster_id FROM league_manager WHERE user_id = $1)
		     OR a.to_roster_id IN (SELECT roster_id FROM league_manager WHERE user_id = $1))
	`, userID).Scan(&s.TradeCount, &s.TradeCount30d)

	// Picks
	_ = a.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE a.asset_type = 'pick' AND a.to_roster_id IN (SELECT roster_id FROM league_manager WHERE user_id = $1)),
			count(*) FILTER (WHERE a.asset_type = 'pick' AND a.from_roster_id IN (SELECT roster_id FROM league_manager WHERE user_id = $1)),
			count(*) FILTER (WHERE a.asset_type = 'pick' AND a.pick_round = 1 AND a.to_roster_id IN (SELECT roster_id FROM league_manager WHERE user_id = $1))
			- count(*) FILTER (WHERE a.asset_type = 'pick' AND a.pick_round = 1 AND a.from_roster_id IN (SELECT roster_id FROM league_manager WHERE user_id = $1))
		FROM leaguemate_transaction t
		JOIN leaguemate_trade_asset a ON a.transaction_id = t.transaction_id
		WHERE t.type = 'trade' AND t.status = 'complete'
	`, userID).Scan(&s.PicksAcquired, &s.PicksTraded, &s.NetFirsts)

	return s
}

func (a *LeaguemateAssessor) generateDossier(ctx context.Context, s *leaguemateSignals) string {
	if s == nil {
		return ""
	}
	prompt := fmt.Sprintf(`Write a concise "how to trade with this manager" dossier (3-4 sentences) for a fantasy football dynasty league manager.

Manager: %s (%s)
Leagues: %d | Win%%: %.1f | Contender score: %d
Trades total: %d | Last 30d: %d
Picks acquired: %d | Picks traded: %d | Net 1sts: %d

Based on these signals, describe their tendency (contender/rebuilder, pick-hoarder or win-now, overpayer or value-shopper) and how to approach trades with them. Be concise and direct. Output ONLY the dossier prose.`,
		s.DisplayName, s.Username, s.LeaguesCount, s.WinPct*100, s.ContenderScore,
		s.TradeCount, s.TradeCount30d, s.PicksAcquired, s.PicksTraded, s.NetFirsts)

	dossier, err := a.llm.Chat(ctx, prompt, 0.3)
	if err != nil {
		slog.Warn("generate dossier", "err", err)
		return ""
	}
	return strings.TrimSpace(dossier)
}

func (a *LeaguemateAssessor) snapshotSignal(ctx context.Context, snapshotDate time.Time, userID string, s *leaguemateSignals, dossier string) {
	posBiasJSON := []byte("{}")
	if len(s.PositionBias) > 0 {
		posBiasJSON = []byte("{}") // simplified
	}

	_, err := a.pool.Exec(ctx, `
		INSERT INTO leaguemate_signal (snapshot_date, user_id, username, display_name,
			leagues_count, win_pct, contender_score, position_bias,
			trade_count, trade_count_30d, picks_acquired, picks_traded, net_firsts,
			dossier, raw_signals, created_at)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, now())
		ON CONFLICT (snapshot_date, user_id) DO UPDATE SET
			dossier = EXCLUDED.dossier, raw_signals = EXCLUDED.raw_signals,
			trade_count = EXCLUDED.trade_count, trade_count_30d = EXCLUDED.trade_count_30d,
			contender_score = EXCLUDED.contender_score, win_pct = EXCLUDED.win_pct
	`, snapshotDate, userID, s.Username, s.DisplayName,
		s.LeaguesCount, s.WinPct, s.ContenderScore, posBiasJSON,
		s.TradeCount, s.TradeCount30d, s.PicksAcquired, s.PicksTraded, s.NetFirsts,
		dossier, posBiasJSON)
	if err != nil {
		slog.Warn("snapshot signal", "user", userID, "err", err)
	}
}