package models

import (
	"time"

	"github.com/google/uuid"
)

// Source represents an RSS feed or API source.
type Source struct {
	ID              uuid.UUID  `db:"id"                json:"id"`
	Name            string     `db:"name"              json:"name"`
	Type            string     `db:"type"              json:"type"`
	URL             string     `db:"url"               json:"url"`
	FetchMethod     string     `db:"fetch_method"      json:"fetch_method"`
	PollIntervalSec int        `db:"poll_interval_sec" json:"poll_interval_sec"`
	IsActive        bool       `db:"is_active"         json:"is_active"`
	ETag            *string    `db:"etag"              json:"etag,omitempty"`
	LastModified    *string    `db:"last_modified"     json:"last_modified,omitempty"`
	LastPollAt      *time.Time `db:"last_poll_at"      json:"last_poll_at,omitempty"`
	NextPollAt      *time.Time `db:"next_poll_at"      json:"next_poll_at,omitempty"`
	CreatedAt       time.Time  `db:"created_at"        json:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at"        json:"updated_at"`
}

// RawDocument is an immutable fetched feed payload.
type RawDocument struct {
	ID          uuid.UUID `db:"id"           json:"id"`
	SourceID    uuid.UUID `db:"source_id"    json:"source_id"`
	URL         string    `db:"url"          json:"url"`
	FetchStatus int       `db:"fetch_status" json:"fetch_status"`
	Headers     []byte    `db:"headers"      json:"headers"`
	BodyText    *string   `db:"body_text"    json:"body_text,omitempty"`
	ContentType *string   `db:"content_type" json:"content_type,omitempty"`
	FetchedAt   time.Time `db:"fetched_at"   json:"fetched_at"`
	CreatedAt   time.Time `db:"created_at"   json:"created_at"`
}

// NewsItem is an RSS article or API news entry.
type NewsItem struct {
	ID                   uuid.UUID  `db:"id"                      json:"id"`
	SourceID             uuid.UUID  `db:"source_id"               json:"source_id"`
	SourceType           string     `db:"source_type"             json:"source_type"`
	ExternalID           *string    `db:"external_id"             json:"external_id,omitempty"`
	RawDocumentID        *uuid.UUID `db:"raw_document_id"         json:"raw_document_id,omitempty"`
	URL                  *string    `db:"url"                     json:"url,omitempty"`
	CanonicalURL         *string    `db:"canonical_url"           json:"canonical_url,omitempty"`
	CanonicalURLHash     *string    `db:"canonical_url_hash"      json:"canonical_url_hash,omitempty"`
	Title                *string    `db:"title"                   json:"title,omitempty"`
	Author               *string    `db:"author"                  json:"author,omitempty"`
	PublishedAt          *time.Time `db:"published_at"            json:"published_at,omitempty"`
	ContentHTML          *string    `db:"content_html"            json:"content_html,omitempty"`
	ContentText          *string    `db:"content_text"            json:"content_text,omitempty"`
	ContentMarkdown      *string    `db:"content_markdown"        json:"content_markdown,omitempty"`
	SummaryShort         *string    `db:"summary_short"           json:"summary_short,omitempty"`
	ContentHash          *string    `db:"content_hash"            json:"content_hash,omitempty"`
	Simhash              *int64     `db:"simhash"                 json:"simhash,omitempty"`
	FetchedAt            *time.Time `db:"fetched_at"              json:"fetched_at,omitempty"`
	BodyFetchStatus      string     `db:"body_fetch_status"       json:"body_fetch_status"`
	BodyFetchAttempts    int        `db:"body_fetch_attempts"     json:"body_fetch_attempts"`
	BodyFetchedAt        *time.Time `db:"body_fetched_at"         json:"body_fetched_at,omitempty"`
	IsRelevant           bool       `db:"is_relevant"             json:"is_relevant"`
	IsNews               bool       `db:"is_news"                 json:"is_news"`
	NewsStory            *string    `db:"news_story"              json:"news_story,omitempty"`
	NewsStoryGeneratedAt *time.Time `db:"news_story_generated_at" json:"news_story_generated_at,omitempty"`
	NewsStoryModel       *string    `db:"news_story_model"        json:"news_story_model,omitempty"`
	Embedding            []float32  `db:"-"                       json:"-"`
	Language             *string    `db:"language"                json:"language,omitempty"`
	Topics               []string   `db:"topics"                  json:"topics"`
	Entities             []string   `db:"entities"                json:"entities"`
	QualityScore         *float64   `db:"quality_score"           json:"quality_score,omitempty"`
	ClusterID            *uuid.UUID `db:"cluster_id"              json:"cluster_id,omitempty"`
	CreatedAt            time.Time  `db:"created_at"              json:"created_at"`
	UpdatedAt            time.Time  `db:"updated_at"              json:"updated_at"`
}

