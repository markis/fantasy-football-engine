package models

import (
	"time"

	"github.com/google/uuid"
)

// Source represents an RSS feed or API source.
type Source struct {
	ID             uuid.UUID   `json:"id" db:"id"`
	Name           string      `json:"name" db:"name"`
	Type           string      `json:"type" db:"type"`
	URL            string      `json:"url" db:"url"`
	FetchMethod    string      `json:"fetch_method" db:"fetch_method"`
	PollIntervalSec int        `json:"poll_interval_sec" db:"poll_interval_sec"`
	IsActive       bool        `json:"is_active" db:"is_active"`
	ETag           *string     `json:"etag,omitempty" db:"etag"`
	LastModified   *string     `json:"last_modified,omitempty" db:"last_modified"`
	LastPollAt     *time.Time  `json:"last_poll_at,omitempty" db:"last_poll_at"`
	NextPollAt     *time.Time  `json:"next_poll_at,omitempty" db:"next_poll_at"`
	CreatedAt      time.Time   `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at" db:"updated_at"`
}

// RawDocument is an immutable fetched feed payload.
type RawDocument struct {
	ID           uuid.UUID  `json:"id" db:"id"`
	SourceID     uuid.UUID  `json:"source_id" db:"source_id"`
	URL          string     `json:"url" db:"url"`
	FetchStatus  int        `json:"fetch_status" db:"fetch_status"`
	Headers      []byte     `json:"headers" db:"headers"`
	BodyText     *string    `json:"body_text,omitempty" db:"body_text"`
	ContentType  *string    `json:"content_type,omitempty" db:"content_type"`
	FetchedAt    time.Time  `json:"fetched_at" db:"fetched_at"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
}

// NewsItem is an RSS article or API news entry.
type NewsItem struct {
	ID                uuid.UUID  `json:"id" db:"id"`
	SourceID          uuid.UUID  `json:"source_id" db:"source_id"`
	SourceType        string     `json:"source_type" db:"source_type"`
	ExternalID        *string    `json:"external_id,omitempty" db:"external_id"`
	RawDocumentID     *uuid.UUID `json:"raw_document_id,omitempty" db:"raw_document_id"`
	URL               *string    `json:"url,omitempty" db:"url"`
	CanonicalURL      *string    `json:"canonical_url,omitempty" db:"canonical_url"`
	CanonicalURLHash  *string    `json:"canonical_url_hash,omitempty" db:"canonical_url_hash"`
	Title             *string    `json:"title,omitempty" db:"title"`
	Author            *string    `json:"author,omitempty" db:"author"`
	PublishedAt       *time.Time `json:"published_at,omitempty" db:"published_at"`
	ContentHTML       *string    `json:"content_html,omitempty" db:"content_html"`
	ContentText       *string    `json:"content_text,omitempty" db:"content_text"`
	ContentMarkdown   *string    `json:"content_markdown,omitempty" db:"content_markdown"`
	SummaryShort      *string    `json:"summary_short,omitempty" db:"summary_short"`
	ContentHash       *string    `json:"content_hash,omitempty" db:"content_hash"`
	Simhash           *int64     `json:"simhash,omitempty" db:"simhash"`
	FetchedAt         *time.Time `json:"fetched_at,omitempty" db:"fetched_at"`
	BodyFetchStatus   string     `json:"body_fetch_status" db:"body_fetch_status"`
	BodyFetchAttempts int        `json:"body_fetch_attempts" db:"body_fetch_attempts"`
	BodyFetchedAt     *time.Time `json:"body_fetched_at,omitempty" db:"body_fetched_at"`
	IsRelevant        bool       `json:"is_relevant" db:"is_relevant"`
	IsNews            bool       `json:"is_news" db:"is_news"`
	NewsStory         *string    `json:"news_story,omitempty" db:"news_story"`
	NewsStoryGeneratedAt *time.Time `json:"news_story_generated_at,omitempty" db:"news_story_generated_at"`
	NewsStoryModel    *string    `json:"news_story_model,omitempty" db:"news_story_model"`
	Embedding         []float32  `json:"-" db:"-"`
	Language          *string    `json:"language,omitempty" db:"language"`
	Topics            []string   `json:"topics" db:"topics"`
	Entities          []string   `json:"entities" db:"entities"`
	QualityScore      *float64   `json:"quality_score,omitempty" db:"quality_score"`
	ClusterID         *uuid.UUID `json:"cluster_id,omitempty" db:"cluster_id"`
	CreatedAt         time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at" db:"updated_at"`
}

