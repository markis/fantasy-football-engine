# Fix: `get_rankings` MCP tool returns `null` and trips the agent's loop-detector circuit breaker

## Symptom
The zeroclaw fantasy agent (consumer of this engine's MCP) called
`ff-engine__get_rankings` 11 times with different args; 7 of those returned
JSON `null`. zeroclaw's loop detector aborted the run:

> Circuit breaker: tool 'ff-engine__get_rankings' called 7 times with
> different arguments but identical results — no progress

The agent cannot distinguish "wrong args" from "no data" because `null` is
opaque, so it retries with different args and many of those *also* return
`null`.

## Reproducible evidence (live DB state)
`player_ranking` rows that actually exist, by source/market:

| source | valid markets | rows | has overall_rank | has sf_overall_rank |
|---|---|---|---|---|
| Dynasty Daddy | 14 | 612 | 612 | 612 |
| KeepTradeCut | 0 | 612 | 612 | 612 |
| FantasyCalc | 1 | 399 | 399 | 350 |
| FantasyCalc | 2 | 399 | 399 | 0 |
| FantasyCalc | 3 | 399 | 399 | 0 |
| FantasyPros ECR | 1 | 422 | 422 | 0 |

Confirmed agent calls + outcomes (from the aborted trace):

| args | result | why |
|---|---|---|
| `source=FantasyCalc` (no market) | null | market defaults to 14; FC has no market 14 |
| `source=FantasyCalc, superflex=true` (no market) | null | same; market 14 mismatch |
| `source=KeepTradeCut` (no market) | null | market defaults to 14; KTC is market 0 |
| `source=KeepTradeCut, market=0` | null | **market 0 overwritten to 14 inside GetRankings** |
| `source=FantasyCalc, market=1/2/3` | data | valid combo |
| `source=Dynasty Daddy` (no market) | data | market defaults to 14; DD *is* market 14 |
| `source=FantasyCalc, market=3, superflex=true` | data (non-sf values) | superflex ignored for FantasyCalc |

## Root cause — code locations

### Bug 1 (the abort trigger): empty result is returned as Go `nil` → JSON `null`
`internal/query/service.go`, `GetRankings` (starts ~line 339):
```go
var result []map[string]any
for rows.Next() { ... result = append(result, item) }
return result, nil
```
When zero rows match, `result` stays `nil`, so MCP serializes it as `null`.
Must return a JSON empty array `[]` (and ideally a small explanatory
structure) so the agent sees "empty", not "error/unknown".

### Bug 2: `if market == 0 { market = 14 }` overwrites a real value
In `GetRankings`:
```go
if market == 0 {
    market = 14 // Dynasty Daddy default
}
```
The MCP handler (`internal/mcp/server.go`, `get_rankings` tool, ~line 207)
already defaults an *absent* market to 14 via `getInt(args, "market", 14)`.
So `market == 0` reaching `GetRankings` can only mean the caller **explicitly
passed 0** — which is KeepTradeCut's real market. Overwriting it to 14
guarantees KTC always returns null. Remove this override (an explicit 0 must
be honored), or move the "absent → default" distinction to the handler only.

### Bug 3: mismatched tool defaults
Handler defaults: `source = "FantasyCalc"`, `market = 14`. FantasyCalc has
**no** market 14 (it has 1/2/3); market 14 is Dynasty Daddy. So the no-args /
source-only call returns null. The tool description even says *"Default
source: FantasyCalc, market 14 (Dynasty Daddy composite)"* —
self-contradictory. Pick a valid default combo. Best: resolve `market` from
`source` when only one is given:
- KeepTradeCut → 0
- Dynasty Daddy → 14
- FantasyCalc → 1
- FantasyPros ECR → 1

### Bug 4 (correctness, secondary): superflex ignored for FantasyCalc
```go
if superflex && source != srcFantasyCalc {
    valueCol = "r.sf_trade_value"
    overallCol = "r.sf_overall_rank"
    posRankCol = "r.sf_position_rank"
}
```
FantasyCalc market 1 *does* have sf data (`sf_overall_rank` populated for 350
rows), but superflex is never applied for FC. Either apply superflex to FC
when sf columns are non-null, or document that FC superflex is unsupported and
return a clear message for that case.

### Bug 5 (data quality, separate/optional): `team` always `""`
`GetRankings` selects `p.team_abbr` from the `player` table, which is not
populated; `player_ranking.team` holds the abbr (e.g. `CIN`, `SEA`). Switch
the SELECT to `r.team` (also affects `search_players` if it has the same
issue — worth a quick check). Verify against the live DB.

## Required changes
1. `GetRankings` must return `[]` (not `nil`) for empty results. Prefer
   initializing `result := []map[string]any{}`. Consider returning a
   structured error/empty payload (e.g.
   `{"results": [], "source": ..., "market": ..., "note": "no rows for this source/market"}`)
   so the agent gets actionable context instead of bare `null`.
2. Remove the `if market == 0 { market = 14 }` override in `GetRankings`;
   keep defaulting in the handler only.
3. Make the `get_rankings` tool defaults a valid combo. Prefer source→market
   resolution in the handler: when `market` is absent, derive it from
   `source` (KTC→0, DD→14, FC→1, ECR→1); when `source` is absent, derive from
   `market` (0→KTC, 14→DD, 1/2/3→FC). Update the tool `Description` accordingly.
4. Apply superflex column selection to FantasyCalc when sf data exists, or
   return a clear "superflex not available for this source/market" message
   instead of silently returning non-sf values.
5. (Optional) Select `r.team` instead of `p.team_abbr` so `team` is populated.

## Acceptance checks
- `get_rankings` with no args returns a valid, non-null result (the default
  source/market combo must have rows).
- `get_rankings {source:"KeepTradeCut", market:0}` returns KTC rows (not
  null).
- `get_rankings {source:"KeepTradeCut"}` (no market) returns KTC rows via
  source→market resolution.
- `get_rankings {source:"FantasyCalc"}` (no market) returns FC rows (market
  1).
- `get_rankings {source:"FantasyCalc", market:1, superflex:true}` returns sf
  columns when sf data exists.
- An empty/no-match case returns `[]` (or a structured empty payload), never
  `null`.
- `team` is populated in results.
- Add/adjust unit tests in `internal/query` (and `internal/mcp` if
  applicable) covering the source→market resolution and the
  empty-result-as-array case.

## Out of scope
Do not change the DB schema or the sync jobs. The data is correct in
`player_ranking`; the bug is in the query/tool layer only.