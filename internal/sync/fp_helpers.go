package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"ff-engine/internal/config"
)

// fetchFPJSON issues an authenticated GET request against a FantasyPros API
// endpoint and decodes the JSON response into out. reqErrCtx and
// decodeErrCtx customize the wrapped error messages for the request and
// decode steps; httpErr is the sentinel error used for non-200 responses.
func fetchFPJSON(
	ctx context.Context, client *http.Client, cfg *config.Config, url string,
	httpErr error, reqErrCtx, decodeErrCtx string, out any,
) error {
	apiKey, err := config.ReadSecret(cfg.FantasyPros.APIKeyPass)
	if err != nil {
		return fmt.Errorf("get FP API key: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("%s: build request: %w", reqErrCtx, err)
	}
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("Accept", "application/json")
	q := req.URL.Query()
	q.Add("limit", "500")
	req.URL.RawQuery = q.Encode()

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", reqErrCtx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			body = []byte("(unable to read error response body)")
		}
		return fmt.Errorf("%w (%d): %s", httpErr, resp.StatusCode, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%s: %w", decodeErrCtx, err)
	}
	return nil
}