// StoryCluster groups related news items.
type StoryCluster struct {
	ID                uuid.UUID  `json:"id" db:"id"`
	ClusterKey        *string    `json:"cluster_key,omitempty" db:"cluster_key"`
	RepresentativeTitle *string  `json:"representative_title,omitempty" db:"representative_title"`
	ItemIDs           []uuid.UUID `json:"item_ids" db:"item_ids"`
	ImportanceScore   *float64   `json:"importance_score,omitempty" db:"importance_score"`
	FirstSeenAt       time.Time  `json:"first_seen_at" db:"first_seen_at"`
	LastSeenAt        time.Time  `json:"last_seen_at" db:"last_seen_at"`
	CreatedAt         time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at" db:"updated_at"`
}

// Fact is an atomic fantasy football fact extracted from a news item.
type Fact struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	NewsItemID  uuid.UUID  `json:"news_item_id" db:"news_item_id"`
	FactText    string     `json:"fact_text" db:"fact_text"`
	Entities    []string   `json:"entities" db:"entities"`
	Topics      []string   `json:"topics" db:"topics"`
	OccurredAt  time.Time  `json:"occurred_at" db:"occurred_at"`
	Confidence  *string    `json:"confidence,omitempty" db:"confidence"`
	Embedding   []float32  `json:"-" db:"-"`
	ExtractedAt time.Time  `json:"extracted_at" db:"extracted_at"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
}

// Player is a Sleeper NFL player.
type Player struct {
	ID                   uuid.UUID  `json:"id" db:"id"`
	SleeperPlayerID      string     `json:"sleeper_player_id" db:"sleeper_player_id"`
	FirstName            *string    `json:"first_name,omitempty" db:"first_name"`
	LastName             *string    `json:"last_name,omitempty" db:"last_name"`
	FullName             *string    `json:"full_name,omitempty" db:"full_name"`
	SearchFullName       *string    `json:"search_full_name,omitempty" db:"search_full_name"`
	Position             *string    `json:"position,omitempty" db:"position"`
	FantasyPositions     []string   `json:"fantasy_positions" db:"fantasy_positions"`
	Team                 *string    `json:"team,omitempty" db:"team"`
	TeamAbbr             *string    `json:"team_abbr,omitempty" db:"team_abbr"`
	Status               *string    `json:"status,omitempty" db:"status"`
	Active               bool       `json:"active" db:"active"`
	InjuryStatus         *string    `json:"injury_status,omitempty" db:"injury_status"`
	InjuryBodyPart       *string    `json:"injury_body_part,omitempty" db:"injury_body_part"`
	InjuryNotes          *string    `json:"injury_notes,omitempty" db:"injury_notes"`
	InjuryStartDate      *time.Time `json:"injury_start_date,omitempty" db:"injury_start_date"`
	Age                  *int       `json:"age,omitempty" db:"age"`
	YearsExp             *int       `json:"years_exp,omitempty" db:"years_exp"`
	BirthDate            *time.Time `json:"birth_date,omitempty" db:"birth_date"`
	Height               *string    `json:"height,omitempty" db:"height"`
	Weight               *string    `json:"weight,omitempty" db:"weight"`
	College              *string    `json:"college,omitempty" db:"college"`
	Number               *int       `json:"number,omitempty" db:"number"`
	DepthChartPosition   *string    `json:"depth_chart_position,omitempty" db:"depth_chart_position"`
	DepthChartOrder      *int       `json:"depth_chart_order,omitempty" db:"depth_chart_order"`
	PracticeParticipation *string   `json:"practice_participation,omitempty" db:"practice_participation"`
	PracticeDescription  *string    `json:"practice_description,omitempty" db:"practice_description"`
	GsisID               *string    `json:"gsis_id,omitempty" db:"gsis_id"`
	EspnID               *string    `json:"espn_id,omitempty" db:"espn_id"`
	RotowireID           *string    `json:"rotowire_id,omitempty" db:"rotowire_id"`
	RotoworldID          *string    `json:"rotoworld_id,omitempty" db:"rotoworld_id"`
	YahooID              *string    `json:"yahoo_id,omitempty" db:"yahoo_id"`
	SportradarID         *string    `json:"sportradar_id,omitempty" db:"sportradar_id"`
	StatsID              *string    `json:"stats_id,omitempty" db:"stats_id"`
	NewsUpdated          *time.Time `json:"news_updated,omitempty" db:"news_updated"`
	LastSyncedAt         time.Time  `json:"last_synced_at" db:"last_synced_at"`
	CreatedAt            time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at" db:"updated_at"`
}

