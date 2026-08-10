package corpus

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/markis/fantasy-football-engine/internal/db"
	"github.com/markis/fantasy-football-engine/internal/models"
	"github.com/markis/fantasy-football-engine/internal/sleeper"
)

// Common provides shared helpers for the corpus publisher.
type Common struct {
	pool           *db.Pool
	sleeper        *sleeper.Client
	corpus         string
	staging        string
	gitAuthorName  string
	gitAuthorEmail string
	gitPAT         string
}

// New creates a new Common helper.
func New(pool *db.Pool, sleeperClient *sleeper.Client, corpusDir string) *Common {
	return &Common{
		pool:    pool,
		sleeper: sleeperClient,
		corpus:  corpusDir,
		staging: filepath.Join(corpusDir, ".staging"),
	}
}

// SetGitIdentity sets the git author name, email, and PAT for commit/push.
func (c *Common) SetGitIdentity(name, email, pat string) {
	c.gitAuthorName = name
	c.gitAuthorEmail = email
	c.gitPAT = pat
}

// NowTime returns the current time.
func (c *Common) NowTime() time.Time {
	return time.Now()
}

func (c *Common) NowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

func (c *Common) StagingDir() string { return c.staging }
func (c *Common) CorpusDir() string  { return c.corpus }

// --- Hashing / stable IDs ---

//go:embed schemas/*.json
var schemaFS embed.FS

