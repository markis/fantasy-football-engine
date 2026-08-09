-- Fantasy Football News Schema
-- Same pattern as Hudson News but without:
--   is_hudson (no geographic filter — everything is fantasy-relevant by source)
--   city_official (no city officials)
--   podcast_episode/covered_* (no podcast)
--   news.html/news.xml (agent IS the interface — Discord chat)

CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ---------------------------------------------------------------------------
-- UUID v7 generation (time-ordered, sortable, index-friendly)
-- Uses pgcrypto for random bytes; timestamp from clock_timestamp() for
-- monotonic ordering within a transaction.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION uuid_v7() RETURNS uuid AS $$
DECLARE
  unix_ts_ms bigint;
  uuid_bytes bytea;
  rand_bytes bytea;
  b6 integer;
  b8 integer;
BEGIN
  unix_ts_ms := extract(epoch FROM clock_timestamp()) * 1000;
  rand_bytes := gen_random_bytes(10);
  uuid_bytes := substring(int8send(unix_ts_ms) FROM 3 FOR 6) || substring(rand_bytes FROM 1 FOR 4);
  b6 := get_byte(uuid_bytes, 6);
  uuid_bytes := set_byte(uuid_bytes, 6, (b6 & 15) | 112);
  b8 := get_byte(uuid_bytes, 8);
  uuid_bytes := set_byte(uuid_bytes, 8, (b8 & 63) | 128);
  uuid_bytes := uuid_bytes || substring(rand_bytes FROM 5 FOR 6);
  RETURN encode(uuid_bytes, 'hex')::uuid;
END;
$$ LANGUAGE plpgsql VOLATILE;

