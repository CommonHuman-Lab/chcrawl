package extract

import (
	"context"
	"strconv"
	"strings"

	"github.com/commonhuman-lab/chcrawl/fetch"
	"github.com/commonhuman-lab/chcrawl/internal/sourcemap"
)

// SourceMapExtractor recovers original (pre-minification) source for JS files that reference a
// .js.map, via internal/sourcemap. Unlike the other extractors it needs a Fetcher, since it may
// fetch a remote map file itself. Recovered sources are reported as an informational Discovery
// (Meta only), never enqueued as crawl targets — they're typically bare module paths, not routes.
type SourceMapExtractor struct {
	Fetcher fetch.Fetcher
}

func (SourceMapExtractor) Name() string { return "source_map" }

func (SourceMapExtractor) Applies(resp *fetch.Response) bool {
	return strings.Contains(strings.ToLower(resp.ContentType), "javascript")
}

func (e SourceMapExtractor) Extract(ctx context.Context, in Input) ([]Discovery, error) {
	result, err := sourcemap.Fetch(ctx, e.Fetcher, in.Resp.FinalURL, in.Resp.Body)
	if err != nil || result == nil || result.Len() == 0 {
		return nil, err
	}

	return []Discovery{{
		Kind: "source_map",
		Meta: map[string]string{
			"source":            "source_map",
			"js_url":            in.Resp.FinalURL,
			"sources_recovered": strconv.Itoa(len(result.Sources)),
			"sources_mapped":    strconv.Itoa(len(result.Mapping)),
		},
	}}, nil
}
