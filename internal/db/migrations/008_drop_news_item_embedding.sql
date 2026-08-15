-- Drop the legacy article-level embedding column and its indexes.
-- Article similarity (clustering, dedup) now uses news_chunk embeddings
-- directly via the HNSW index news_chunk_embedding_idx (see 006_news_chunks.sql).

DROP INDEX IF EXISTS news_item_embedding_idx;
DROP INDEX IF EXISTS idx_news_item_embedding;

ALTER TABLE news_item DROP COLUMN IF EXISTS embedding;