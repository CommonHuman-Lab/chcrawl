package engine

import (
	"sync/atomic"
	"time"

	"github.com/commonhuman-lab/chcrawl/internal/output"
)

// Stats holds the atomic counters that back the final SummaryEvent. Every
// field is updated at exactly one well-defined pipeline point (see
// pipeline.go) so the numbers stay consistent under concurrent workers.
type Stats struct {
	urlsDiscovered      atomic.Int64
	urlsUnique          atomic.Int64
	urlsInScope         atomic.Int64
	endpoints           atomic.Int64
	params              atomic.Int64
	forms               atomic.Int64
	jsFiles             atomic.Int64
	jsRoutes            atomic.Int64
	requestsMade        atomic.Int64
	responsesOK         atomic.Int64
	responsesFailed     atomic.Int64
	redirectsFollowed   atomic.Int64
	duplicatesRejected  atomic.Int64
	robotsDisallowed    atomic.Int64
	pagesInBudget       atomic.Int64
	sourceMapsRecovered atomic.Int64
	openAPIEndpoints    atomic.Int64
	retryAttempts       atomic.Int64
	retryBackoffNS      atomic.Int64
}

func (s *Stats) Snapshot(seed string, start time.Time, partial bool) output.SummaryEvent {
	dur := time.Since(start)
	secs := dur.Seconds()
	discovered := s.urlsDiscovered.Load()
	inScope := s.urlsInScope.Load()

	var urlsPerSec, usefulPerSec float64
	if secs > 0 {
		urlsPerSec = float64(discovered) / secs
		usefulPerSec = float64(inScope) / secs
	}

	backoff := time.Duration(s.retryBackoffNS.Load())
	activeWall := dur - backoff
	if activeWall < 0 {
		activeWall = 0
	}
	backoffMS := backoff.Milliseconds()
	activeWallMS := activeWall.Milliseconds()

	return output.SummaryEvent{
		Seed:                    seed,
		Partial:                 partial,
		Duration:                dur,
		DurationHuman:           dur.String(),
		URLsDiscovered:          discovered,
		URLsUnique:              s.urlsUnique.Load(),
		URLsInScope:             inScope,
		Endpoints:               s.endpoints.Load(),
		Params:                  s.params.Load(),
		Forms:                   s.forms.Load(),
		JSFiles:                 s.jsFiles.Load(),
		JSRoutes:                s.jsRoutes.Load(),
		RequestsMade:            s.requestsMade.Load(),
		ResponsesOK:             s.responsesOK.Load(),
		ResponsesFailed:         s.responsesFailed.Load(),
		RedirectsFollowed:       s.redirectsFollowed.Load(),
		DuplicatesRejected:      s.duplicatesRejected.Load(),
		RobotsDisallowed:        s.robotsDisallowed.Load(),
		SourceMapsRecovered:     s.sourceMapsRecovered.Load(),
		OpenAPIEndpoints:        s.openAPIEndpoints.Load(),
		RetryAttempts:           s.retryAttempts.Load(),
		RetryBackoff:            backoff,
		ActiveWall:              activeWall,
		RetryBackoffMS:          backoffMS,
		ActiveWallMS:            activeWallMS,
		URLsPerSec:              urlsPerSec,
		UsefulDiscoveriesPerSec: usefulPerSec,
	}
}