// StoryCluster groups related news items.
type StoryCluster struct {
	ID                  uuid.UUID   `db:"id"                   json:"id"`
	ClusterKey          *string     `db:"cluster_key"          json:"cluster_key,omitempty"`
	RepresentativeTitle *string     `db:"representative_title" json:"representative_title,omitempty"`
	ItemIDs             []uuid.UUID `db:"item_ids"             json:"item_ids"`
	ImportanceScore     *float64    `db:"importance_score"     json:"importance_score,omitempty"`
	FirstSeenAt         time.Time   `db:"first_seen_at"        json:"first_seen_at"`
	LastSeenAt          time.Time   `db:"last_seen_at"         json:"last_seen_at"`
	CreatedAt           time.Time   `db:"created_at"           json:"created_at"`
	UpdatedAt           time.Time   `db:"updated_at"           json:"updated_at"`
}

// Fact is an atomic fantasy football fact extracted from a news item.
type Fact struct {
	ID          uuid.UUID `db:"id"           json:"id"`
	NewsItemID  uuid.UUID `db:"news_item_id" json:"news_item_id"`
	FactText    string    `db:"fact_text"    json:"fact_text"`
	Entities    []string  `db:"entities"     json:"entities"`
	Topics      []string  `db:"topics"       json:"topics"`
	OccurredAt  time.Time `db:"occurred_at"  json:"occurred_at"`
	Confidence  *string   `db:"confidence"   json:"confidence,omitempty"`
	Embedding   []float32 `db:"-"            json:"-"`
	ExtractedAt time.Time `db:"extracted_at" json:"extracted_at"`
	CreatedAt   time.Time `db:"created_at"   json:"created_at"`
}

// Player is a Sleeper NFL player.
type Player struct {
	ID                    uuid.UUID  `db:"id"                     json:"id"`
	SleeperPlayerID       string     `db:"sleeper_player_id"      json:"sleeper_player_id"`
	FirstName             *string    `db:"first_name"             json:"first_name,omitempty"`
	LastName              *string    `db:"last_name"              json:"last_name,omitempty"`
	FullName              *string    `db:"full_name"              json:"full_name,omitempty"`
	SearchFullName        *string    `db:"search_full_name"       json:"search_full_name,omitempty"`
	Position              *string    `db:"position"               json:"position,omitempty"`
	FantasyPositions      []string   `db:"fantasy_positions"      json:"fantasy_positions"`
	Team                  *string    `db:"team"                   json:"team,omitempty"`
	TeamAbbr              *string    `db:"team_abbr"              json:"team_abbr,omitempty"`
	Status                *string    `db:"status"                 json:"status,omitempty"`
	Active                bool       `db:"active"                 json:"active"`
	InjuryStatus          *string    `db:"injury_status"          json:"injury_status,omitempty"`
	InjuryBodyPart        *string    `db:"injury_body_part"       json:"injury_body_part,omitempty"`
	InjuryNotes           *string    `db:"injury_notes"           json:"injury_notes,omitempty"`
	InjuryStartDate       *time.Time `db:"injury_start_date"      json:"injury_start_date,omitempty"`
	Age                   *int       `db:"age"                    json:"age,omitempty"`
	YearsExp              *int       `db:"years_exp"              json:"years_exp,omitempty"`
	BirthDate             *time.Time `db:"birth_date"             json:"birth_date,omitempty"`
	Height                *string    `db:"height"                 json:"height,omitempty"`
	Weight                *string    `db:"weight"                 json:"weight,omitempty"`
	College               *string    `db:"college"                json:"college,omitempty"`
	Number                *int       `db:"number"                 json:"number,omitempty"`
	DepthChartPosition    *string    `db:"depth_chart_position"   json:"depth_chart_position,omitempty"`
	DepthChartOrder       *int       `db:"depth_chart_order"      json:"depth_chart_order,omitempty"`
	PracticeParticipation *string    `db:"practice_participation" json:"practice_participation,omitempty"`
	PracticeDescription   *string    `db:"practice_description"   json:"practice_description,omitempty"`
	GsisID                *string    `db:"gsis_id"                json:"gsis_id,omitempty"`
	EspnID                *string    `db:"espn_id"                json:"espn_id,omitempty"`
	RotowireID            *string    `db:"rotowire_id"            json:"rotowire_id,omitempty"`
	RotoworldID           *string    `db:"rotoworld_id"           json:"rotoworld_id,omitempty"`
	YahooID               *string    `db:"yahoo_id"               json:"yahoo_id,omitempty"`
	SportradarID          *string    `db:"sportradar_id"          json:"sportradar_id,omitempty"`
	StatsID               *string    `db:"stats_id"               json:"stats_id,omitempty"`
	NewsUpdated           *time.Time `db:"news_updated"           json:"news_updated,omitempty"`
	LastSyncedAt          time.Time  `db:"last_synced_at"         json:"last_synced_at"`
	CreatedAt             time.Time  `db:"created_at"             json:"created_at"`
	UpdatedAt             time.Time  `db:"updated_at"             json:"updated_at"`
}

