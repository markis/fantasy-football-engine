-- Leaguemate profiler — Tier 1 (roster map).
--
-- Extends the base schema with a 1-hop manager graph: Markis's 5 leagues ->
-- their managers -> those managers' OTHER leagues -> those leagues' rosters.
-- Powers the "who owns player X across their leagues" overlap query and a
-- cheap contender/rebuild signal (wins/losses), and sharpens the corpus
-- decision-relevance gate (leaguemate rosters are a better watch set than a
-- static name index).
--
-- Scope guard (enforced in sync_leaguemates.py, not the schema): only
-- managers from Markis's 5 owned leagues are profiled. Managers discovered
-- inside other leagues are stored (free — same API call) but NOT recursively
-- expanded. Sport = nfl, season = current. Per-manager league coverage is
-- capped (default 15) so two hyper-active managers can't blow the budget.
--
-- All tables upsert idempotently (ON CONFLICT ... DO UPDATE), matching the
-- sync_players/sync_rankings pattern. Roster snapshots are replace-per-league:
-- the player rows for a league are deleted then reinserted each run, so the
-- table always reflects the current Sleeper state (no stale "left the team"
-- rows). league_manager and league rows are upserts.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ---------------------------------------------------------------------------
-- league — catalog of every league we touch: Markis's 5 (is_markis_league)
-- plus discovered leaguemate leagues. Sleeper league_id is the natural key.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS league (
    league_id            text PRIMARY KEY,           -- Sleeper league_id
    name                 text,
    season               text,                       -- e.g. '2026'
    sport                text DEFAULT 'nfl',
    status               text,                       -- in_season | pre_draft | complete | ...
    num_teams            int,
    has_superflex        boolean,                    -- derived: 'SUPER_FLEX' in roster_positions
    is_best_ball         boolean,                    -- derived: settings.best_ball == 1
    league_type          int,                        -- settings.type (2=dynasty, 1=keeper, 0=redraft)
    roster_positions     jsonb,                      -- full slot list from Sleeper
    settings             jsonb,                      -- Sleeper league settings (scoring, etc.)
    previous_league_id   text,                       -- dynasty chain (year-over-year carryover)
    is_markis_league     boolean NOT NULL DEFAULT false,
    discovered_via_user_id text,                     -- for non-Markis leagues: which leaguemate led us here
    first_seen_at        timestamptz NOT NULL DEFAULT now(),
    last_synced_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS league_season_idx    ON league (season);
CREATE INDEX IF NOT EXISTS league_markis_idx    ON league (is_markis_league) WHERE is_markis_league;
CREATE INDEX IF NOT EXISTS league_discover_idx  ON league (discovered_via_user_id) WHERE discovered_via_user_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- sleeper_user — identity table keyed by user_id. Username (the stable @handle)
-- is the canonical identity; it's NOT returned by /league/{id}/users, so it's
-- fetched via a per-user /user/{id} call, only for Markis's direct leaguemates
-- (bounded). display_name/avatar come free from the league users endpoint and
-- are populated for every user we encounter. league_manager joins here for the
-- handle; team_name stays per-league in league_manager.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sleeper_user (
    user_id        text PRIMARY KEY,            -- Sleeper user_id
    username       text,                        -- @handle (canonical identity); /user/{id} only
    display_name   text,                        -- free from league users endpoint
    avatar         text,
    is_markis      boolean NOT NULL DEFAULT false,
    first_seen_at  timestamptz NOT NULL DEFAULT now(),
    last_synced_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS sleeper_user_username_idx ON sleeper_user (username) WHERE username IS NOT NULL;

-- ---------------------------------------------------------------------------
-- league_manager — one row per (league, roster slot). Captures who manages
-- which team in which league. owner_id is the Sleeper user_id; co_owners are
-- also emitted (co_owner = true) so co-managed teams aren't invisible.
-- standings columns (wins/losses/ties) come from roster.settings and give a
-- free contender/rebuild hint even at Tier 1. Identity (username/display_name)
-- lives in sleeper_user; team_name is per-league here.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS league_manager (
    league_id     text        NOT NULL REFERENCES league(league_id) ON DELETE CASCADE,
    user_id       text        NOT NULL REFERENCES sleeper_user(user_id) ON DELETE CASCADE,
    roster_id     int         NOT NULL,               -- roster slot within the league
    team_name     text,                              -- user.metadata.team_name (per-league, e.g. 'Queensland Maroons')
    co_owner      boolean     NOT NULL DEFAULT false, -- true if sourced from co_owners, not owner_id
    is_markis     boolean     NOT NULL DEFAULT false,  -- user_id == MARKIS_USER_ID (derived, stable)
    wins          int,
    losses        int,
    ties          int,
    fpts          real,                               -- standings tiebreak, optional
    last_synced_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (league_id, roster_id, user_id)       -- a slot has one owner + N co_owners
);
CREATE INDEX IF NOT EXISTS lm_user_idx      ON league_manager (user_id);
CREATE INDEX IF NOT EXISTS lm_markis_idx    ON league_manager (is_markis) WHERE is_markis;
CREATE INDEX IF NOT EXISTS lm_league_roster  ON league_manager (league_id, roster_id);

-- Reconcile an already-applied (older) league_manager: drop the identity
-- columns that moved to sleeper_user and add the FK. Idempotent (IF EXISTS /
-- guarded). Safe because the table is empty until sync_leaguemates.py runs.
ALTER TABLE league_manager DROP COLUMN IF EXISTS display_name;
ALTER TABLE league_manager DROP COLUMN IF EXISTS username;
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'league_manager_user_id_fkey') THEN
        ALTER TABLE league_manager
            ADD CONSTRAINT league_manager_user_id_fkey
            FOREIGN KEY (user_id) REFERENCES sleeper_user(user_id) ON DELETE CASCADE;
    END IF;
END $$;

-- ---------------------------------------------------------------------------
-- leaguemate_roster_player — the players currently on a roster in a league.
-- Replace-per-league each run (DELETE + INSERT) so it always mirrors current
-- Sleeper state. `slot` distinguishes starters from bench/IR/taxi (reserve
-- and taxi players are owned and matter for valuation, just not lineups).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS leaguemate_roster_player (
    league_id          text        NOT NULL REFERENCES league(league_id) ON DELETE CASCADE,
    roster_id          int         NOT NULL,
    sleeper_player_id  text        NOT NULL,
    slot               text        NOT NULL DEFAULT 'bench',  -- starter | bench | reserve | taxi
    snapshot_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (league_id, roster_id, sleeper_player_id)
);
CREATE INDEX IF NOT EXISTS lrp_player_idx   ON leaguemate_roster_player (sleeper_player_id);
CREATE INDEX IF NOT EXISTS lrp_league_idx    ON leaguemate_roster_player (league_id);
-- FK to player table is intentionally NOT enforced: a roster can hold IR/taxi
-- or just-drafted players before sync_players.py has them. Join loosely.

-- ---------------------------------------------------------------------------
-- sync_run — tiny bookkeeping so we can see when/how the profiler ran and
-- how many calls it spent (useful for tuning the per-manager cap).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS leaguemate_sync_run (
    id              uuid PRIMARY KEY DEFAULT uuid_v7(),
    started_at      timestamptz NOT NULL DEFAULT now(),
    finished_at     timestamptz,
    season          text,
    api_calls       int,
    managers_seen   int,
    leagues_seen    int,
    roster_rows     int,
    status          text,                              -- ok | partial | error
    note            text
);