package corpus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"ff-engine/internal/models"
	"ff-engine/internal/sleeper"
	"ff-engine/internal/util"
)

// intPtrStr returns the decimal string of i, or "" if i is nil. Mirrors the
// *int handling of getStr for fields emitted as strings in players.jsonl.
func intPtrStr(i *int) string {
	if i == nil {
		return ""
	}
	return strconv.Itoa(*i)
}

// dflt returns s, or an empty non-nil slice if s is nil, so nil slices
// marshal as JSON [] rather than null (the schema requires arrays).
func dflt[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// renderTeam renders team/ (team-state, roster, picks, settings, transactions).
func (p *Publisher) renderTeam(ctx context.Context, targetDir string) (map[string]any, error) {
	teamDir := filepath.Join(targetDir, "team")
	if err := os.MkdirAll(teamDir, 0o750); err != nil {
		return nil, fmt.Errorf("create directory %s: %w", teamDir, err)
	}
	leaguesDir := filepath.Join(teamDir, "leagues")
	if err := os.MkdirAll(leaguesDir, 0o750); err != nil {
		slog.Warn("failed to create leagues directory", "err", err)
	}

	now := p.common.NowISO()
	var leagues []TeamState

	for leagueID, lf := range models.LeagueFormats {
		leagueState, ok := p.buildLeagueState(ctx, leagueID, &lf, now)
		if !ok {
			continue
		}

		slug := slugify(lf.Name)
		if err := WriteJSON(filepath.Join(leaguesDir, slug, "team-state.json"), leagueState); err != nil {
			return nil, err
		}

		leagues = append(leagues, leagueState)
	}

	if err := writeTeamArtifacts(teamDir, leagues, now); err != nil {
		return nil, err
	}

	return map[string]any{colLeagues: len(leagues)}, nil
}

// buildLeagueState assembles the team-state document for a single league.
// The bool return reports whether Markis has a roster in the league at all
// (and thus whether the caller should include the result); a false return
// alongside logged warnings mirrors the original "continue on lookup
// failure" behavior.
func (p *Publisher) buildLeagueState(ctx context.Context, leagueID string, lf *models.LeagueFormat, now string) (TeamState, bool) {
	rosters, err := p.common.LeagueRosters(ctx, leagueID)
	if err != nil {
		slog.Warn("get rosters for team", "league", leagueID, "err", err)
		return TeamState{}, false
	}
	myRoster := p.common.MyRoster(rosters)
	if myRoster == nil {
		return TeamState{}, false
	}

	leagueInfo, err := p.common.LeagueInfo(ctx, leagueID)
	if err != nil {
		slog.Warn("failed to get league info", "league_id", leagueID, "err", err)
		return TeamState{}, false
	}

	rosterOwner := buildRosterOwnerMap(rosters)
	starterSlots := nonBenchSlots(leagueInfo)
	buckets := p.buildRosterSlots(ctx, myRoster, starterSlots)
	futurePicks := p.buildFuturePicks(ctx, leagueID, myRoster, rosterOwner)
	faabRemaining := computeFaabRemaining(myRoster, leagueInfo)
	teamName := resolveTeamName(lf, myRoster)
	benchCount := countBenchSlots(leagueInfo)

	leagueState := TeamState{
		AsOf: now,
		League: TeamStateLeague{
			Name:           lf.Name,
			Platform:       "Sleeper",
			LeagueID:       leagueID,
			Format:         lf.Type,
			Teams:          lf.Teams,
			ScoringSummary: fmt.Sprintf("%d QB, PPR=%d, TEP=%s", lf.NumQbs, lf.PPR, lf.TEP),
			RosterSummary:  fmt.Sprintf("Starters: %s; Bench: %d", strings.Join(starterSlots, ","), benchCount),
			TradeDeadline:  nil,
			Notes:          "",
		},
		Team: TeamStateTeam{
			Name:                   teamName,
			CompetitiveMode:        "unknown", // would be read from TEAM_STATE.md
			TargetContentionWindow: nil,
			Roster:                 dflt(buckets.roster),
			TaxiSquad:              dflt(buckets.taxiSquad),
			InjuredReserve:         dflt(buckets.injuredReserve),
			FuturePicks:            dflt(futurePicks),
			FaabRemaining:          faabRemaining,
			ActiveTradeDiscussions: []string{},
			RosterConstraints:      []string{},
			CurrentPriorities:      []string{},
		},
		DataFreshness: TeamStateDataFreshness{
			RosterUpdatedAt:       now,
			LeagueUpdatedAt:       now,
			TransactionsUpdatedAt: now,
		},
	}
	return leagueState, true
}

// buildRosterOwnerMap maps roster_id -> owner_id for every roster in a
// league, used to resolve traded-pick ownership to display names.
func buildRosterOwnerMap(rosters []sleeper.Roster) map[int]string {
	rosterOwner := make(map[int]string, len(rosters))
	for i := range rosters {
		r := &rosters[i]
		// Preserve the prior fmt.Sprint(r["owner_id"]) sentinel: an orphaned
		// roster (owner_id null) maps to "<nil>", not "".
		owner := "<nil>"
		if r.OwnerID != nil {
			owner = util.StrOrEmpty(r.OwnerID.Ptr())
		}
		rosterOwner[int(r.RosterID)] = owner
	}
	return rosterOwner
}

// nonBenchSlots returns the league's starting lineup slots, in order,
// excluding the bench slot.
func nonBenchSlots(leagueInfo *sleeper.League) []string {
	var starterSlots []string
	for _, rp := range leagueInfo.RosterPositions {
		if rp != "BN" {
			starterSlots = append(starterSlots, rp)
		}
	}
	return starterSlots
}

// countBenchSlots returns how many bench slots the league's roster has.
func countBenchSlots(leagueInfo *sleeper.League) int {
	benchCount := 0
	for _, rp := range leagueInfo.RosterPositions {
		if rp == "BN" {
			benchCount++
		}
	}
	return benchCount
}

// computeFaabRemaining derives remaining waiver budget from league settings
// (the league's total waiver budget must come from settings, not be
// assumed — leagues can configure any total; 100 is Sleeper's own default).
func computeFaabRemaining(myRoster *sleeper.Roster, leagueInfo *sleeper.League) int {
	faab := 0
	if v, ok := myRoster.Settings["waiver_budget_used"]; ok {
		if n, ok := v.(float64); ok {
			faab = int(n)
		}
	}
	waiverBudget := 100
	if v, ok := leagueInfo.Settings["waiver_budget"]; ok {
		if n, ok := v.(float64); ok {
			waiverBudget = int(n)
		}
	}
	return max(waiverBudget-faab, 0)
}

// resolveTeamName returns the roster's custom team name if set, otherwise
// a default derived from the league name.
func resolveTeamName(lf *models.LeagueFormat, myRoster *sleeper.Roster) string {
	teamName := lf.Name + " (Markis)"
	if tn, ok := myRoster.Metadata["team_name"].(string); ok && tn != "" {
		teamName = tn
	}
	return teamName
}

// rosterBuckets partitions a roster into its active/taxi/IR slot records.
type rosterBuckets struct {
	roster         []RosterSlot
	taxiSquad      []RosterSlot
	injuredReserve []RosterSlot
}

// buildRosterSlots partitions Markis's roster into active roster, taxi
// squad, and injured reserve slot records.
func (p *Publisher) buildRosterSlots(ctx context.Context, myRoster *sleeper.Roster, starterSlots []string) rosterBuckets {
	starters := []string(myRoster.Starters)
	starterSlot := make(map[string]string, len(starters))
	for i, pid := range starters {
		if pid == "" || pid == "0" || i >= len(starterSlots) {
			continue
		}
		starterSlot[pid] = starterSlots[i]
	}
	taxiSet := stringSet([]string(myRoster.Taxi))
	reserveSet := stringSet([]string(myRoster.Reserve))

	players := []string(myRoster.Players)
	playerRows := p.common.PlayerRows(ctx, players)
	rkRows := p.common.RankingRows(ctx, players, "Dynasty Daddy", 14)

	var buckets rosterBuckets
	for _, sid := range players {
		pr := playerRows[sid]
		if pr == nil {
			continue
		}
		assignment := resolvePlayerSlot(sid, starterSlot, taxiSet, reserveSet)
		slotRec := buildPlayerSlotRecord(sid, pr, rkRows[sid], assignment.slot)
		switch assignment.bucket {
		case "taxi":
			buckets.taxiSquad = append(buckets.taxiSquad, slotRec)
		case "reserve":
			buckets.injuredReserve = append(buckets.injuredReserve, slotRec)
		default:
			buckets.roster = append(buckets.roster, slotRec)
		}
	}
	return buckets
}

// stringSet builds a lookup set from a string slice.
func stringSet(ss []string) map[string]bool {
	set := make(map[string]bool, len(ss))
	for _, s := range ss {
		set[s] = true
	}
	return set
}

// playerSlotAssignment is a player's resolved lineup slot label and which
// roster bucket ("taxi", "reserve", or "" for active roster) it belongs in.
type playerSlotAssignment struct {
	slot   string
	bucket string
}

// resolvePlayerSlot determines a player's roster slot label and bucket.
func resolvePlayerSlot(sid string, starterSlot map[string]string, taxiSet, reserveSet map[string]bool) playerSlotAssignment {
	slot := "BN"
	if s, ok := starterSlot[sid]; ok {
		slot = s
	}
	switch {
	case taxiSet[sid]:
		return playerSlotAssignment{slot: "TAXI", bucket: "taxi"}
	case reserveSet[sid]:
		return playerSlotAssignment{slot: "IR", bucket: "reserve"}
	default:
		return playerSlotAssignment{slot: slot}
	}
}

// buildPlayerSlotRecord builds the JSON record for a single rostered player
// already assigned to slot.
func buildPlayerSlotRecord(sid string, pr *PlayerRow, rk *RankingRow, slot string) RosterSlot {
	var tradeValue *int
	if rk != nil {
		tradeValue = rk.TradeValue
	}
	return RosterSlot{
		SleeperPlayerID: sid,
		FullName:        util.NilIfEmpty(util.StrOrEmpty(pr.FullName)),
		Position:        util.NilIfEmpty(util.StrOrEmpty(pr.Position)),
		NflTeam:         util.NilIfEmpty(util.StrOrEmpty(pr.TeamAbbr)),
		Slot:            slot,
		TradeValue:      tradeValue,
		Age:             pr.Age,
		InjuryStatus:    util.NilIfEmpty(util.StrOrEmpty(pr.InjuryStatus)),
	}
}

// buildFuturePicks returns the draft picks currently owned by Markis's
// roster in a league, resolving original/current owners to display names.
func (p *Publisher) buildFuturePicks(
	ctx context.Context, leagueID string, myRoster *sleeper.Roster, rosterOwner map[int]string,
) []FuturePick {
	markisRosterID := strconv.Itoa(int(myRoster.RosterID))
	tradedPicks, err := p.common.LeagueTradedPicks(ctx, leagueID)
	if err != nil {
		slog.Warn("failed to get league traded picks", "league_id", leagueID, "err", err)
		tradedPicks = nil
	}
	var futurePicks []FuturePick
	for _, pick := range tradedPicks {
		if strconv.Itoa(int(pick.OwnerID)) != markisRosterID {
			continue
		}
		originalTeam := strconv.Itoa(int(pick.RosterID))
		if name, ok := rosterOwner[int(pick.RosterID)]; ok {
			originalTeam = name
		}
		currentOwner := strconv.Itoa(int(pick.OwnerID))
		if name, ok := rosterOwner[int(pick.OwnerID)]; ok {
			currentOwner = name
		}
		futurePicks = append(futurePicks, FuturePick{
			Season:       int(pick.Season),
			Round:        int(pick.Round),
			OriginalTeam: originalTeam,
			CurrentOwner: currentOwner,
		})
	}
	return futurePicks
}

// writeTeamArtifacts writes the team/ files that don't vary per-league:
// the combined team-state document, roster markdown, future-picks and
// league-settings stubs, and the (currently empty) transaction history.
func writeTeamArtifacts(teamDir string, leagues []TeamState, now string) error {
	multi := map[string]any{
		colLeagues:     leagues,
		"as_of":        now,
		colGeneratedAt: now,
	}
	if err := WriteJSON(filepath.Join(teamDir, "team-state.json"), multi); err != nil {
		return err
	}

	if err := WriteText(filepath.Join(teamDir, "roster.md"), renderRosterMarkdown(leagues, now)); err != nil {
		return err
	}

	if err := WriteJSON(filepath.Join(teamDir, "future-picks.json"), map[string]any{
		"picks":        []any{},
		colGeneratedAt: now,
	}); err != nil {
		return err
	}

	if err := WriteJSON(filepath.Join(teamDir, "league-settings.json"), map[string]any{
		colLeagues:     models.LeagueFormats,
		colGeneratedAt: now,
	}); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(teamDir, "transaction-history.jsonl"), []byte(""), 0o600); err != nil {
		slog.Warn("failed to create transaction-history.jsonl", "err", err)
	}
	return nil
}

