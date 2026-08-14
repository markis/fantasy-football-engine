// Package models defines shared domain types, including the mapping of
package models

import (
	"time"

	"github.com/google/uuid"
)

// --- Feed / news domain ---

// Source represents an RSS feed or API source.
type Source struct {
	ID              uuid.UUID  `db:"id"                json:"id"`
	Name            string     `db:"name"              json:"name"`
	Type            string     `db:"type"              json:"type"`
	URL             string     `db:"url"               json:"url"`
	FetchMethod     string     `db:"fetch_method"      json:"fetchMethod"`
	PollIntervalSec int        `db:"poll_interval_sec" json:"pollIntervalSec"`
	IsActive        bool       `db:"is_active"         json:"isActive"`
	ETag            *string    `db:"etag"              json:"eTag,omitempty"`
	LastModified    *string    `db:"last_modified"     json:"lastModified,omitempty"`
	LastPollAt      *time.Time `db:"last_poll_at"      json:"lastPollAt,omitempty"`
	NextPollAt      *time.Time `db:"next_poll_at"      json:"nextPollAt,omitempty"`
	CreatedAt       time.Time  `db:"created_at"        json:"createdAt"`
	UpdatedAt       time.Time  `db:"updated_at"        json:"updatedAt"`
}

// RawDocument is an immutable fetched feed payload.
type RawDocument struct {
	ID          uuid.UUID `db:"id"           json:"id"`
	SourceID    uuid.UUID `db:"source_id"    json:"sourceId"`
	URL         string    `db:"url"          json:"url"`
	FetchStatus int       `db:"fetch_status" json:"fetchStatus"`
	Headers     []byte    `db:"headers"      json:"headers"`
	BodyText    *string   `db:"body_text"    json:"bodyText,omitempty"`
	ContentType *string   `db:"content_type" json:"contentType,omitempty"`
	FetchedAt   time.Time `db:"fetched_at"   json:"fetchedAt"`
	CreatedAt   time.Time `db:"created_at"   json:"createdAt"`
}

// NewsItem is an RSS article or API news entry.
type NewsItem struct {
	ID                   uuid.UUID  `db:"id"                      json:"id"`
	SourceID             uuid.UUID  `db:"source_id"               json:"sourceId"`
	SourceType           string     `db:"source_type"             json:"sourceType"`
	ExternalID           *string    `db:"external_id"             json:"externalId,omitempty"`
	RawDocumentID        *uuid.UUID `db:"raw_document_id"         json:"rawDocumentId,omitempty"`
	URL                  *string    `db:"url"                     json:"url,omitempty"`
	CanonicalURL         *string    `db:"canonical_url"           json:"canonicalUrl,omitempty"`
	CanonicalURLHash     *string    `db:"canonical_url_hash"      json:"canonicalUrlHash,omitempty"`
	Title                *string    `db:"title"                   json:"title,omitempty"`
	Author               *string    `db:"author"                  json:"author,omitempty"`
	PublishedAt          *time.Time `db:"published_at"            json:"publishedAt,omitempty"`
	ContentHTML          *string    `db:"content_html"            json:"contentHtml,omitempty"`
	ContentText          *string    `db:"content_text"            json:"contentText,omitempty"`
	ContentMarkdown      *string    `db:"content_markdown"        json:"contentMarkdown,omitempty"`
	SummaryShort         *string    `db:"summary_short"           json:"summaryShort,omitempty"`
	ContentHash          *string    `db:"content_hash"            json:"contentHash,omitempty"`
	Simhash              *int64     `db:"simhash"                 json:"simhash,omitempty"`
	FetchedAt            *time.Time `db:"fetched_at"              json:"fetchedAt,omitempty"`
	BodyFetchStatus      string     `db:"body_fetch_status"       json:"bodyFetchStatus"`
	BodyFetchAttempts    int        `db:"body_fetch_attempts"     json:"bodyFetchAttempts"`
	BodyFetchedAt        *time.Time `db:"body_fetched_at"         json:"bodyFetchedAt,omitempty"`
	IsRelevant           bool       `db:"is_relevant"             json:"isRelevant"`
	IsNews               bool       `db:"is_news"                 json:"isNews"`
	NewsStory            *string    `db:"news_story"              json:"newsStory,omitempty"`
	NewsStoryGeneratedAt *time.Time `db:"news_story_generated_at" json:"newsStoryGeneratedAt,omitempty"`
	NewsStoryModel       *string    `db:"news_story_model"        json:"newsStoryModel,omitempty"`
	Embedding            []float32  `db:"-"                       json:"-"`
	Language             *string    `db:"language"                json:"language,omitempty"`
	Topics               []string   `db:"topics"                  json:"topics"`
	Entities             []string   `db:"entities"                json:"entities"`
	QualityScore         *float64   `db:"quality_score"           json:"qualityScore,omitempty"`
	ClusterID            *uuid.UUID `db:"cluster_id"              json:"clusterId,omitempty"`
	CreatedAt            time.Time  `db:"created_at"              json:"createdAt"`
	UpdatedAt            time.Time  `db:"updated_at"              json:"updatedAt"`
}

