package output

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/commonhuman-lab/chcrawl/internal/extract"
	"github.com/commonhuman-lab/chcrawl/internal/fetch"
	"github.com/commonhuman-lab/chcrawl/internal/openapi"
)

func mustMarshalJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}

func TestAppendPageEventJSON_MatchesEncodingJSON(t *testing.T) {
	ts := time.Date(2026, 8, 29, 12, 30, 0, 123456789, time.UTC)
	cases := []PageEvent{
		{}, // zero value
		{
			Type: "page", Timestamp: ts, URL: "https://example.com/a?x=1",
			FinalURL: "https://example.com/a?x=1", Depth: 3, Status: 200,
			ContentType: "text/html; charset=utf-8", BytesRead: 4821, Truncated: true,
			FetchMS: 142, RetryAttempts: 2, RetryDelayMS: 500,
			RedirectChain: []fetch.RedirectHop{{URL: "https://example.com/", StatusCode: 301}},
			Discoveries: []extract.Discovery{
				{Kind: "link", URL: "https://example.com/b"},
				{Kind: "form", URL: "https://example.com/c", Method: "POST",
					Params: []extract.Param{{Name: "q", Value: "<script>&\"'"}},
					Base:   map[string]string{}, Meta: map[string]string{}},
				{Kind: "form", URL: "https://example.com/d", Method: "POST",
					Base: map[string]string{"z": "1", "a": "2", "m": "3"},
					Meta: map[string]string{"csrf": "tok en"}},
				{Kind: "link", URL: "https://example.com/e", Params: nil, Base: nil, Meta: nil},
			},
		},
		{
			// FinalURL equal to URL, but still non-empty, so it must still emit.
			URL: "https://example.com", FinalURL: "https://example.com",
		},
		{
			// RedirectChain/Discoveries explicitly empty-but-non-nil: omitted either way (len==0).
			RedirectChain: []fetch.RedirectHop{}, Discoveries: []extract.Discovery{},
		},
	}
	for i, e := range cases {
		got, err := appendPageEventJSON(nil, &e)
		if err != nil {
			t.Fatalf("case %d: appendPageEventJSON: %v", i, err)
		}
		want := mustMarshalJSON(t, e)
		if !bytes.Equal(got, want) {
			t.Errorf("case %d mismatch:\n got  %s\n want %s", i, got, want)
		}
	}
}

func TestAppendErrorEventJSON_MatchesEncodingJSON(t *testing.T) {
	cases := []ErrorEvent{
		{},
		{Type: "error", URL: "https://example.com/x", Stage: "fetch",
			Error: "boom: line1\nline2 with \"quotes\" and <html>&amp;", RetryAttempts: 3, RetryDelayMS: 1000},
		{Type: "error", URL: "https://example.com/y", Stage: "robots", Error: ""},
	}
	for i, e := range cases {
		got := appendErrorEventJSON(nil, &e)
		want := mustMarshalJSON(t, e)
		if !bytes.Equal(got, want) {
			t.Errorf("case %d mismatch:\n got  %s\n want %s", i, got, want)
		}
	}
}

func TestAppendOpenAPIEventJSON_MatchesEncodingJSON(t *testing.T) {
	cases := []OpenAPIEvent{
		{}, // Endpoints nil -> must be "endpoints":null, not omitted
		{Type: "openapi", SourceURL: "https://example.com/openapi.json", Endpoints: []openapi.Endpoint{}},
		{Type: "openapi", SourceURL: "https://example.com/openapi.json", Endpoints: []openapi.Endpoint{
			{URL: "https://example.com/api/users/{id}", Method: "GET",
				PathParams: []string{"id"}, QueryParams: nil, BodyParams: []string{},
				RawPath: "/api/users/{id}"},
		}},
	}
	for i, e := range cases {
		got := appendOpenAPIEventJSON(nil, &e)
		want := mustMarshalJSON(t, e)
		if !bytes.Equal(got, want) {
			t.Errorf("case %d mismatch:\n got  %s\n want %s", i, got, want)
		}
	}
}

func TestAppendSummaryEventJSON_MatchesEncodingJSON(t *testing.T) {
	cases := []SummaryEvent{
		{}, // zero value: Partial omitted, everything else present at 0
		{
			Type: "summary", Seed: "https://example.com", Partial: true,
			Duration: 5 * time.Second, DurationHuman: "5s",
			URLsDiscovered: 100, URLsUnique: 90, URLsInScope: 80,
			Endpoints: 10, Params: 5, Forms: 2, JSFiles: 3, JSRoutes: 1,
			RequestsMade: 95, ResponsesOK: 90, ResponsesFailed: 5,
			RedirectsFollowed: 4, DuplicatesRejected: 10, RobotsDisallowed: 2,
			SourceMapsRecovered: 1, OpenAPIEndpoints: 6, RetryAttempts: 7,
			RetryBackoff: 250 * time.Millisecond, ActiveWall: 4750 * time.Millisecond,
			RetryBackoffMS: 250, ActiveWallMS: 4750,
			URLsPerSec: 0, UsefulDiscoveriesPerSec: 1234.5, PeakMemoryBytes: 1 << 20,
		},
		{
			// exercise the 'e' float format branch and the e-09->e-9 trim
			URLsPerSec: 1e-8, UsefulDiscoveriesPerSec: 1e25,
		},
	}
	for i, e := range cases {
		got, err := appendSummaryEventJSON(nil, &e)
		if err != nil {
			t.Fatalf("case %d: appendSummaryEventJSON: %v", i, err)
		}
		want := mustMarshalJSON(t, e)
		if !bytes.Equal(got, want) {
			t.Errorf("case %d mismatch:\n got  %s\n want %s", i, got, want)
		}
	}
}

func TestAppendSummaryEventJSON_NaNInfMatchesEncodingJSONError(t *testing.T) {
	cases := []SummaryEvent{
		{URLsPerSec: math.NaN()},
		{URLsPerSec: math.Inf(1)},
		{UsefulDiscoveriesPerSec: math.Inf(-1)},
	}
	for i, e := range cases {
		_, gotErr := appendSummaryEventJSON(nil, &e)
		_, wantErr := json.Marshal(e)
		if (gotErr == nil) != (wantErr == nil) {
			t.Errorf("case %d: appendSummaryEventJSON err=%v, json.Marshal err=%v", i, gotErr, wantErr)
		}
	}
}
