-- Force re-chunking of news items whose existing chunks exceed the new
-- 6000-char per-chunk budget. Setting chunked_content_hash = NULL makes
-- the chunker pick them up again (it re-chunks when chunked_content_hash
-- IS DISTINCT FROM content_hash), and the new SplitLongChunks logic will
-- split oversized sections into multiple sub-chunks.
UPDATE news_item
   SET chunked_content_hash = NULL
 WHERE id IN (
     SELECT nc.news_item_id
       FROM news_chunk nc
      WHERE length(nc.chunk_text) > 6000
 );