// renderRosterMarkdown renders the human-readable roster.md summary.
func renderRosterMarkdown(leagues []TeamState, now string) string {
	var mdLines []string
	mdLines = append(mdLines, "# Roster", "", fmt.Sprintf("_Generated %s._", now), "")
	for i := range leagues {
		lg := &leagues[i]
		mdLines = append(mdLines, "## "+lg.League.Name, "")
		for _, r := range lg.Team.Roster {
			mdLines = append(mdLines, fmt.Sprintf("- %s (%s, %s) — value: %v",
				any(r.FullName), any(r.Position), any(r.NflTeam), r.TradeValue))
		}
		mdLines = append(mdLines, "")
	}
	return strings.Join(mdLines, "\n")
}

var slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	s := slugNonAlnum.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "league"
	}
	return s
}

// renderDatasets renders datasets/ JSONL/JSON feeds.
func (p *Publisher) renderDatasets(ctx context.Context, targetDir string) (map[string]any, error) {
	dsDir := filepath.Join(targetDir, "datasets")
	if err := os.MkdirAll(dsDir, 0o750); err != nil {
		return nil, fmt.Errorf("create directory %s: %w", dsDir, err)
	}

	watchIDs, _, ownership := p.buildWatchSet(ctx)
	pr := p.common.PlayerRows(ctx, watchIDs)
	now := p.common.NowISO()

	writePlayersJSONL(dsDir, watchIDs, pr, ownership)
	sigCount := writeSignalsJSONL(dsDir, watchIDs, pr, now)

	valCount, err := p.writeValuationsJSONL(ctx, dsDir, watchIDs, now)
	if err != nil {
		return nil, err
	}

	newsCount := writeNewsEventsJSONL(targetDir, dsDir)

	if err := os.WriteFile(filepath.Join(dsDir, "league-transactions.jsonl"), []byte(""), 0o600); err != nil {
		return nil, fmt.Errorf("writeFile league-transactions: %w", err)
	}

	if err := writeEntitiesJSON(dsDir, pr, watchIDs, now); err != nil {
		return nil, err
	}

	return map[string]any{
		"players":      len(watchIDs),
		"signals":      sigCount,
		"valuations":   valCount,
		"news_events":  newsCount,
		"transactions": 0,
	}, nil
}