// PlayerRanking is a dynasty trade value / ranking snapshot.
type PlayerRanking struct {
	ID               uuid.UUID  `db:"id"                  json:"id"`
	PlayerID         uuid.UUID  `db:"player_id"           json:"player_id"`
	Source           string     `db:"source"              json:"source"`
	Market           int        `db:"market"              json:"market"`
	NameID           *string    `db:"name_id"             json:"name_id,omitempty"`
	Position         *string    `db:"position"            json:"position,omitempty"`
	Team             *string    `db:"team"                json:"team,omitempty"`
	OverallRank      *int       `db:"overall_rank"        json:"overall_rank,omitempty"`
	PositionRank     *int       `db:"position_rank"       json:"position_rank,omitempty"`
	SfOverallRank    *int       `db:"sf_overall_rank"     json:"sf_overall_rank,omitempty"`
	SfPositionRank   *int       `db:"sf_position_rank"    json:"sf_position_rank,omitempty"`
	TradeValue       *int       `db:"trade_value"         json:"trade_value,omitempty"`
	SfTradeValue     *int       `db:"sf_trade_value"      json:"sf_trade_value,omitempty"`
	RedraftValue     *int       `db:"redraft_value"       json:"redraft_value,omitempty"`
	AllTimeHigh      *int       `db:"all_time_high"       json:"all_time_high,omitempty"`
	AllTimeLow       *int       `db:"all_time_low"        json:"all_time_low,omitempty"`
	LastMonthValue   *int       `db:"last_month_value"    json:"last_month_value,omitempty"`
	LastMonthValueSF *int       `db:"last_month_value_sf" json:"last_month_value_sf,omitempty"`
	LastMonthRank    *int       `db:"last_month_rank"     json:"last_month_rank,omitempty"`
	LastMonthRankSF  *int       `db:"last_month_rank_sf"  json:"last_month_rank_sf,omitempty"`
	AvgADP           *string    `db:"avg_adp"             json:"avg_adp,omitempty"`
	PercentOwned     *string    `db:"percent_owned"       json:"percent_owned,omitempty"`
	PercentStarted   *string    `db:"percent_started"     json:"percent_started,omitempty"`
	SnapshotDate     time.Time  `db:"snapshot_date"       json:"snapshot_date"`
	DataDate         *time.Time `db:"data_date"           json:"data_date,omitempty"`
	CreatedAt        time.Time  `db:"created_at"          json:"created_at"`
	UpdatedAt        time.Time  `db:"updated_at"          json:"updated_at"`
}

// PlayerRankingHistory is an append-only daily ranking snapshot.
type PlayerRankingHistory struct {
	PlayerID     uuid.UUID `db:"player_id"     json:"player_id"`
	Source       string    `db:"source"        json:"source"`
	Market       int       `db:"market"        json:"market"`
	SnapshotDate time.Time `db:"snapshot_date" json:"snapshot_date"`
	TradeValue   *int      `db:"trade_value"   json:"trade_value,omitempty"`
	OverallRank  *int      `db:"overall_rank"  json:"overall_rank,omitempty"`
	PositionRank *int      `db:"position_rank" json:"position_rank,omitempty"`
	RedraftValue *int      `db:"redraft_value" json:"redraft_value,omitempty"`
	CreatedAt    time.Time `db:"created_at"    json:"created_at"`
}