-- Source table (RSS feeds)
CREATE TABLE IF NOT EXISTS source (
    id UUID PRIMARY KEY DEFAULT uuid_v7(),
    name TEXT NOT NULL UNIQUE,
    type TEXT NOT NULL DEFAULT 'rss',
    url TEXT NOT NULL,
    fetch_method TEXT NOT NULL DEFAULT 'rss',
    poll_interval_sec INTEGER NOT NULL DEFAULT 3600,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- News item table (RSS articles + enriched content)
CREATE TABLE IF NOT EXISTS news_item (
    id UUID PRIMARY KEY DEFAULT uuid_v7(),
    source_id UUID NOT NULL REFERENCES source(id),
    source_type TEXT NOT NULL DEFAULT 'rss',
    external_id TEXT,
    url TEXT,
    canonical_url TEXT,
    canonical_url_hash TEXT,
    title TEXT,
    summary_short TEXT,
    content_text TEXT,
    content_hash TEXT,
    simhash BIGINT,
    fetched_at TIMESTAMPTZ,
    body_fetch_status TEXT DEFAULT 'pending',
    body_fetch_attempts INTEGER DEFAULT 0,
    body_fetched_at TIMESTAMPTZ,
    is_relevant BOOLEAN DEFAULT false,
    is_news BOOLEAN DEFAULT true,
    news_story TEXT,
    news_story_generated_at TIMESTAMPTZ,
    news_story_model TEXT,
    embedding vector(768),
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    cluster_id UUID
);

-- Story cluster table (groups related news items)
CREATE TABLE IF NOT EXISTS story_cluster (
    id UUID PRIMARY KEY DEFAULT uuid_v7(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Fact table (atomic facts extracted from news items)
CREATE TABLE IF NOT EXISTS fact (
    id UUID PRIMARY KEY DEFAULT uuid_v7(),
    news_item_id UUID NOT NULL REFERENCES news_item(id),
    fact_text TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,  -- article published_at (RSS); every fact is rooted in time
    confidence TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    embedding vector(768)
);

-- Player table (Sleeper player database — synced daily by sync_players.py)
CREATE TABLE IF NOT EXISTS player (
    id UUID PRIMARY KEY DEFAULT uuid_v7(),
    sleeper_player_id TEXT NOT NULL UNIQUE,
    first_name TEXT,
    last_name TEXT,
    full_name TEXT,
    search_full_name TEXT,
    position TEXT,
    fantasy_positions TEXT[] NOT NULL DEFAULT '{}',
    team TEXT,
    team_abbr TEXT,
    status TEXT,
    active BOOLEAN NOT NULL DEFAULT false,
    injury_status TEXT,
    injury_body_part TEXT,
    injury_notes TEXT,
    injury_start_date DATE,
    age INTEGER,
    years_exp INTEGER,
    birth_date DATE,
    height TEXT,
    weight TEXT,
    college TEXT,
    number INTEGER,
    depth_chart_position TEXT,
    depth_chart_order INTEGER,
    practice_participation TEXT,
    practice_description TEXT,
    gsis_id TEXT,
    espn_id TEXT,
    rotowire_id TEXT,
    rotoworld_id TEXT,
    yahoo_id TEXT,
    sportradar_id TEXT,
    stats_id TEXT,
    news_updated TIMESTAMPTZ,
    last_synced_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_news_item_source ON news_item(source_id);
CREATE INDEX IF NOT EXISTS idx_news_item_published ON news_item(published_at);
CREATE INDEX IF NOT EXISTS idx_news_item_relevant ON news_item(is_relevant) WHERE is_relevant = true;
CREATE INDEX IF NOT EXISTS idx_news_item_cluster ON news_item(cluster_id);
CREATE INDEX IF NOT EXISTS idx_fact_news_item ON fact(news_item_id);
CREATE INDEX IF NOT EXISTS idx_source_active ON source(is_active) WHERE is_active = true;
CREATE INDEX IF NOT EXISTS idx_player_position ON player(position);
CREATE INDEX IF NOT EXISTS idx_player_team ON player(team);
CREATE INDEX IF NOT EXISTS idx_player_active ON player(active) WHERE active = true;
CREATE INDEX IF NOT EXISTS idx_player_status ON player(status);

-- Player updated_at trigger
DROP TRIGGER IF EXISTS player_updated_at ON player;
CREATE TRIGGER player_updated_at BEFORE UPDATE ON player
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Player ranking table (Dynasty Daddy — synced daily by sync_rankings.py)
-- Stores BOTH 1QB (trade_value/overall_rank) and Superflex
-- (sf_trade_value/sf_overall_rank) columns per player.
CREATE TABLE IF NOT EXISTS player_ranking (
    id UUID PRIMARY KEY DEFAULT uuid_v7(),
    player_id UUID NOT NULL REFERENCES player(id) ON DELETE CASCADE,
    source TEXT NOT NULL DEFAULT 'Dynasty Daddy',
    market INTEGER NOT NULL DEFAULT 14,
    name_id TEXT,
    position TEXT,
    team TEXT,
    overall_rank INTEGER,
    position_rank INTEGER,
    sf_overall_rank INTEGER,
    sf_position_rank INTEGER,
    trade_value INTEGER,
    sf_trade_value INTEGER,
    -- single-season redraft trade value (FantasyCalc redraftValue); NULL for
    -- sources that don't provide it. Powers the redraft league + diagnostics.
    redraft_value INTEGER,
    all_time_high INTEGER,
    all_time_low INTEGER,
    all_time_high_sf INTEGER,
    all_time_low_sf INTEGER,
    all_time_best_rank INTEGER,
    all_time_worst_rank INTEGER,
    all_time_best_rank_sf INTEGER,
    all_time_worst_rank_sf INTEGER,
    three_month_high INTEGER,
    three_month_low INTEGER,
    three_month_high_sf INTEGER,
    three_month_low_sf INTEGER,
    three_month_best_rank INTEGER,
    three_month_worst_rank INTEGER,
    three_month_best_rank_sf INTEGER,
    three_month_worst_rank_sf INTEGER,
    last_month_value INTEGER,
    last_month_value_sf INTEGER,
    last_month_rank INTEGER,
    last_month_rank_sf INTEGER,
    avg_adp TEXT,
    fantasypro_adp INTEGER,
    bb10_adp INTEGER,
    rtsports_adp INTEGER,
    underdog_adp INTEGER,
    drafters_adp INTEGER,
    dynasty_daddy_adp INTEGER,
    avg_ros TEXT,
    espn_ros INTEGER,
    fantasyguys_ros INTEGER,
    fantasypros_ros INTEGER,
    fanduel_ros INTEGER,
    percent_owned TEXT,
    percent_started TEXT,
    snapshot_date DATE NOT NULL DEFAULT CURRENT_DATE,
    data_date TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (player_id, source, market)
);

CREATE INDEX IF NOT EXISTS idx_player_ranking_rank ON player_ranking(overall_rank);
CREATE INDEX IF NOT EXISTS idx_player_ranking_pos_rank ON player_ranking(position, position_rank);
CREATE INDEX IF NOT EXISTS idx_player_ranking_sf ON player_ranking(sf_overall_rank);
CREATE INDEX IF NOT EXISTS idx_player_ranking_player ON player_ranking(player_id);

DROP TRIGGER IF EXISTS player_ranking_updated_at ON player_ranking;
CREATE TRIGGER player_ranking_updated_at BEFORE UPDATE ON player_ranking
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Vector indexes (ivfflat for cosine similarity)
CREATE INDEX IF NOT EXISTS idx_news_item_embedding
    ON news_item USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100);

CREATE INDEX IF NOT EXISTS idx_fact_embedding
    ON fact USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100);

-- Insert default fantasy football RSS sources
-- URLs verified working 2026-08-03. NFL.com and FFToolbox RSS feeds were dead;
-- replaced with CBS Sports NFL headlines and FantasySP NFL fantasy respectively.
INSERT INTO source (name, type, url, fetch_method, poll_interval_sec) VALUES
    ('Rotowire Fantasy News', 'rss', 'https://football.rotowire.com/rss/news.php', 'rss', 3600),
    ('ESPN Fantasy Football', 'rss', 'https://www.espn.com/espn/rss/fantasy/news', 'rss', 3600),
    ('CBS Sports NFL', 'rss', 'https://www.cbssports.com/rss/headlines/nfl', 'rss', 3600),
    ('PFF Fantasy', 'rss', 'https://www.pff.com/feed', 'rss', 3600),
    ('FantasySP NFL', 'rss', 'https://www.fantasysp.com/rss/nfl/fantasysp/', 'rss', 3600)
ON CONFLICT (name) DO NOTHING;

-- ---------------------------------------------------------------------------
-- player_ranking_history — append-only daily snapshots for trend tracking.
-- One row per (player, source, market, day). Idempotent per day (same-day
-- re-runs update, not duplicate). Populated by sync_fantasycalc/sync_rankings.
-- Powers risers/fallers + value-over-time queries.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS player_ranking_history (
    player_id   uuid        NOT NULL REFERENCES player(id) ON DELETE CASCADE,
    source      text        NOT NULL,
    market      int         NOT NULL,
    snapshot_date date      NOT NULL DEFAULT CURRENT_DATE,
    trade_value int,
    overall_rank int,
    position_rank int,
    redraft_value int,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (player_id, source, market, snapshot_date)
);
CREATE INDEX IF NOT EXISTS idx_prh_date ON player_ranking_history(source, market, snapshot_date);
CREATE INDEX IF NOT EXISTS idx_prh_player ON player_ranking_history(player_id, source, market, snapshot_date);