// StoryCluster groups related news items.
type StoryCluster struct {
	ID                  uuid.UUID   `db:"id"                   json:"id"`
	ClusterKey          *string     `db:"cluster_key"          json:"clusterKey,omitempty"`
	RepresentativeTitle *string     `db:"representative_title" json:"representativeTitle,omitempty"`
	ItemIDs             []uuid.UUID `db:"item_ids"             json:"itemIDs"`
	ImportanceScore     *float64    `db:"importance_score"     json:"importanceScore,omitempty"`
	FirstSeenAt         time.Time   `db:"first_seen_at"        json:"firstSeenAt"`
	LastSeenAt          time.Time   `db:"last_seen_at"         json:"lastSeenAt"`
	CreatedAt           time.Time   `db:"created_at"           json:"createdAt"`
	UpdatedAt           time.Time   `db:"updated_at"           json:"updatedAt"`
}

// Fact is an atomic fantasy football fact extracted from a news item.
type Fact struct {
	ID          uuid.UUID `db:"id"           json:"id"`
	NewsItemID  uuid.UUID `db:"news_item_id" json:"newsItemId"`
	FactText    string    `db:"fact_text"    json:"factText"`
	Entities    []string  `db:"entities"     json:"entities"`
	Topics      []string  `db:"topics"       json:"topics"`
	OccurredAt  time.Time `db:"occurred_at"  json:"occurredAt"`
	Confidence  *string   `db:"confidence"   json:"confidence,omitempty"`
	Embedding   []float32 `db:"-"            json:"-"`
	ExtractedAt time.Time `db:"extracted_at" json:"extractedAt"`
	CreatedAt   time.Time `db:"created_at"   json:"createdAt"`
}

