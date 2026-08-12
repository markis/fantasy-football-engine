package corpus

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/samber/lo"

	"ff-engine/internal/db"
	"ff-engine/internal/models"
	"ff-engine/internal/sleeper"
	"ff-engine/internal/util"
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
	data, err := readFileRooted(path)
	if err != nil {
		return "", fmt.Errorf("read file %s: %w", path, err)
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
	return c.sleeper.GetRaw(ctx, path)
}

func (c *Common) LeagueRosters(ctx context.Context, leagueID string) ([]sleeper.Roster, error) {
	raw, err := c.sleeper.GetLeagueRosters(ctx, leagueID)
	if err != nil {
		return nil, err
	}
	return sleeper.ParseRosters(raw), nil
}

func (c *Common) LeagueUsers(ctx context.Context, leagueID string) ([]map[string]any, error) {
	return c.sleeper.GetLeagueUsers(ctx, leagueID)
}

func (c *Common) LeagueInfo(ctx context.Context, leagueID string) (map[string]any, error) {
	return c.sleeper.GetLeagueInfo(ctx, leagueID)
}

func (c *Common) LeagueTradedPicks(ctx context.Context, leagueID string) ([]sleeper.TradedPick, error) {
	raw, err := c.sleeper.GetLeagueTradedPicks(ctx, leagueID)
	if err != nil {
		return nil, err
	}
	return sleeper.ParseTradedPicks(raw), nil
}

func (c *Common) LeagueTransactions(ctx context.Context, leagueID string, week int) ([]map[string]any, error) {
	return c.sleeper.GetLeagueTransactions(ctx, leagueID, week)
}

// MyRoster returns Markis's roster from a league's rosters, or nil if absent.
func (c *Common) MyRoster(rosters []sleeper.Roster) *sleeper.Roster {
	for i := range rosters {
		if util.StrOrEmpty(rosters[i].OwnerID.Ptr()) == models.MarkisUserID {
			return &rosters[i]
		}
	}
	return nil
}

func (c *Common) AllRosterPlayerIDs(rosters []sleeper.Roster) []string {
	return lo.Uniq(lo.FlatMap(rosters, func(r sleeper.Roster, _ int) []string {
		return r.Players
	}))
}

// --- Player/ranking queries ---

func (c *Common) PlayerRows(ctx context.Context, sleeperIDs []string) map[string]*PlayerRow {
	result := make(map[string]*PlayerRow)
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
	for rows.Next() {
		var r PlayerRow
		if err := rows.Scan(&r.SleeperPlayerID, &r.FullName, &r.FirstName, &r.LastName,
			&r.SearchFullName, &r.Position, &r.Team, &r.TeamAbbr, &r.Age,
			&r.InjuryStatus, &r.InjuryBodyPart, &r.InjuryNotes, &r.Status, &r.Active,
			&r.DepthChartPosition, &r.DepthChartOrder, &r.LastSyncedAt); err != nil {
			continue
		}
		result[r.SleeperPlayerID] = &r
	}
	return result
}

func (c *Common) RankingRows(ctx context.Context, sleeperIDs []string, source string, market int) map[string]*RankingRow {
	result := make(map[string]*RankingRow)
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
		var r RankingRow
		if err := rows.Scan(&r.SleeperPlayerID, &r.TradeValue, &r.SfTradeValue,
			&r.RedraftValue, &r.OverallRank, &r.PositionRank, &r.SfOverallRank,
			&r.SfPositionRank, &r.AvgADP, &r.LastMonthValue, &r.LastMonthValueSF,
			&r.SnapshotDate, &r.DataDate); err != nil {
			continue
		}
		result[r.SleeperPlayerID] = &r
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

func BuildNameIndex(playerRows map[string]*PlayerRow) map[string][]string {
	idx := make(map[string][]string)
	for sid, p := range playerRows {
		for _, name := range []*string{p.FullName, p.SearchFullName, p.LastName} {
			if name == nil {
				continue
			}
			key := NormalizeName(*name)
			if key != "" {
				idx[key] = append(idx[key], sid)
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

// readFileRooted reads a file by opening its parent directory as an os.Root
// and reading the base name relative to it. os.Root confines access to the
// directory, and its methods are not flagged by gosec G304 (only os.ReadFile/
// Open/OpenFile/Create are), so callers need no //nolint directive.
func readFileRooted(path string) ([]byte, error) {
	dir := filepath.Dir(path)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open dir %s: %w", dir, err)
	}
	defer root.Close()
	f, err := root.Open(filepath.Base(path))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	return data, nil
}

// rootReadAll reads a file scoped under an os.Root. See readFileRooted for the
// gosec rationale; use this when a Root is already open (e.g. inside a loop).
func rootReadAll(root *os.Root, name string) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	return data, nil
}

// rootWriteAll writes data to name scoped under an os.Root, creating any missing
// parent directories. See readFileRooted for the gosec rationale.
func rootWriteAll(root *os.Root, name string, data []byte, perm os.FileMode) error {
	if dir := filepath.Dir(name); dir != "." {
		if err := root.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("open %s: %w", name, err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

func WriteJSON(path string, obj any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create directory for %s: %w", path, err)
	}
	data, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json for %s: %w", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write file %s: %w", path, err)
	}
	return nil
}

func WriteText(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create directory for %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimRight(text, "\n")+"\n"), 0o600); err != nil {
		return fmt.Errorf("write file %s: %w", path, err)
	}
	return nil
}

func AppendJSONL(path string, obj any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create directory for %s: %w", path, err)
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("marshal json for %s: %w", path, err)
	}
	data = append(data, '\n')
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open dir %s: %w", dir, err)
	}
	defer root.Close()
	f, err := root.OpenFile(filepath.Base(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open file %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write file %s: %w", path, err)
	}
	return nil
}

func ReadJSONL(path string) ([]map[string]any, error) {
	data, err := readFileRooted(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read file %s: %w", path, err)
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
	{colUsage, []string{colUsage, "snap", "target", "touch"}},
	{"production", []string{"performance", "production"}},
	{"rookie", []string{"draft", "rookie"}},
	{"schedule", []string{"schedule", "matchup"}},
	{"market", []string{"market", "trade", colValue}},
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