// writePlayersJSONL writes datasets/players.jsonl, one record per watched
// player, annotated with the leagues/roles that make it relevant.
func writePlayersJSONL(dsDir string, watchIDs []string, pr map[string]*PlayerRow, ownership map[string][][2]string) {
	playersPath := filepath.Join(dsDir, "players.jsonl")
	if err := os.WriteFile(playersPath, []byte(""), 0o600); err != nil {
		slog.Warn("failed to create players.jsonl", "err", err)
	}
	for _, sid := range sortedStringSlice(watchIDs) {
		own := ownership[sid]
		leagues := make([]map[string]string, 0, len(own))
		for _, pair := range own {
			leagues = append(leagues, map[string]string{"league": pair[0], "role": pair[1]})
		}
		p := pr[sid]
		var fullName, pos, teamAbbr, status, injStatus, injBody, injNotes, ageStr string
		var active any
		if p != nil {
			fullName = util.StrOrEmpty(p.FullName)
			pos = util.StrOrEmpty(p.Position)
			teamAbbr = util.StrOrEmpty(p.TeamAbbr)
			ageStr = intPtrStr(p.Age)
			status = util.StrOrEmpty(p.Status)
			active = p.Active
			injStatus = util.StrOrEmpty(p.InjuryStatus)
			injBody = util.StrOrEmpty(p.InjuryBodyPart)
			injNotes = util.StrOrEmpty(p.InjuryNotes)
		}
		rec := map[string]any{
			"sleeper_player_id": sid,
			"nfl_id":            "nfl:" + sid,
			"full_name":         fullName,
			"position":          pos,
			"nfl_team":          teamAbbr,
			"age":               ageStr,
			colStatus:           status,
			"active":            active,
			"injury_status":     injStatus,
			"injury_body_part":  injBody,
			"injury_notes":      injNotes,
			"leagues":           leagues,
		}
		appendJSONLFile(playersPath, rec)
	}
}

