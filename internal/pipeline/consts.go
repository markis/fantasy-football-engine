package pipeline

const (
	statusError   = "error"
	statusSkipped = "skipped"
	statusFetched = "fetched"

	// defaultClusterBatch bounds one clustering run when no job limit is
	// configured — each item scans all table embeddings, so runs must
	// stay bounded.
	defaultClusterBatch = 200
)