// PlayerRanking is a dynasty trade value / ranking snapshot.
type PlayerRanking struct {
	ID                  uuid.UUID  `json:"id" db:"id"`
	PlayerID            uuid.UUID  `json:"player_id" db:"player_id"`
	Source              string     `json:"source" db:"source"`
	Market              int        `json:"market" db:"market"`
	NameID              *string    `json:"name_id,omitempty" db:"name_id"`
	Position            *string    `json:"position,omitempty" db:"position"`
	Team                *string    `json:"team,omitempty" db:"team"`
	OverallRank         *int       `json:"overall_rank,omitempty" db:"overall_rank"`
	PositionRank        *int       `json:"position_rank,omitempty" db:"position_rank"`
	SfOverallRank       *int       `json:"sf_overall_rank,omitempty" db:"sf_overall_rank"`
	SfPositionRank      *int       `json:"sf_position_rank,omitempty" db:"sf_position_rank"`
	TradeValue          *int       `json:"trade_value,omitempty" db:"trade_value"`
	SfTradeValue        *int       `json:"sf_trade_value,omitempty" db:"sf_trade_value"`
	RedraftValue        *int       `json:"redraft_value,omitempty" db:"redraft_value"`
	AllTimeHigh         *int       `json:"all_time_high,omitempty" db:"all_time_high"`
	AllTimeLow          *int       `json:"all_time_low,omitempty" db:"all_time_low"`
	LastMonthValue      *int       `json:"last_month_value,omitempty" db:"last_month_value"`
	LastMonthValueSF    *int       `json:"last_month_value_sf,omitempty" db:"last_month_value_sf"`
	LastMonthRank       *int       `json:"last_month_rank,omitempty" db:"last_month_rank"`
	LastMonthRankSF     *int       `json:"last_month_rank_sf,omitempty" db:"last_month_rank_sf"`
	AvgADP              *string    `json:"avg_adp,omitempty" db:"avg_adp"`
	PercentOwned        *string    `json:"percent_owned,omitempty" db:"percent_owned"`
	PercentStarted      *string    `json:"percent_started,omitempty" db:"percent_started"`
	SnapshotDate        time.Time  `json:"snapshot_date" db:"snapshot_date"`
	DataDate            *time.Time `json:"data_date,omitempty" db:"data_date"`
	CreatedAt           time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at" db:"updated_at"`
}