// writeSignalsJSONL writes datasets/player-signals.jsonl, emitting one
// injury signal per watched player currently carrying an injury status.
// Returns the number of signals written.
func writeSignalsJSONL(dsDir string, watchIDs []string, pr map[string]*PlayerRow, now string) int {
	sigPath := filepath.Join(dsDir, "player-signals.jsonl")
	if err := os.WriteFile(sigPath, []byte(""), 0o600); err != nil {
		slog.Warn("failed to create player-signals.jsonl", "err", err)
	}
	sigCount := 0
	for _, sid := range sortedStringSlice(watchIDs) {
		p := pr[sid]
		if p == nil {
			continue
		}
		injStatus := util.StrOrEmpty(p.InjuryStatus)
		if injStatus == "" {
			continue
		}
		obs := now
		if !p.LastSyncedAt.IsZero() {
			obs = p.LastSyncedAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		rec := PlayerSignal{
			SignalID:   SignalID("nfl:"+sid, "injury", injStatus, obs),
			PlayerID:   "nfl:" + sid,
			SignalType: colInjury,
			Value: PlayerSignalInjuryValue{
				Status:   injStatus,
				BodyPart: util.StrOrEmpty(p.InjuryBodyPart),
				Notes:    util.StrOrEmpty(p.InjuryNotes),
			},
			Source:           "Sleeper",
			SourceURL:        nil,
			ObservedAt:       obs,
			PublishedAt:      nil,
			Confidence:       "high",
			Status:           colCurrent,
			EvidenceRecordID: nil,
		}
		appendJSONLFile(sigPath, rec)
		sigCount++
	}
	return sigCount
}

// writeValuationsJSONL writes datasets/valuations.jsonl from every
// configured ranking source/market pair. Returns the number of valuation
// records written.
func (p *Publisher) writeValuationsJSONL(ctx context.Context, dsDir string, watchIDs []string, now string) (int, error) {
	valPath := filepath.Join(dsDir, "valuations.jsonl")
	if err := os.WriteFile(valPath, []byte(""), 0o600); err != nil {
		return 0, fmt.Errorf("writeFile valuations: %w", err)
	}
	sources := []struct {
		source string
		market int
	}{
		{"Dynasty Daddy", 14},
		{srcFantasyCalc, 1},
		{srcFantasyCalc, 2},
		{srcFantasyCalc, 3},
		{"KeepTradeCut", 0},
	}
	fmtCtxMap := map[int]string{
		1:  "12t-1QB-PPR",
		2:  "12t-SF-PPR-TEP1.0",
		3:  "10t-SF-HalfPPR",
		14: "Dynasty Daddy composite",
		0:  "KeepTradeCut composite",
	}
	valCount := 0
	for _, src := range sources {
		rk := p.common.RankingRows(ctx, watchIDs, src.source, src.market)
		fmtCtx := fmtCtxMap[src.market]
		for sid, r := range rk {
			valCount += writeValuationRecord(valPath, sid, r, src.source, fmtCtx, now)
		}
	}
	return valCount, nil
}

// writeValuationRecord appends a single trade-value record for a player,
// if the ranking row has one. Returns 1 if a record was written, else 0.
func writeValuationRecord(valPath, sid string, r *RankingRow, source, fmtCtx, now string) int {
	obs := now
	if r.DataDate != nil {
		obs = r.DataDate.UTC().Format("2006-01-02T15:04:05Z")
	} else if !r.SnapshotDate.IsZero() {
		obs = r.SnapshotDate.UTC().Format("2006-01-02T15:04:05Z")
	}
	if r.TradeValue == nil {
		return 0
	}
	rec := Valuation{
		ValuationID:      ValuationID("nfl:"+sid, source, "trade-value", fmtCtx, obs),
		PlayerID:         "nfl:" + sid,
		Source:           source,
		ValuationType:    "trade-value",
		FormatContext:    fmtCtx,
		Value:            *r.TradeValue,
		ObservedAt:       obs,
		SourceURL:        nil,
		Confidence:       colMedium,
		EvidenceRecordID: nil,
	}
	appendJSONLFile(valPath, rec)
	return 1
}

// writeNewsEventsJSONL writes datasets/news-events.jsonl from current
// evidence records. Returns the number of events written.
func writeNewsEventsJSONL(targetDir, dsDir string) int {
	newsPath := filepath.Join(dsDir, "news-events.jsonl")
	if err := os.WriteFile(newsPath, []byte(""), 0o600); err != nil {
		slog.Warn("failed to create news-events.jsonl", "err", err)
	}
	recDir := filepath.Join(targetDir, "evidence", "records")
	entries, err := os.ReadDir(recDir)
	if err != nil {
		slog.Warn("failed to read evidence records directory", "path", recDir, "err", err)
		entries = nil
	}
	root, rootErr := os.OpenRoot(recDir)
	if rootErr != nil {
		slog.Warn("failed to open evidence records dir root", "path", recDir, "err", rootErr)
		return 0
	}
	defer root.Close()
	newsCount := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := rootReadAll(root, entry.Name())
		if err != nil {
			continue
		}
		var rec map[string]any
		if json.Unmarshal(data, &rec) != nil {
			continue
		}
		if rec["status"] != "current" {
			continue
		}
		appendJSONLFile(newsPath, map[string]any{
			"evidence_record_id": rec["id"],
			colCanonicalURL:      rec[colCanonicalURL],
			colTitle:             rec[colTitle],
			colPublishedAt:       rec[colPublishedAt],
			"topic":              rec["topic"],
			"publisher":          rec["publisher"],
			"player_ids":         rec["player_ids"],
			"team_ids":           rec["team_ids"],
		})
		newsCount++
	}
	return newsCount
}