// Player is a Sleeper NFL player.
type Player struct {
	ID                    uuid.UUID  `db:"id"                     json:"id"`
	SleeperPlayerID       string     `db:"sleeper_player_id"      json:"sleeperPlayerId"`
	FirstName             *string    `db:"first_name"             json:"firstName,omitempty"`
	LastName              *string    `db:"last_name"              json:"lastName,omitempty"`
	FullName              *string    `db:"full_name"              json:"fullName,omitempty"`
	SearchFullName        *string    `db:"search_full_name"       json:"searchFullName,omitempty"`
	Position              *string    `db:"position"               json:"position,omitempty"`
	FantasyPositions      []string   `db:"fantasy_positions"      json:"fantasyPositions"`
	Team                  *string    `db:"team"                   json:"team,omitempty"`
	TeamAbbr              *string    `db:"team_abbr"              json:"teamAbbr,omitempty"`
	Status                *string    `db:"status"                 json:"status,omitempty"`
	Active                bool       `db:"active"                 json:"active"`
	InjuryStatus          *string    `db:"injury_status"          json:"injuryStatus,omitempty"`
	InjuryBodyPart        *string    `db:"injury_body_part"       json:"injuryBodyPart,omitempty"`
	InjuryNotes           *string    `db:"injury_notes"           json:"injuryNotes,omitempty"`
	InjuryStartDate       *time.Time `db:"injury_start_date"      json:"injuryStartDate,omitempty"`
	Age                   *int       `db:"age"                    json:"age,omitempty"`
	YearsExp              *int       `db:"years_exp"              json:"yearsExp,omitempty"`
	BirthDate             *time.Time `db:"birth_date"             json:"birthDate,omitempty"`
	Height                *string    `db:"height"                 json:"height,omitempty"`
	Weight                *string    `db:"weight"                 json:"weight,omitempty"`
	College               *string    `db:"college"                json:"college,omitempty"`
	Number                *int       `db:"number"                 json:"number,omitempty"`
	DepthChartPosition    *string    `db:"depth_chart_position"   json:"depthChartPosition,omitempty"`
	DepthChartOrder       *int       `db:"depth_chart_order"      json:"depthChartOrder,omitempty"`
	PracticeParticipation *string    `db:"practice_participation" json:"practiceParticipation,omitempty"`
	PracticeDescription   *string    `db:"practice_description"   json:"practiceDescription,omitempty"`
	GsisID                *string    `db:"gsis_id"                json:"gsisId,omitempty"`
	EspnID                *string    `db:"espn_id"                json:"espnId,omitempty"`
	RotowireID            *string    `db:"rotowire_id"            json:"rotowireId,omitempty"`
	RotoworldID           *string    `db:"rotoworld_id"           json:"rotoworldId,omitempty"`
	YahooID               *string    `db:"yahoo_id"               json:"yahooId,omitempty"`
	SportradarID          *string    `db:"sportradar_id"          json:"sportradarId,omitempty"`
	StatsID               *string    `db:"stats_id"               json:"statsId,omitempty"`
	NewsUpdated           *time.Time `db:"news_updated"           json:"newsUpdated,omitempty"`
	LastSyncedAt          time.Time  `db:"last_synced_at"         json:"lastSyncedAt"`
	CreatedAt             time.Time  `db:"created_at"             json:"createdAt"`
	UpdatedAt             time.Time  `db:"updated_at"             json:"updatedAt"`
}

// PlayerRanking is a dynasty trade value / ranking snapshot.
type PlayerRanking struct {
	ID               uuid.UUID  `db:"id"                  json:"id"`
	PlayerID         uuid.UUID  `db:"player_id"           json:"playerId"`
	Source           string     `db:"source"              json:"source"`
	Market           int        `db:"market"              json:"market"`
	NameID           *string    `db:"name_id"             json:"nameId,omitempty"`
	Position         *string    `db:"position"            json:"position,omitempty"`
	Team             *string    `db:"team"                json:"team,omitempty"`
	OverallRank      *int       `db:"overall_rank"        json:"overallRank,omitempty"`
	PositionRank     *int       `db:"position_rank"       json:"positionRank,omitempty"`
	SfOverallRank    *int       `db:"sf_overall_rank"     json:"sfOverallRank,omitempty"`
	SfPositionRank   *int       `db:"sf_position_rank"    json:"sfPositionRank,omitempty"`
	TradeValue       *int       `db:"trade_value"         json:"tradeValue,omitempty"`
	SfTradeValue     *int       `db:"sf_trade_value"      json:"sfTradeValue,omitempty"`
	RedraftValue     *int       `db:"redraft_value"       json:"redraftValue,omitempty"`
	AllTimeHigh      *int       `db:"all_time_high"       json:"allTimeHigh,omitempty"`
	AllTimeLow       *int       `db:"all_time_low"        json:"allTimeLow,omitempty"`
	LastMonthValue   *int       `db:"last_month_value"    json:"lastMonthValue,omitempty"`
	LastMonthValueSF *int       `db:"last_month_value_sf" json:"lastMonthValueSf,omitempty"`
	LastMonthRank    *int       `db:"last_month_rank"     json:"lastMonthRank,omitempty"`
	LastMonthRankSF  *int       `db:"last_month_rank_sf"  json:"lastMonthRankSf,omitempty"`
	AvgADP           *string    `db:"avg_adp"             json:"avgAdp,omitempty"`
	PercentOwned     *string    `db:"percent_owned"       json:"percentOwned,omitempty"`
	PercentStarted   *string    `db:"percent_started"     json:"percentStarted,omitempty"`
	SnapshotDate     time.Time  `db:"snapshot_date"       json:"snapshotDate"`
	DataDate         *time.Time `db:"data_date"           json:"dataDate,omitempty"`
	CreatedAt        time.Time  `db:"created_at"          json:"createdAt"`
	UpdatedAt        time.Time  `db:"updated_at"          json:"updatedAt"`
}