// League is a Sleeper league (Markis's or discovered leaguemate league).
type League struct {
	LeagueID            string    `db:"league_id"              json:"league_id"`
	Name                *string   `db:"name"                   json:"name,omitempty"`
	Season              *string   `db:"season"                 json:"season,omitempty"`
	Sport               string    `db:"sport"                  json:"sport"`
	Status              *string   `db:"status"                 json:"status,omitempty"`
	NumTeams            *int      `db:"num_teams"              json:"num_teams,omitempty"`
	HasSuperflex        *bool     `db:"has_superflex"          json:"has_superflex,omitempty"`
	IsBestBall          *bool     `db:"is_best_ball"           json:"is_best_ball,omitempty"`
	LeagueType          *int      `db:"league_type"            json:"league_type,omitempty"`
	RosterPositions     []byte    `db:"roster_positions"       json:"roster_positions"`
	Settings            []byte    `db:"settings"               json:"settings"`
	PreviousLeagueID    *string   `db:"previous_league_id"     json:"previous_league_id,omitempty"`
	IsMarkisLeague      bool      `db:"is_markis_league"       json:"is_markis_league"`
	DiscoveredViaUserID *string   `db:"discovered_via_user_id" json:"discovered_via_user_id,omitempty"`
	FirstSeenAt         time.Time `db:"first_seen_at"          json:"first_seen_at"`
	LastSyncedAt        time.Time `db:"last_synced_at"         json:"last_synced_at"`
}

// SleeperUser is a Sleeper user identity.
type SleeperUser struct {
	UserID       string    `db:"user_id"        json:"user_id"`
	Username     *string   `db:"username"       json:"username,omitempty"`
	DisplayName  *string   `db:"display_name"   json:"display_name,omitempty"`
	Avatar       *string   `db:"avatar"         json:"avatar,omitempty"`
	IsMarkis     bool      `db:"is_markis"      json:"is_markis"`
	FirstSeenAt  time.Time `db:"first_seen_at"  json:"first_seen_at"`
	LastSyncedAt time.Time `db:"last_synced_at" json:"last_synced_at"`
}

// LeagueManager is one row per (league, roster slot).
type LeagueManager struct {
	LeagueID     string    `db:"league_id"      json:"league_id"`
	UserID       string    `db:"user_id"        json:"user_id"`
	RosterID     int       `db:"roster_id"      json:"roster_id"`
	TeamName     *string   `db:"team_name"      json:"team_name,omitempty"`
	CoOwner      bool      `db:"co_owner"       json:"co_owner"`
	IsMarkis     bool      `db:"is_markis"      json:"is_markis"`
	Wins         *int      `db:"wins"           json:"wins,omitempty"`
	Losses       *int      `db:"losses"         json:"losses,omitempty"`
	Ties         *int      `db:"ties"           json:"ties,omitempty"`
	Fpts         *float32  `db:"fpts"           json:"fpts,omitempty"`
	LastSyncedAt time.Time `db:"last_synced_at" json:"last_synced_at"`
}

// LeaguemateRosterPlayer is a player on a roster in a league.
type LeaguemateRosterPlayer struct {
	LeagueID        string    `db:"league_id"         json:"league_id"`
	RosterID        int       `db:"roster_id"         json:"roster_id"`
	SleeperPlayerID string    `db:"sleeper_player_id" json:"sleeper_player_id"`
	Slot            string    `db:"slot"              json:"slot"`
	SnapshotAt      time.Time `db:"snapshot_at"       json:"snapshot_at"`
}

