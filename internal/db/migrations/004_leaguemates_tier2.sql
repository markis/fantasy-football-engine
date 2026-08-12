-- Leaguemate profiler — Tier 2 (trade ledger).
--
-- Stores every completed Sleeper TRADE from the leagues we already map in
-- Tier 1 (Markis's 5 + discovered leaguemate leagues), normalized so the agent
-- can answer "what did player X actually go for in a leaguemate's other
-- league?" — the true-market-price signal that anchors trade negotiation far
-- better than market averages.
--
-- Design notes:
--   - We store ALL trades (not just watch-set ones): storage is trivial
--     (~15k trades / ~60k asset rows over a season) and it keeps full history
--     for Tier 3 tendency profiling. Each ASSET is flagged is_watch_set so the
--     headline "what did X go for" query stays index-fast; the relevance
--     filter applies at corpus evidence-emission time, not collection.
--   - Sleeper trade shape: adds[p]=to_roster, drops[p]=from_roster (either may
--     be NULL for pure pick trades); draft_picks[] carry
--     {round, season, roster_id (original slot), owner_id (->),
--      previous_owner_id (<-)}.
--   - transaction_id is a global Sleeper snowflake -> natural PK.
--   - Scan is incremental (leaguemate_scan_state) — daily run scans
--     last_week..current_nfl_week per in_season/complete league; drafting /
--     pre_draft leagues are skipped (no in-season trades). See
--     the leaguemates trades sync.

CREATE EXTENSION IF NOT EXISTS pgcrypto;  -- for uuid_v7()

-- ---------------------------------------------------------------------------
-- leaguemate_transaction — one row per Sleeper trade. transaction_id (global
-- snowflake) is the PK, so a trade seen in multiple leagues (impossible) or
-- re-scanned simply upserts.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS leaguemate_transaction (
    transaction_id     text PRIMARY KEY,           -- Sleeper global snowflake
    league_id          text NOT NULL REFERENCES league(league_id) ON DELETE CASCADE,
    type               text NOT NULL DEFAULT 'trade',
    status             text,                        -- complete | pending | failed
    creator            text,                        -- Sleeper user_id who proposed it
    week               int,                         -- the week/leg the txn was booked under
    roster_ids         int[] NOT NULL DEFAULT '{}',  -- participating roster slots
    consenter_ids      int[] NOT NULL DEFAULT '{}',  -- rosters that consented
    created_at         timestamptz,                  -- from Sleeper `created` (epoch ms)
    status_updated_at  timestamptz,                  -- from Sleeper `status_updated`
    is_markis_league   boolean NOT NULL DEFAULT false,
    involves_watch_set boolean NOT NULL DEFAULT false, -- any asset is a watch-set player
    raw                jsonb,                        -- full Sleeper txn object (picks meta, etc.)
    first_seen_at      timestamptz NOT NULL DEFAULT now(),
    last_synced_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS lt_league_idx     ON leaguemate_transaction (league_id);
CREATE INDEX IF NOT EXISTS lt_created_idx    ON leaguemate_transaction (created_at DESC);
CREATE INDEX IF NOT EXISTS lt_markis_idx     ON leaguemate_transaction (is_markis_league) WHERE is_markis_league;
CREATE INDEX IF NOT EXISTS lt_watchset_idx   ON leaguemate_transaction (involves_watch_set) WHERE involves_watch_set;

-- ---------------------------------------------------------------------------
-- leaguemate_trade_asset — one row per moving player or pick. Normalized so
-- "what was X traded for?" is a self-join on transaction_id. Roster ids are
-- only unique within a league, so league_id is denormalized for joins to
-- league_manager -> sleeper_user (the trading managers).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS leaguemate_trade_asset (
    id                 uuid PRIMARY KEY DEFAULT uuid_v7(),
    transaction_id     text NOT NULL REFERENCES leaguemate_transaction(transaction_id) ON DELETE CASCADE,
    league_id          text NOT NULL REFERENCES league(league_id) ON DELETE CASCADE,
    asset_type         text NOT NULL,                -- 'player' | 'pick'
    sleeper_player_id  text,                         -- NULL for picks
    pick_season        text,                         -- e.g. '2027' (picks only)
    pick_round         int,                          -- picks only
    pick_roster_id     int,                          -- original owner slot (picks only)
    from_roster_id     int,                          -- drops[p] / pick.previous_owner_id
    to_roster_id       int,                          -- adds[p] / pick.owner_id
    is_watch_set       boolean,                      -- player is on a Markis-league roster
    first_seen_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS lta_player_idx   ON leaguemate_trade_asset (sleeper_player_id) WHERE sleeper_player_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS lta_txn_idx       ON leaguemate_trade_asset (transaction_id);
CREATE INDEX IF NOT EXISTS lta_watchset_idx  ON leaguemate_trade_asset (is_watch_set) WHERE is_watch_set;
-- No dedup index: the leaguemates trades sync deletes + reinserts assets per
-- transaction on each re-scan, so duplicates are impossible by construction.

-- ---------------------------------------------------------------------------
-- leaguemate_scan_state — incremental scan bookkeeping per league.
-- last_week_scanned = highest week fully fetched; each run re-scans that week
-- (to catch late-added trades) then scans forward to the current NFL week.
-- skip_reason is set for drafting/pre_draft leagues we deliberately skip.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS leaguemate_scan_state (
    league_id         text PRIMARY KEY REFERENCES league(league_id) ON DELETE CASCADE,
    status            text,                          -- league status at last scan
    last_week_scanned int NOT NULL DEFAULT 0,
    last_scanned_at   timestamptz NOT NULL DEFAULT now(),
    trades_stored     int NOT NULL DEFAULT 0,
    skip_reason       text                           -- 'drafting' | 'pre_draft' | 'error: ...'
);
CREATE INDEX IF NOT EXISTS lss_skip_idx ON leaguemate_scan_state (skip_reason) WHERE skip_reason IS NOT NULL;