// writeEntitiesJSON writes datasets/entities.json summarizing the leagues,
// teams, and player universe covered by this publish run.
func writeEntitiesJSON(dsDir string, pr map[string]*PlayerRow, watchIDs []string, now string) error {
	teams := make(map[string]bool)
	for _, p := range pr {
		t := util.StrOrEmpty(p.TeamAbbr)
		if t != "" {
			teams[t] = true
		}
	}
	teamList := make([]string, 0, len(teams))
	for t := range teams {
		teamList = append(teamList, t)
	}
	leagueList := make([]map[string]any, 0, len(models.LeagueFormats))
	for lid, lf := range models.LeagueFormats {
		leagueList = append(leagueList, map[string]any{
			"league_id": lid, "name": lf.Name, "format": lf.Type, "teams": lf.Teams,
		})
	}
	entities := map[string]any{
		"as_of":         now,
		"leagues":       leagueList,
		"teams":         teamList,
		"players_count": len(watchIDs),
		"players":       []any{},
	}
	return WriteJSON(filepath.Join(dsDir, "entities.json"), entities)
}

func appendJSONLFile(path string, obj any) {
	data, err := json.Marshal(obj)
	if err != nil {
		slog.Warn("failed to marshal JSONL object", "path", path, "err", err)
		return
	}
	data = append(data, '\n')
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		slog.Warn("failed to open JSONL dir", "path", path, "err", err)
		return
	}
	defer root.Close()
	f, err := root.OpenFile(filepath.Base(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		slog.Warn("failed to open JSONL file", "path", path, "err", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		slog.Warn("failed to write JSONL data", "path", path, "err", err)
	}
}

func sortedStringSlice(ids []string) []string {
	result := make([]string, len(ids))
	copy(result, ids)
	sort.Strings(result)
	return result
}
