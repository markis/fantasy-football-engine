-- Fantasy Football pipeline migration — extends the base schema to support
-- the Hudson-derived pipeline scripts (fetch_rss, enrich, embed, dedup,
-- cluster, extract_facts, generate_stories, query_*).
--
-- The base schema (schema.sql) is intentionally minimal. This migration adds
-- the columns/tables/constraints the pipeline scripts depend on, while keeping
-- the FF-specific naming (is_relevant instead of is_hudson, no city_official,
-- no podcast, no report tables).

-- ---------------------------------------------------------------------------
-- UUID v7 generation (time-ordered, sortable, index-friendly)
-- ---------------------------------------------------------------------------
CREATE EXTENSION IF NOT EXISTS pgcrypto;

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

-- Switch all table ID defaults from gen_random_uuid() to uuid_v7()
ALTER TABLE source ALTER COLUMN id SET DEFAULT uuid_v7();
ALTER TABLE raw_document ALTER COLUMN id SET DEFAULT uuid_v7();
ALTER TABLE news_item ALTER COLUMN id SET DEFAULT uuid_v7();
ALTER TABLE story_cluster ALTER COLUMN id SET DEFAULT uuid_v7();
ALTER TABLE fact ALTER COLUMN id SET DEFAULT uuid_v7();

-- ---------------------------------------------------------------------------
-- source: add crawl-state columns for conditional RSS requests
-- ---------------------------------------------------------------------------
ALTER TABLE source ADD COLUMN IF NOT EXISTS etag text;
ALTER TABLE source ADD COLUMN IF NOT EXISTS last_modified text;
ALTER TABLE source ADD COLUMN IF NOT EXISTS last_poll_at timestamptz;
ALTER TABLE source ADD COLUMN IF NOT EXISTS next_poll_at timestamptz;

CREATE UNIQUE INDEX IF NOT EXISTS source_url_key ON source (url);

-- ---------------------------------------------------------------------------
-- raw_document: immutable fetched payload (RSS feed body)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS raw_document (
    id              uuid PRIMARY KEY DEFAULT uuid_v7(),
    source_id       uuid NOT NULL REFERENCES source(id) ON DELETE CASCADE,
    url             text NOT NULL,
    fetch_status    integer NOT NULL,
    headers         jsonb NOT NULL DEFAULT '{}',
    body_text       text,
    content_type    text,
    fetched_at      timestamptz NOT NULL DEFAULT now(),
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS raw_document_source_id_idx ON raw_document (source_id);
CREATE INDEX IF NOT EXISTS raw_document_fetched_at_idx ON raw_document (fetched_at DESC);

-- ---------------------------------------------------------------------------
-- news_item: add enrichment + clustering + body-fetch columns
-- ---------------------------------------------------------------------------
ALTER TABLE news_item ADD COLUMN IF NOT EXISTS raw_document_id uuid REFERENCES raw_document(id) ON DELETE SET NULL;
ALTER TABLE news_item ADD COLUMN IF NOT EXISTS author text;
ALTER TABLE news_item ADD COLUMN IF NOT EXISTS content_html text;
ALTER TABLE news_item ADD COLUMN IF NOT EXISTS content_markdown text;
ALTER TABLE news_item ADD COLUMN IF NOT EXISTS language text;
ALTER TABLE news_item ADD COLUMN IF NOT EXISTS topics text[] NOT NULL DEFAULT '{}';
ALTER TABLE news_item ADD COLUMN IF NOT EXISTS entities text[] NOT NULL DEFAULT '{}';
ALTER TABLE news_item ADD COLUMN IF NOT EXISTS quality_score double precision;

-- Body-fetch status CHECK (RSS-only pipeline: items go straight to 'fetched'
-- or 'skipped' — no 'fetching' transition, but the constraint accepts all
-- values for compatibility with the shared scripts).
ALTER TABLE news_item DROP CONSTRAINT IF EXISTS news_item_body_fetch_status_check;
ALTER TABLE news_item ADD CONSTRAINT news_item_body_fetch_status_check
    CHECK (body_fetch_status IN ('pending', 'fetching', 'fetched', 'failed', 'skipped'));

ALTER TABLE news_item ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();

-- Unique indexes for dedup (same as Hudson schema)
CREATE UNIQUE INDEX IF NOT EXISTS news_item_source_ext_id_key
    ON news_item (source_id, external_id);
CREATE UNIQUE INDEX IF NOT EXISTS news_item_source_canonical_hash_key
    ON news_item (source_id, canonical_url_hash) WHERE canonical_url_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS news_item_content_hash_idx
    ON news_item (content_hash) WHERE content_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS news_item_simhash_idx
    ON news_item (simhash) WHERE simhash IS NOT NULL;

-- Replace ivfflat embedding index with hnsw for better recall (same as Hudson)
DROP INDEX IF EXISTS idx_news_item_embedding;
CREATE INDEX IF NOT EXISTS news_item_embedding_idx
    ON news_item USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64)
    WHERE embedding IS NOT NULL;

-- ---------------------------------------------------------------------------
-- story_cluster: add clustering columns
-- ---------------------------------------------------------------------------
ALTER TABLE story_cluster ADD COLUMN IF NOT EXISTS cluster_key text;
ALTER TABLE story_cluster ADD COLUMN IF NOT EXISTS representative_title text;
ALTER TABLE story_cluster ADD COLUMN IF NOT EXISTS first_seen_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE story_cluster ADD COLUMN IF NOT EXISTS last_seen_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE story_cluster ADD COLUMN IF NOT EXISTS item_ids uuid[] NOT NULL DEFAULT '{}';
ALTER TABLE story_cluster ADD COLUMN IF NOT EXISTS importance_score double precision;
ALTER TABLE story_cluster ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();

