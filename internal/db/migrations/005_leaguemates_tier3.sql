-- Leaguemate profiler — Tier 3 (tendency dossier).
--
-- Weekly LLM-generated per-leaguemate dossier, backed by deterministic signals
-- computed from the Tier 1 roster map + Tier 2 trade ledger. One row per
-- (snapshot_date, user_id), so tendency drift is trackable week over week
-- (a rebuilder trending toward contention shows up as a rising
-- contender_score and shifting position_bias).
--
-- assess_leaguemates.py (Sun 07:30 ET) computes the numeric signals in Python
-- (grounded, no hallucination), calls minimax-m3 once per manager to write the
-- `dossier` prose, and snapshots the row here. corpus/render_leaguemates.py
-- then renders current/leaguemate-brief.md + datasets/leaguemate-profiles.jsonl
-- from the latest snapshot at publish time (Sun 08:05). The self-study cron
-- folds the brief into KNOWLEDGE.md.
--
-- Only Markis's ~50 direct leaguemates are profiled (the people he actually
-- trades with), not secondary managers in discovered leagues.

CREATE EXTENSION IF NOT EXISTS pgcrypto;  -- uuid_v7 used elsewhere; harmless

CREATE TABLE IF NOT EXISTS leaguemate_signal (
    snapshot_date    date        NOT NULL,
    user_id          text        NOT NULL,
    username         text,
    display_name     text,
    leagues_count    int,
    win_pct          real,                       -- aggregate across leagues (NULL pre-season)
    avg_core_age     real,                       -- avg age of their top-7 valued players
    contender_score  int,                        -- 0-100 heuristic composite
    position_bias    jsonb,                      -- {"RB": 14, "WR": 9, "QB": 6, ...}
    trade_count      int,
    trade_count_30d  int,
    overpay_delta    real,                       -- avg (gave_value - got_value); + = tends to overpay
    trades_valuated  int,                        -- trades where both sides could be valued
    picks_acquired   int,
    picks_traded     int,
    net_firsts       int,                        -- acquired - traded future 1sts
    dossier          text,                       -- LLM prose (contender/rebuild, tendencies, how-to-trade)
    raw_signals      jsonb,                      -- full computed bundle (recent trades, etc.)
    created_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (snapshot_date, user_id)
);
CREATE INDEX IF NOT EXISTS ls_snapshot_idx ON leaguemate_signal (snapshot_date);
CREATE INDEX IF NOT EXISTS ls_user_idx     ON leaguemate_signal (user_id, snapshot_date DESC);