// PlayerRankingHistory is an append-only daily ranking snapshot.
type PlayerRankingHistory struct {
	PlayerID     uuid.UUID `db:"player_id"     json:"playerId"`
	Source       string    `db:"source"        json:"source"`
	Market       int       `db:"market"        json:"market"`
	SnapshotDate time.Time `db:"snapshot_date" json:"snapshotDate"`
	TradeValue   *int      `db:"trade_value"   json:"tradeValue,omitempty"`
	OverallRank  *int      `db:"overall_rank"  json:"overallRank,omitempty"`
	PositionRank *int      `db:"position_rank" json:"positionRank,omitempty"`
	RedraftValue *int      `db:"redraft_value" json:"redraftValue,omitempty"`
	CreatedAt    time.Time `db:"created_at"    json:"createdAt"`
}

// League is a Sleeper league (Markis's or discovered leaguemate league).
type League struct {
	LeagueID            string    `db:"league_id"              json:"leagueId"`
	Name                *string   `db:"name"                   json:"name,omitempty"`
	Season              *string   `db:"season"                 json:"season,omitempty"`
	Sport               string    `db:"sport"                  json:"sport"`
	Status              *string   `db:"status"                 json:"status,omitempty"`
	NumTeams            *int      `db:"num_teams"              json:"numTeams,omitempty"`
	HasSuperflex        *bool     `db:"has_superflex"          json:"hasSuperflex,omitempty"`
	IsBestBall          *bool     `db:"is_best_ball"           json:"isBestBall,omitempty"`
	LeagueType          *int      `db:"league_type"            json:"leagueType,omitempty"`
	RosterPositions     []byte    `db:"roster_positions"       json:"rosterPositions"`
	Settings            []byte    `db:"settings"               json:"settings"`
	PreviousLeagueID    *string   `db:"previous_league_id"     json:"previousLeagueId,omitempty"`
	IsMarkisLeague      bool      `db:"is_markis_league"       json:"isMarkisLeague"`
	DiscoveredViaUserID *string   `db:"discovered_via_user_id" json:"discoveredViaUserId,omitempty"`
	FirstSeenAt         time.Time `db:"first_seen_at"          json:"firstSeenAt"`
	LastSyncedAt        time.Time `db:"last_synced_at"         json:"lastSyncedAt"`
}

// SleeperUser is a Sleeper user identity.
type SleeperUser struct {
	UserID       string    `db:"user_id"        json:"userId"`
	Username     *string   `db:"username"       json:"username,omitempty"`
	DisplayName  *string   `db:"display_name"   json:"displayName,omitempty"`
	Avatar       *string   `db:"avatar"         json:"avatar,omitempty"`
	IsMarkis     bool      `db:"is_markis"      json:"isMarkis"`
	FirstSeenAt  time.Time `db:"first_seen_at"  json:"firstSeenAt"`
	LastSyncedAt time.Time `db:"last_synced_at" json:"lastSyncedAt"`
}

// LeagueManager is one row per (league, roster slot).
type LeagueManager struct {
	LeagueID     string    `db:"league_id"      json:"leagueId"`
	UserID       string    `db:"user_id"        json:"userId"`
	RosterID     int       `db:"roster_id"      json:"rosterId"`
	TeamName     *string   `db:"team_name"      json:"teamName,omitempty"`
	CoOwner      bool      `db:"co_owner"       json:"coOwner"`
	IsMarkis     bool      `db:"is_markis"      json:"isMarkis"`
	Wins         *int      `db:"wins"           json:"wins,omitempty"`
	Losses       *int      `db:"losses"         json:"losses,omitempty"`
	Ties         *int      `db:"ties"           json:"ties,omitempty"`
	Fpts         *float32  `db:"fpts"           json:"fpts,omitempty"`
	LastSyncedAt time.Time `db:"last_synced_at" json:"lastSyncedAt"`
}