CREATE UNIQUE INDEX IF NOT EXISTS story_cluster_cluster_key_key
    ON story_cluster (cluster_key);

-- FK: news_item.cluster_id -> story_cluster.id
ALTER TABLE news_item
    DROP CONSTRAINT IF EXISTS news_item_cluster_id_fkey;
ALTER TABLE news_item
    ADD CONSTRAINT news_item_cluster_id_fkey
    FOREIGN KEY (cluster_id) REFERENCES story_cluster(id) ON DELETE SET NULL;

-- ---------------------------------------------------------------------------
-- fact: add extraction columns + idempotency constraint
-- ---------------------------------------------------------------------------
ALTER TABLE fact ADD COLUMN IF NOT EXISTS entities text[] NOT NULL DEFAULT '{}';
ALTER TABLE fact ADD COLUMN IF NOT EXISTS topics text[] NOT NULL DEFAULT '{}';
ALTER TABLE fact ADD COLUMN IF NOT EXISTS extracted_at timestamptz NOT NULL DEFAULT now();

-- Idempotency: unique on (news_item_id, md5(fact_text)) so re-running
-- fact extraction doesn't duplicate facts.
CREATE UNIQUE INDEX IF NOT EXISTS fact_news_item_fact_hash_key
    ON fact (news_item_id, md5(fact_text));

-- ---------------------------------------------------------------------------
-- updated_at triggers
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS source_updated_at ON source;
CREATE TRIGGER source_updated_at BEFORE UPDATE ON source
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

DROP TRIGGER IF EXISTS news_item_updated_at ON news_item;
CREATE TRIGGER news_item_updated_at BEFORE UPDATE ON news_item
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

DROP TRIGGER IF EXISTS story_cluster_updated_at ON story_cluster;
CREATE TRIGGER story_cluster_updated_at BEFORE UPDATE ON story_cluster
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------------------
-- player: Sleeper player database (synced daily by the players sync)
-- ---------------------------------------------------------------------------
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

CREATE INDEX IF NOT EXISTS idx_player_position ON player(position);
CREATE INDEX IF NOT EXISTS idx_player_team ON player(team);
CREATE INDEX IF NOT EXISTS idx_player_active ON player(active) WHERE active = true;
CREATE INDEX IF NOT EXISTS idx_player_status ON player(status);

DROP TRIGGER IF EXISTS player_updated_at ON player;
CREATE TRIGGER player_updated_at BEFORE UPDATE ON player
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------------------
-- player_ranking: Dynasty Daddy rankings (synced daily by the rankings sync)
-- Stores BOTH 1QB (trade_value/overall_rank) and Superflex
-- (sf_trade_value/sf_overall_rank) columns per player.
-- ---------------------------------------------------------------------------
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

-- ---------------------------------------------------------------------------
-- SimHash Hamming distance function (used by the dedup check)
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION hamming_distance(a bigint, b bigint)
RETURNS integer AS $$
DECLARE
    x bigint;
    c integer;
BEGIN
    x := a # b;
    c := 0;
    WHILE x > 0 LOOP
        c := c + 1;
        x := x & (x - 1);
    END LOOP;
    RETURN c;
END;
$$ LANGUAGE plpgsql IMMUTABLE;
-- ---------------------------------------------------------------------------
-- Redraft value column (single-season trade value, from FantasyCalc redraftValue)
-- Lets the agent value the redraft league (Poor Life Choices) properly and
-- compute the dynasty<redraft rebuild diagnostic + contender-validation check.
-- Safe/idempotent: ADD COLUMN IF NOT EXISTS; nullable (NULL for sources
-- that don't provide redraft values).
-- ---------------------------------------------------------------------------
ALTER TABLE player_ranking ADD COLUMN IF NOT EXISTS redraft_value integer;

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

-- ---------------------------------------------------------------------------
-- fact.occurred_at: repurpose as the article RSS published_at (timestamptz)
-- so facts are reliably filterable by when the news occurred. Was the LLM
-- event date (often NULL / future / hallucinated) — now always the article
-- publish timestamp. Type date -> timestamptz for precise window filtering.
-- ---------------------------------------------------------------------------
ALTER TABLE fact ALTER COLUMN occurred_at TYPE timestamptz USING occurred_at::timestamptz;

-- fact.occurred_at is non-nullable: every fact is rooted in time (article
-- published_at, else item created_at). A fact true 2 years ago may not be true today.
ALTER TABLE fact ALTER COLUMN occurred_at SET NOT NULL;

-- ---------------------------------------------------------------------------
-- fp_note_check — track when each FantasyPros player was last checked for an
-- Expert Note, so the scraper can cycle through all ~668 players politely
-- (a bounded batch per run, skipping recently-checked) and stay under the cron
-- timeout without triggering FantasyPros bot protection.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS fp_note_check (
    slug            text PRIMARY KEY,
    player_name     text,
    ecr             integer,
    last_checked_at timestamptz NOT NULL DEFAULT now(),
    has_note        boolean NOT NULL DEFAULT false
);
CREATE INDEX IF NOT EXISTS idx_fp_note_check_due ON fp_note_check(last_checked_at);