// PlayerRankingHistory is an append-only daily ranking snapshot.
type PlayerRankingHistory struct {
	PlayerID      uuid.UUID `json:"player_id" db:"player_id"`
	Source        string    `json:"source" db:"source"`
	Market        int       `json:"market" db:"market"`
	SnapshotDate  time.Time `json:"snapshot_date" db:"snapshot_date"`
	TradeValue    *int      `json:"trade_value,omitempty" db:"trade_value"`
	OverallRank   *int      `json:"overall_rank,omitempty" db:"overall_rank"`
	PositionRank  *int      `json:"position_rank,omitempty" db:"position_rank"`
	RedraftValue  *int      `json:"redraft_value,omitempty" db:"redraft_value"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
}

// League is a Sleeper league (Markis's or discovered leaguemate league).
type League struct {
	LeagueID            string     `json:"league_id" db:"league_id"`
	Name                *string    `json:"name,omitempty" db:"name"`
	Season              *string    `json:"season,omitempty" db:"season"`
	Sport               string     `json:"sport" db:"sport"`
	Status              *string    `json:"status,omitempty" db:"status"`
	NumTeams            *int       `json:"num_teams,omitempty" db:"num_teams"`
	HasSuperflex        *bool      `json:"has_superflex,omitempty" db:"has_superflex"`
	IsBestBall          *bool      `json:"is_best_ball,omitempty" db:"is_best_ball"`
	LeagueType          *int       `json:"league_type,omitempty" db:"league_type"`
	RosterPositions     []byte     `json:"roster_positions" db:"roster_positions"`
	Settings            []byte     `json:"settings" db:"settings"`
	PreviousLeagueID    *string    `json:"previous_league_id,omitempty" db:"previous_league_id"`
	IsMarkisLeague      bool       `json:"is_markis_league" db:"is_markis_league"`
	DiscoveredViaUserID *string    `json:"discovered_via_user_id,omitempty" db:"discovered_via_user_id"`
	FirstSeenAt         time.Time  `json:"first_seen_at" db:"first_seen_at"`
	LastSyncedAt        time.Time  `json:"last_synced_at" db:"last_synced_at"`
}

// SleeperUser is a Sleeper user identity.
type SleeperUser struct {
	UserID       string    `json:"user_id" db:"user_id"`
	Username     *string   `json:"username,omitempty" db:"username"`
	DisplayName  *string   `json:"display_name,omitempty" db:"display_name"`
	Avatar       *string   `json:"avatar,omitempty" db:"avatar"`
	IsMarkis     bool      `json:"is_markis" db:"is_markis"`
	FirstSeenAt  time.Time `json:"first_seen_at" db:"first_seen_at"`
	LastSyncedAt time.Time `json:"last_synced_at" db:"last_synced_at"`
}

// LeagueManager is one row per (league, roster slot).
type LeagueManager struct {
	LeagueID     string    `json:"league_id" db:"league_id"`
	UserID       string    `json:"user_id" db:"user_id"`
	RosterID     int       `json:"roster_id" db:"roster_id"`
	TeamName     *string   `json:"team_name,omitempty" db:"team_name"`
	CoOwner      bool      `json:"co_owner" db:"co_owner"`
	IsMarkis     bool      `json:"is_markis" db:"is_markis"`
	Wins         *int      `json:"wins,omitempty" db:"wins"`
	Losses       *int      `json:"losses,omitempty" db:"losses"`
	Ties         *int      `json:"ties,omitempty" db:"ties"`
	Fpts         *float32  `json:"fpts,omitempty" db:"fpts"`
	LastSyncedAt time.Time `json:"last_synced_at" db:"last_synced_at"`
}

// LeaguemateRosterPlayer is a player on a roster in a league.
type LeaguemateRosterPlayer struct {
	LeagueID         string    `json:"league_id" db:"league_id"`
	RosterID         int       `json:"roster_id" db:"roster_id"`
	SleeperPlayerID  string    `json:"sleeper_player_id" db:"sleeper_player_id"`
	Slot             string    `json:"slot" db:"slot"`
	SnapshotAt       time.Time `json:"snapshot_at" db:"snapshot_at"`
}

// LeaguemateTransaction is a completed Sleeper trade.
type LeaguemateTransaction struct {
	TransactionID       string     `json:"transaction_id" db:"transaction_id"`
	LeagueID            string     `json:"league_id" db:"league_id"`
	Type                string     `json:"type" db:"type"`
	Status              *string    `json:"status,omitempty" db:"status"`
	Creator             *string    `json:"creator,omitempty" db:"creator"`
	Week                *int       `json:"week,omitempty" db:"week"`
	RosterIDs           []int      `json:"roster_ids" db:"roster_ids"`
	ConsenterIDs        []int      `json:"consenter_ids" db:"consenter_ids"`
	CreatedAt           *time.Time `json:"created_at,omitempty" db:"created_at"`
	StatusUpdatedAt     *time.Time `json:"status_updated_at,omitempty" db:"status_updated_at"`
	IsMarkisLeague      bool       `json:"is_markis_league" db:"is_markis_league"`
	InvolvesWatchSet    bool       `json:"involves_watch_set" db:"involves_watch_set"`
	Raw                 []byte     `json:"raw" db:"raw"`
	LastSyncedAt        time.Time  `json:"last_synced_at" db:"last_synced_at"`
}

// LeaguemateTradeAsset is one row per moving player or pick in a trade.
type LeaguemateTradeAsset struct {
	ID               uuid.UUID  `json:"id" db:"id"`
	TransactionID    string     `json:"transaction_id" db:"transaction_id"`
	LeagueID         string     `json:"league_id" db:"league_id"`
	AssetType        string     `json:"asset_type" db:"asset_type"`
	SleeperPlayerID  *string    `json:"sleeper_player_id,omitempty" db:"sleeper_player_id"`
	PickSeason       *string    `json:"pick_season,omitempty" db:"pick_season"`
	PickRound        *int       `json:"pick_round,omitempty" db:"pick_round"`
	PickRosterID     *int       `json:"pick_roster_id,omitempty" db:"pick_roster_id"`
	FromRosterID     *int       `json:"from_roster_id,omitempty" db:"from_roster_id"`
	ToRosterID       *int       `json:"to_roster_id,omitempty" db:"to_roster_id"`
	IsWatchSet       *bool      `json:"is_watch_set,omitempty" db:"is_watch_set"`
}

// LeaguemateSignal is a weekly tendency dossier for a leaguemate.
type LeaguemateSignal struct {
	SnapshotDate   time.Time `json:"snapshot_date" db:"snapshot_date"`
	UserID         string    `json:"user_id" db:"user_id"`
	Username       *string   `json:"username,omitempty" db:"username"`
	DisplayName    *string   `json:"display_name,omitempty" db:"display_name"`
	LeaguesCount   *int      `json:"leagues_count,omitempty" db:"leagues_count"`
	WinPct         *float32  `json:"win_pct,omitempty" db:"win_pct"`
	AvgCoreAge     *float32  `json:"avg_core_age,omitempty" db:"avg_core_age"`
	ContenderScore *int      `json:"contender_score,omitempty" db:"contender_score"`
	PositionBias   []byte    `json:"position_bias" db:"position_bias"`
	TradeCount     *int      `json:"trade_count,omitempty" db:"trade_count"`
	TradeCount30d  *int      `json:"trade_count_30d,omitempty" db:"trade_count_30d"`
	OverpayDelta   *float32  `json:"overpay_delta,omitempty" db:"overpay_delta"`
	TradesValuated *int      `json:"trades_valuated,omitempty" db:"trades_valuated"`
	PicksAcquired  *int      `json:"picks_acquired,omitempty" db:"picks_acquired"`
	PicksTraded    *int      `json:"picks_traded,omitempty" db:"picks_traded"`
	NetFirsts      *int      `json:"net_firsts,omitempty" db:"net_firsts"`
	Dossier        *string   `json:"dossier,omitempty" db:"dossier"`
	RawSignals     []byte    `json:"raw_signals" db:"raw_signals"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}

// FPNoteCheck tracks when each FantasyPros player was last checked.
type FPNoteCheck struct {
	Slug          string     `json:"slug" db:"slug"`
	PlayerName    *string    `json:"player_name,omitempty" db:"player_name"`
	ECR           *int       `json:"ecr,omitempty" db:"ecr"`
	LastCheckedAt time.Time  `json:"last_checked_at" db:"last_checked_at"`
	HasNote       bool       `json:"has_note" db:"has_note"`
}

// NFLState is the current Sleeper NFL state.
type NFLState struct {
	Week       int    `json:"week"`
	Season     string `json:"season"`
	SeasonType string `json:"season_type"`
	LeagueYear int    `json:"league_year"`
}