// LeaguemateRosterPlayer is a player on a roster in a league.
type LeaguemateRosterPlayer struct {
	LeagueID        string    `db:"league_id"         json:"leagueId"`
	RosterID        int       `db:"roster_id"         json:"rosterId"`
	SleeperPlayerID string    `db:"sleeper_player_id" json:"sleeperPlayerId"`
	Slot            string    `db:"slot"              json:"slot"`
	SnapshotAt      time.Time `db:"snapshot_at"       json:"snapshotAt"`
}

// LeaguemateTransaction is a completed Sleeper trade.
type LeaguemateTransaction struct {
	TransactionID    string     `db:"transaction_id"     json:"transactionId"`
	LeagueID         string     `db:"league_id"          json:"leagueId"`
	Type             string     `db:"type"               json:"type"`
	Status           *string    `db:"status"             json:"status,omitempty"`
	Creator          *string    `db:"creator"            json:"creator,omitempty"`
	Week             *int       `db:"week"               json:"week,omitempty"`
	RosterIDs        []int      `db:"roster_ids"         json:"rosterIDs"`
	ConsenterIDs     []int      `db:"consenter_ids"      json:"consenterIDs"`
	CreatedAt        *time.Time `db:"created_at"         json:"createdAt,omitempty"`
	StatusUpdatedAt  *time.Time `db:"status_updated_at"  json:"statusUpdatedAt,omitempty"`
	IsMarkisLeague   bool       `db:"is_markis_league"   json:"isMarkisLeague"`
	InvolvesWatchSet bool       `db:"involves_watch_set" json:"involvesWatchSet"`
	Raw              []byte     `db:"raw"                json:"raw"`
	LastSyncedAt     time.Time  `db:"last_synced_at"     json:"lastSyncedAt"`
}

// LeaguemateTradeAsset is one row per moving player or pick in a trade.
type LeaguemateTradeAsset struct {
	ID              uuid.UUID `db:"id"                json:"id"`
	TransactionID   string    `db:"transaction_id"    json:"transactionId"`
	LeagueID        string    `db:"league_id"         json:"leagueId"`
	AssetType       string    `db:"asset_type"        json:"assetType"`
	SleeperPlayerID *string   `db:"sleeper_player_id" json:"sleeperPlayerId,omitempty"`
	PickSeason      *string   `db:"pick_season"       json:"pickSeason,omitempty"`
	PickRound       *int      `db:"pick_round"        json:"pickRound,omitempty"`
	PickRosterID    *int      `db:"pick_roster_id"    json:"pickRosterId,omitempty"`
	FromRosterID    *int      `db:"from_roster_id"    json:"fromRosterId,omitempty"`
	ToRosterID      *int      `db:"to_roster_id"      json:"toRosterId,omitempty"`
	IsWatchSet      *bool     `db:"is_watch_set"      json:"isWatchSet,omitempty"`
}

// LeaguemateSignal is a weekly tendency dossier for a leaguemate.
type LeaguemateSignal struct {
	SnapshotDate   time.Time `db:"snapshot_date"   json:"snapshotDate"`
	UserID         string    `db:"user_id"         json:"userId"`
	Username       *string   `db:"username"        json:"username,omitempty"`
	DisplayName    *string   `db:"display_name"    json:"displayName,omitempty"`
	LeaguesCount   *int      `db:"leagues_count"   json:"leaguesCount,omitempty"`
	WinPct         *float32  `db:"win_pct"         json:"winPct,omitempty"`
	AvgCoreAge     *float32  `db:"avg_core_age"    json:"avgCoreAge,omitempty"`
	ContenderScore *int      `db:"contender_score" json:"contenderScore,omitempty"`
	PositionBias   []byte    `db:"position_bias"   json:"positionBias"`
	TradeCount     *int      `db:"trade_count"     json:"tradeCount,omitempty"`
	TradeCount30d  *int      `db:"trade_count_30d" json:"tradeCount30d,omitempty"`
	OverpayDelta   *float32  `db:"overpay_delta"   json:"overpayDelta,omitempty"`
	TradesValuated *int      `db:"trades_valuated" json:"tradesValuated,omitempty"`
	PicksAcquired  *int      `db:"picks_acquired"  json:"picksAcquired,omitempty"`
	PicksTraded    *int      `db:"picks_traded"    json:"picksTraded,omitempty"`
	NetFirsts      *int      `db:"net_firsts"      json:"netFirsts,omitempty"`
	Dossier        *string   `db:"dossier"         json:"dossier,omitempty"`
	RawSignals     []byte    `db:"raw_signals"     json:"rawSignals"`
	CreatedAt      time.Time `db:"created_at"      json:"createdAt"`
}

