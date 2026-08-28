// Package output streams crawl results as newline-delimited JSON.
package output

import (
	"time"

	"github.com/commonhuman-lab/chcrawl/internal/extract"
	"github.com/commonhuman-lab/chcrawl/internal/fetch"
	"github.com/commonhuman-lab/chcrawl/internal/openapi"
)

// PageEvent records one successfully fetched and parsed page.
type PageEvent struct {
	Type          string              `json:"type"`
	Timestamp     time.Time           `json:"ts"`
	URL           string              `json:"url"`
	FinalURL      string              `json:"final_url,omitempty"`
	Depth         int                 `json:"depth"`
	Status        int                 `json:"status"`
	ContentType   string              `json:"content_type,omitempty"`
	BytesRead     int                 `json:"bytes_read"`
	Truncated     bool                `json:"truncated,omitempty"`
	RedirectChain []fetch.RedirectHop `json:"redirect_chain,omitempty"`
	Discoveries   []extract.Discovery `json:"discoveries,omitempty"`
	FetchMS       int64               `json:"fetch_ms"`
	RetryAttempts int                 `json:"retry_attempts,omitempty"`
	RetryDelayMS  int64               `json:"retry_delay_ms,omitempty"`
}

// ErrorEvent records a failure at some pipeline stage for one URL.
type ErrorEvent struct {
	Type          string `json:"type"`
	URL           string `json:"url"`
	Stage         string `json:"stage"` // "fetch", "extract", "robots"
	Error         string `json:"error"`
	RetryAttempts int    `json:"retry_attempts,omitempty"`
	RetryDelayMS  int64  `json:"retry_delay_ms,omitempty"`
}

// OpenAPIEvent records the result of an optional OpenAPI/Swagger discovery
// pass against the crawl's target origin.
type OpenAPIEvent struct {
	Type      string             `json:"type"`
	SourceURL string             `json:"source_url"`
	Endpoints []openapi.Endpoint `json:"endpoints"`
}

// SummaryEvent is the final record emitted at the end of a crawl run,
// satisfying every metric called for in the project brief.
type SummaryEvent struct {
	Type                string        `json:"type"`
	Seed                string        `json:"seed"`
	Partial             bool          `json:"partial,omitempty"`
	Duration            time.Duration `json:"duration_ns"`
	DurationHuman       string        `json:"duration"`
	URLsDiscovered      int64         `json:"urls_discovered_total"`
	URLsUnique          int64         `json:"urls_unique"`
	URLsInScope         int64         `json:"urls_in_scope_unique"`
	Endpoints           int64         `json:"endpoints_discovered"`
	Params              int64         `json:"params_discovered"`
	Forms               int64         `json:"forms_discovered"`
	JSFiles             int64         `json:"js_files_discovered"`
	JSRoutes            int64         `json:"js_routes_discovered"`
	RequestsMade        int64         `json:"requests_made"`
	ResponsesOK         int64         `json:"responses_ok"`
	ResponsesFailed     int64         `json:"responses_failed"`
	RedirectsFollowed   int64         `json:"redirects_followed"`
	DuplicatesRejected  int64         `json:"duplicates_rejected"`
	RobotsDisallowed    int64         `json:"robots_disallowed"`
	SourceMapsRecovered int64         `json:"source_maps_recovered"`
	OpenAPIEndpoints    int64         `json:"openapi_endpoints_discovered"`
	// ActiveWallMS is wall-clock Duration minus RetryBackoffMS (floored at 0,
	// not CPU time); concurrent retries can overlap on the wall clock while
	// RetryBackoffMS sums them independently, so it can exceed Duration.
	RetryAttempts int64 `json:"retry_attempts"`
	// RetryBackoff/ActiveWall are nanosecond precision; the MS fields are
	// rounded convenience copies. Prefer the ns fields for statistics — ms
	// rounding distorts sub-millisecond workloads.
	RetryBackoff   time.Duration `json:"retry_backoff_ns"`
	ActiveWall     time.Duration `json:"active_wall_ns"`
	RetryBackoffMS int64         `json:"retry_backoff_ms"`
	ActiveWallMS   int64         `json:"active_wall_ms"`

	URLsPerSec              float64 `json:"urls_per_sec"`
	UsefulDiscoveriesPerSec float64 `json:"useful_unique_discoveries_per_sec"`
	PeakMemoryBytes         uint64  `json:"peak_memory_bytes"`
}