func SHA256(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

func EvidenceID(canonicalURL, contentHash string) string {
	return "sha256:" + SHA256(canonicalURL+"|"+contentHash)
}

func ClaimID(text string) string {
	return "claim:" + SHA256(text)[:16]
}

func SignalID(playerID, stype, value, observedAt string) string {
	return "signal:" + SHA256(playerID + "|" + stype + "|" + value + "|" + observedAt)[:16]
}

func ValuationID(playerID, source, vtype, fmtCtx, observedAt string) string {
	return "valuation:" + SHA256(playerID + "|" + source + "|" + vtype + "|" + fmtCtx + "|" + observedAt)[:16]
}

func ChangeID(ts, entityID, op string) string {
	return "change:" + SHA256(ts + "|" + entityID + "|" + op)[:16]
}

func ContentHash(text string) string {
	return "sha256:" + SHA256(text)
}

func FileSHA256Bytes(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is internal content directory path, not user input
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}

// --- Time helpers ---

func ParseDT(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	s = strings.Replace(s, "Z", "+00:00", 1)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t, true
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func ISOOrNone(s string) string {
	t, ok := ParseDT(s)
	if !ok {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

// --- Sleeper fetch (with cache) ---

func (c *Common) SleeperGet(ctx context.Context, path string) (any, error) {
	return c.sleeper.GetRaw(ctx, path) //nolint:wrapcheck // proxy method
}

func (c *Common) LeagueRosters(ctx context.Context, leagueID string) ([]map[string]any, error) {
	return c.sleeper.GetLeagueRosters(ctx, leagueID) //nolint:wrapcheck // proxy method
}

func (c *Common) LeagueUsers(ctx context.Context, leagueID string) ([]map[string]any, error) {
	return c.sleeper.GetLeagueUsers(ctx, leagueID) //nolint:wrapcheck // proxy method
}

func (c *Common) LeagueInfo(ctx context.Context, leagueID string) (map[string]any, error) {
	return c.sleeper.GetLeagueInfo(ctx, leagueID) //nolint:wrapcheck // proxy method
}

func (c *Common) LeagueTradedPicks(ctx context.Context, leagueID string) ([]map[string]any, error) {
	return c.sleeper.GetLeagueTradedPicks(ctx, leagueID) //nolint:wrapcheck // proxy method
}

func (c *Common) LeagueTransactions(ctx context.Context, leagueID string, week int) ([]map[string]any, error) {
	return c.sleeper.GetLeagueTransactions(ctx, leagueID, week) //nolint:wrapcheck // proxy method
}

func (c *Common) MyRoster(rosters []map[string]any) map[string]any {
	for _, r := range rosters {
		if fmt.Sprint(r["owner_id"]) == models.MarkisUserID {
			return r
		}
	}
	return nil
}

func (c *Common) AllRosterPlayerIDs(rosters []map[string]any) []string {
	ids := make(map[string]bool)
	for _, r := range rosters {
		for _, p := range toStringSlice(r["players"]) {
			if p != "" {
				ids[p] = true
			}
		}
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	return result
}

// --- Player/ranking queries ---

func (c *Common) PlayerRows(ctx context.Context, sleeperIDs []string) map[string]map[string]any {
	result := make(map[string]map[string]any)
	if len(sleeperIDs) == 0 {
		return result
	}
	rows, err := c.pool.Query(ctx, `
		SELECT sleeper_player_id, full_name, first_name, last_name, search_full_name,
		       position, team, team_abbr, age, injury_status, injury_body_part,
		       injury_notes, status, active, depth_chart_position, depth_chart_order,
		       last_synced_at
		FROM player WHERE sleeper_player_id = ANY($1)
	`, sleeperIDs)
	if err != nil {
		slog.Warn("player rows query", "err", err)
		return result
	}
	defer rows.Close()
	cols := []string{
		colSleeperPlayerID, colFullName, "first_name", "last_name", "search_full_name",
		colPosition, colTeam, "team_abbr", colAge, colInjuryStatus, "injury_body_part",
		"injury_notes", colStatus, "active", "depth_chart_position", "depth_chart_order",
		"last_synced_at",
	}
	for rows.Next() {
		vals := make(map[string]any)
		var sleeperID string
		var fullName, firstName, lastName, searchName, position, team, teamAbbr,
			injuryStatus, injuryBodyPart, injuryNotes, status, dcPos *string
		var age, dcOrder *int
		var active bool
		var lastSynced time.Time
		if err := rows.Scan(&sleeperID, &fullName, &firstName, &lastName, &searchName,
			&position, &team, &teamAbbr, &age, &injuryStatus, &injuryBodyPart,
			&injuryNotes, &status, &active, &dcPos, &dcOrder, &lastSynced); err != nil {
			continue
		}
		vals["sleeper_player_id"] = sleeperID
		vals["full_name"] = fullName
		vals["last_name"] = lastName
		vals["search_full_name"] = searchName
		vals["position"] = position
		vals["team"] = team
		vals["team_abbr"] = teamAbbr
		vals["age"] = age
		vals["injury_status"] = injuryStatus
		vals["injury_body_part"] = injuryBodyPart
		vals["injury_notes"] = injuryNotes
		vals["status"] = status
		vals["active"] = active
		vals["depth_chart_position"] = dcPos
		vals["depth_chart_order"] = dcOrder
		vals["last_synced_at"] = lastSynced
		_ = cols
		result[sleeperID] = vals
	}
	return result
}

func (c *Common) RankingRows(ctx context.Context, sleeperIDs []string, source string, market int) map[string]map[string]any {
	result := make(map[string]map[string]any)
	if len(sleeperIDs) == 0 {
		return result
	}
	rows, err := c.pool.Query(ctx, `
		SELECT p.sleeper_player_id, r.trade_value, r.sf_trade_value, r.redraft_value,
		       r.overall_rank, r.position_rank, r.sf_overall_rank, r.sf_position_rank,
		       r.avg_adp, r.last_month_value, r.last_month_value_sf,
		       r.snapshot_date, r.data_date
		FROM player p JOIN player_ranking r ON r.player_id = p.id
		WHERE r.source = $1 AND r.market = $2 AND p.sleeper_player_id = ANY($3)
	`, source, market, sleeperIDs)
	if err != nil {
		slog.Warn("ranking rows query", "err", err)
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var sleeperID string
		var tradeValue, sfTradeValue, redraftValue, overallRank, positionRank,
			sfOverallRank, sfPositionRank, lastMonthValue, lastMonthValueSF *int
		var avgADP *string
		var snapshotDate time.Time
		var dataDate *time.Time
		if err := rows.Scan(&sleeperID, &tradeValue, &sfTradeValue, &redraftValue,
			&overallRank, &positionRank, &sfOverallRank, &sfPositionRank,
			&avgADP, &lastMonthValue, &lastMonthValueSF, &snapshotDate, &dataDate); err != nil {
			continue
		}
		vals := map[string]any{
			"sleeper_player_id":   sleeperID,
			"trade_value":         tradeValue,
			"sf_trade_value":      sfTradeValue,
			"redraft_value":       redraftValue,
			"overall_rank":        overallRank,
			"position_rank":       positionRank,
			"sf_overall_rank":     sfOverallRank,
			"sf_position_rank":    sfPositionRank,
			"avg_adp":             avgADP,
			"last_month_value":    lastMonthValue,
			"last_month_value_sf": lastMonthValueSF,
			"snapshot_date":       snapshotDate,
			"data_date":           dataDate,
		}
		result[sleeperID] = vals
	}
	return result
}

// --- Name matching ---

var (
	nameNormalizer = regexp.MustCompile(`[^a-z0-9 ]`)
	suffixRe       = regexp.MustCompile(`\s+(jr|sr|ii|iii|iv)\b`)
	multiSpaceRe   = regexp.MustCompile(`\s+`)
)

func NormalizeName(n string) string {
	n = strings.ToLower(n)
	n = nameNormalizer.ReplaceAllString(n, "")
	n = suffixRe.ReplaceAllString(n, "")
	n = multiSpaceRe.ReplaceAllString(n, " ")
	return strings.TrimSpace(n)
}

func BuildNameIndex(playerRows map[string]map[string]any) map[string][]string {
	idx := make(map[string][]string)
	for sid, p := range playerRows {
		for _, k := range []string{"full_name", "search_full_name", "last_name"} {
			if v, ok := p[k].(*string); ok && v != nil {
				key := NormalizeName(*v)
				if key != "" {
					idx[key] = append(idx[key], sid)
				}
			}
		}
	}
	return idx
}

func MatchEntitiesToPlayers(entities []string, nameIndex map[string][]string) []string {
	hits := make(map[string]bool)
	for _, e := range entities {
		key := NormalizeName(e)
		if len(key) < 3 {
			continue
		}
		if sids, ok := nameIndex[key]; ok {
			for _, sid := range sids {
				hits[sid] = true
			}
		}
	}
	result := make([]string, 0, len(hits))
	for sid := range hits {
		result = append(result, sid)
	}
	return result
}

// --- File IO ---

func WriteJSON(path string, obj any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func WriteText(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.TrimRight(text, "\n")+"\n"), 0o600)
}

func AppendJSONL(path string, obj any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // path is content directory path, not user input
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err //nolint:wrapcheck // propagate stdlib error
}

func ReadJSONL(path string) ([]map[string]any, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is internal storage path, not user input
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var result []map[string]any
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err == nil {
			result = append(result, obj)
		}
	}
	return result, nil
}

// --- Topic mapping ---

// topicRules maps a topic result to the set of keywords that trigger it.
// Order matters: the first rule with a matching keyword wins.
var topicRules = []struct {
	result   string
	keywords []string
}{
	{colInjury, []string{colInjury}},
	{"transaction", []string{"transaction"}},
	{"depth-chart", []string{"depth chart"}},
	{"usage", []string{"usage", "snap", "target", "touch"}},
	{"production", []string{"performance", "production"}},
	{"rookie", []string{"draft", "rookie"}},
	{"schedule", []string{"schedule", "matchup"}},
	{"market", []string{"market", "trade", "value"}},
}

func TopicFromTopics(topics []string) string {
	tset := make(map[string]bool)
	for _, t := range topics {
		tset[strings.ToLower(t)] = true
	}
	for _, rule := range topicRules {
		if topicSetHasAny(tset, rule.keywords) {
			return rule.result
		}
	}
	return "other"
}

func topicSetHasAny(tset map[string]bool, keywords []string) bool {
	for _, k := range keywords {
		if tset[k] {
			return true
		}
	}
	return false
}

// --- Helper ---

func toStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		s := fmt.Sprint(item)
		if s != "" && s != nilStr {
			result = append(result, s)
		}
	}
	return result
}