// FPNoteCheck tracks when each FantasyPros player was last checked.
type FPNoteCheck struct {
	Slug          string    `db:"slug"            json:"slug"`
	PlayerName    *string   `db:"player_name"     json:"playerName,omitempty"`
	ECR           *int      `db:"ecr"             json:"ecr,omitempty"`
	LastCheckedAt time.Time `db:"last_checked_at" json:"lastCheckedAt"`
	HasNote       bool      `db:"has_note"        json:"hasNote"`
}

// NFLState is the current Sleeper NFL state.
type NFLState struct {
	Week       int    `json:"week"`
	Season     string `json:"season"`
	SeasonType string `json:"seasonType"`
	LeagueYear int    `json:"leagueYear"`
}

// --- League format / FantasyCalc market mapping ---

const (
	tepPPPlus = "te++"
	typeType  = "dynasty"
	typeNone  = "none"
)

// LeagueFormat describes a Sleeper league's scoring format and its
// FantasyCalc market mapping.
type LeagueFormat struct {
	Name   string `json:"name"`
	Teams  int    `json:"teams"`
	NumQbs int    `json:"numQbs"`
	PPR    int    `json:"ppr"`
	TEP    string `json:"tep"`
	Market *int   `json:"market"`
	Type   string `json:"type"`
}

// LeagueFormats is the authoritative mapping of Markis's Sleeper league IDs
// to their scoring formats and FantasyCalc market IDs.
var LeagueFormats = map[string]LeagueFormat{
	"1343410990741479425": {Name: "Poor Life Choices", Teams: 18, NumQbs: 2, PPR: 1, TEP: tepPPPlus, Market: nil, Type: "redraft"},
	"1312178509908545536": {Name: "Backyard Brawl", Teams: 12, NumQbs: 1, PPR: 1, TEP: typeNone, Market: new(1), Type: typeType},
	"1312183009700499456": {Name: "Mama Says Foosball", Teams: 12, NumQbs: 2, PPR: 1, TEP: tepPPPlus, Market: new(2), Type: typeType},
	"1312170789964898304": {Name: "Deja Vu Dynasty", Teams: 12, NumQbs: 2, PPR: 1, TEP: tepPPPlus, Market: new(2), Type: typeType},
	"1312051514470055936": {Name: "Dave is the best", Teams: 10, NumQbs: 2, PPR: 0, TEP: typeNone, Market: new(3), Type: typeType},
}

// FormatCombo is a distinct dynasty format combo synced from FantasyCalc.
type FormatCombo struct {
	Market int    `json:"market"`
	Teams  int    `json:"teams"`
	NumQbs int    `json:"numQbs"`
	PPR    int    `json:"ppr"`
	TEP    string `json:"tep"`
	Label  string `json:"label"`
}

// FormatCombos are the distinct dynasty format combos to sync.
var FormatCombos = []FormatCombo{
	{Market: 1, Teams: 12, NumQbs: 1, PPR: 1, TEP: typeNone, Label: "12t-1QB-PPR-none"},
	{Market: 2, Teams: 12, NumQbs: 2, PPR: 1, TEP: tepPPPlus, Label: "12t-SF-PPR-TEP1.0"},
	{Market: 3, Teams: 10, NumQbs: 2, PPR: 0, TEP: typeNone, Label: "10t-SF-HalfPPR-none"},
}

// MarketLabel maps a FantasyCalc market ID to its label.
var MarketLabel = map[int]string{
	1: "12t-1QB-PPR-none",
	2: "12t-SF-PPR-TEP1.0",
	3: "10t-SF-HalfPPR-none",
}

