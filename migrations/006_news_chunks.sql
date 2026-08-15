-- news_chunk: section-level chunks of news_item articles for granular embedding
CREATE TABLE IF NOT EXISTS news_chunk (
    id            UUID PRIMARY KEY DEFAULT uuid_v7(),
    news_item_id  UUID NOT NULL REFERENCES news_item(id) ON DELETE CASCADE,
    chunk_index   INTEGER NOT NULL,
    heading       TEXT,
    chunk_text    TEXT NOT NULL,
    embedding     vector(768),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(news_item_id, chunk_index)
);

CREATE INDEX IF NOT EXISTS news_chunk_embedding_idx
    ON news_chunk USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64)
    WHERE embedding IS NOT NULL;

CREATE INDEX IF NOT EXISTS news_chunk_news_item_id_idx
    ON news_chunk (news_item_id);

ALTER TABLE news_item ADD COLUMN IF NOT EXISTS chunked_content_hash TEXT;