package corpus

import "time"

// PlayerRow is the read view returned by Common.PlayerRows: the subset of the
// player table columns the corpus publisher needs. It mirrors the SELECT
// column order in PlayerRows exactly, so rows.Scan can target the fields
// directly. Pointer fields map NULL to nil; Active is NOT NULL in the table.
type PlayerRow struct {
	SleeperPlayerID    string
	FullName           *string
	FirstName          *string
	LastName           *string
	SearchFullName     *string
	Position           *string
	Team               *string
	TeamAbbr           *string
	Age                *int
	InjuryStatus       *string
	InjuryBodyPart     *string
	InjuryNotes        *string
	Status             *string
	Active             bool
	DepthChartPosition *string
	DepthChartOrder    *int
	LastSyncedAt       time.Time
}

// RankingRow is the read view returned by Common.RankingRows: the subset of
// player_ranking columns the corpus publisher needs, joined to player for the
// sleeper id. Field order matches the SELECT in RankingRows.
type RankingRow struct {
	SleeperPlayerID  string
	TradeValue       *int
	SfTradeValue     *int
	RedraftValue     *int
	OverallRank      *int
	PositionRank     *int
	SfOverallRank    *int
	SfPositionRank   *int
	AvgADP           *string
	LastMonthValue   *int
	LastMonthValueSF *int
	SnapshotDate     time.Time
	DataDate         *time.Time
}