// MarkisUserID is Markis's Sleeper user ID.
const MarkisUserID = "558115100726579200"

// PickValue constants — 1QB dynasty pick values.
var (
	Round1       = map[int]int{1: 9000, 2: 8200, 3: 7600, 4: 6800, 5: 6200, 6: 5600, 7: 5000, 8: 4500, 9: 4000, 10: 3600, 11: 3200, 12: 2900}
	Round2       = map[int]int{1: 2600, 2: 2300, 3: 2100, 4: 1900, 5: 1700, 6: 1500, 7: 1400, 8: 1300, 9: 1200, 10: 1100, 11: 1000, 12: 950}
	RoundDefault = map[int]int{1: 3400, 2: 2000, 3: 600, 4: 250}
	Tranche      = map[string]map[int]int{
		"early": {1: 8200, 2: 2300},
		"mid":   {1: 6200, 2: 1700},
		"late":  {1: 3400, 2: 1100},
	}
)

// AgeBands are age-retention factors per position (from DYNASTY_PLAYBOOK.md).
var AgeBands = map[string][]AgeBand{
	"RB": {{25, 1.0}, {26, 0.85}, {27, 0.70}, {28, 0.50}, {29, 0.30}, {200, 0.15}},
	"WR": {{27, 1.0}, {28, 0.90}, {29, 0.75}, {30, 0.60}, {31, 0.40}, {200, 0.20}},
	"TE": {{28, 1.0}, {29, 0.90}, {30, 0.80}, {31, 0.65}, {32, 0.50}, {200, 0.30}},
	"QB": {{30, 1.0}, {31, 0.90}, {32, 0.80}, {33, 0.70}, {34, 0.55}, {200, 0.40}},
}

type AgeBand struct {
	MaxAge int
	Factor float64
}

// AgeFactor returns the age-retention factor for a position/age.
func AgeFactor(pos string, age *int) float64 {
	if age == nil {
		return 1.0
	}
	bands, ok := AgeBands[pos]
	if !ok {
		return 1.0
	}
	for _, b := range bands {
		if *age <= b.MaxAge {
			return b.Factor
		}
	}
	return bands[len(bands)-1].Factor
}

// --- NFL teams ---

// NFLTeams maps a team's standard abbreviation to its full name.
var NFLTeams = map[string]string{
	"ARI": "Arizona Cardinals", "ATL": "Atlanta Falcons", "BAL": "Baltimore Ravens",
	"BUF": "Buffalo Bills", "CAR": "Carolina Panthers", "CHI": "Chicago Bears",
	"CIN": "Cincinnati Bengals", "CLE": "Cleveland Browns", "DAL": "Dallas Cowboys",
	"DEN": "Denver Broncos", "DET": "Detroit Lions", "GB": "Green Bay Packers",
	"HOU": "Houston Texans", "IND": "Indianapolis Colts", "JAX": "Jacksonville Jaguars",
	"KC": "Kansas City Chiefs", "LV": "Las Vegas Raiders", "LAC": "Los Angeles Chargers",
	"LAR": "Los Angeles Rams", "MIA": "Miami Dolphins", "MIN": "Minnesota Vikings",
	"NE": "New England Patriots", "NO": "New Orleans Saints", "NYG": "New York Giants",
	"NYJ": "New York Jets", "PHI": "Philadelphia Eagles", "PIT": "Pittsburgh Steelers",
	"SF": "San Francisco 49ers", "SEA": "Seattle Seahawks", "TB": "Tampa Bay Buccaneers",
	"TEN": "Tennessee Titans", "WAS": "Washington Commanders",
}

var nflTeamAbbrByName = func() map[string]string {
	m := make(map[string]string, len(NFLTeams))
	for abbr, name := range NFLTeams {
		m[name] = abbr
	}
	return m
}()

// TeamAbbrForName returns the abbreviation for a full NFL team name (e.g.
// "Philadelphia Eagles" -> "PHI"), and whether it matched.
func TeamAbbrForName(name string) (string, bool) {
	abbr, ok := nflTeamAbbrByName[name]
	return abbr, ok
}