// LeaguemateTransaction is a completed Sleeper trade.
type LeaguemateTransaction struct {
	TransactionID    string     `db:"transaction_id"     json:"transaction_id"`
	LeagueID         string     `db:"league_id"          json:"league_id"`
	Type             string     `db:"type"               json:"type"`
	Status           *string    `db:"status"             json:"status,omitempty"`
	Creator          *string    `db:"creator"            json:"creator,omitempty"`
	Week             *int       `db:"week"               json:"week,omitempty"`
	RosterIDs        []int      `db:"roster_ids"         json:"roster_ids"`
	ConsenterIDs     []int      `db:"consenter_ids"      json:"consenter_ids"`
	CreatedAt        *time.Time `db:"created_at"         json:"created_at,omitempty"`
	StatusUpdatedAt  *time.Time `db:"status_updated_at"  json:"status_updated_at,omitempty"`
	IsMarkisLeague   bool       `db:"is_markis_league"   json:"is_markis_league"`
	InvolvesWatchSet bool       `db:"involves_watch_set" json:"involves_watch_set"`
	Raw              []byte     `db:"raw"                json:"raw"`
	LastSyncedAt     time.Time  `db:"last_synced_at"     json:"last_synced_at"`
}

// LeaguemateTradeAsset is one row per moving player or pick in a trade.
type LeaguemateTradeAsset struct {
	ID              uuid.UUID `db:"id"                json:"id"`
	TransactionID   string    `db:"transaction_id"    json:"transaction_id"`
	LeagueID        string    `db:"league_id"         json:"league_id"`
	AssetType       string    `db:"asset_type"        json:"asset_type"`
	SleeperPlayerID *string   `db:"sleeper_player_id" json:"sleeper_player_id,omitempty"`
	PickSeason      *string   `db:"pick_season"       json:"pick_season,omitempty"`
	PickRound       *int      `db:"pick_round"        json:"pick_round,omitempty"`
	PickRosterID    *int      `db:"pick_roster_id"    json:"pick_roster_id,omitempty"`
	FromRosterID    *int      `db:"from_roster_id"    json:"from_roster_id,omitempty"`
	ToRosterID      *int      `db:"to_roster_id"      json:"to_roster_id,omitempty"`
	IsWatchSet      *bool     `db:"is_watch_set"      json:"is_watch_set,omitempty"`
}

// LeaguemateSignal is a weekly tendency dossier for a leaguemate.
type LeaguemateSignal struct {
	SnapshotDate   time.Time `db:"snapshot_date"   json:"snapshot_date"`
	UserID         string    `db:"user_id"         json:"user_id"`
	Username       *string   `db:"username"        json:"username,omitempty"`
	DisplayName    *string   `db:"display_name"    json:"display_name,omitempty"`
	LeaguesCount   *int      `db:"leagues_count"   json:"leagues_count,omitempty"`
	WinPct         *float32  `db:"win_pct"         json:"win_pct,omitempty"`
	AvgCoreAge     *float32  `db:"avg_core_age"    json:"avg_core_age,omitempty"`
	ContenderScore *int      `db:"contender_score" json:"contender_score,omitempty"`
	PositionBias   []byte    `db:"position_bias"   json:"position_bias"`
	TradeCount     *int      `db:"trade_count"     json:"trade_count,omitempty"`
	TradeCount30d  *int      `db:"trade_count_30d" json:"trade_count_30d,omitempty"`
	OverpayDelta   *float32  `db:"overpay_delta"   json:"overpay_delta,omitempty"`
	TradesValuated *int      `db:"trades_valuated" json:"trades_valuated,omitempty"`
	PicksAcquired  *int      `db:"picks_acquired"  json:"picks_acquired,omitempty"`
	PicksTraded    *int      `db:"picks_traded"    json:"picks_traded,omitempty"`
	NetFirsts      *int      `db:"net_firsts"      json:"net_firsts,omitempty"`
	Dossier        *string   `db:"dossier"         json:"dossier,omitempty"`
	RawSignals     []byte    `db:"raw_signals"     json:"raw_signals"`
	CreatedAt      time.Time `db:"created_at"      json:"created_at"`
}

// FPNoteCheck tracks when each FantasyPros player was last checked.
type FPNoteCheck struct {
	Slug          string    `db:"slug"            json:"slug"`
	PlayerName    *string   `db:"player_name"     json:"player_name,omitempty"`
	ECR           *int      `db:"ecr"             json:"ecr,omitempty"`
	LastCheckedAt time.Time `db:"last_checked_at" json:"last_checked_at"`
	HasNote       bool      `db:"has_note"        json:"has_note"`
}

// NFLState is the current Sleeper NFL state.
type NFLState struct {
	Week       int    `json:"week"`
	Season     string `json:"season"`
	SeasonType string `json:"season_type"`
	LeagueYear int    `json:"league_year"`
}
