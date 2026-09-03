package engine

import (
	"context"

	"github.com/commonhuman-lab/chcrawl/openapi"
	"github.com/commonhuman-lab/chcrawl/output"
)

// discoverOpenAPI runs a one-shot OpenAPI/Swagger probe against the seed's origin, layered on
// top of the crawl rather than triggered per-page, and writes any spec found as an "openapi" record.
func (e *Engine) discoverOpenAPI(ctx context.Context) {
	origin := e.seedURL.Scheme + "://" + e.seedURL.Host
	spec, err := openapi.Discover(ctx, e.openapiFetcher, origin)
	if err != nil {
		_ = e.writer.WriteError(output.ErrorEvent{URL: origin, Stage: "openapi", Error: err.Error()})
		return
	}
	if spec == nil {
		return
	}
	e.stats.openAPIEndpoints.Add(int64(len(spec.Endpoints)))
	_ = e.writer.WriteOpenAPI(output.OpenAPIEvent{SourceURL: spec.SourceURL, Endpoints: spec.Endpoints})
}
