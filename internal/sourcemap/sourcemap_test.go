package sourcemap

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/commonhuman-lab/chcrawl/fetch"
)

func newTestFetcher(t *testing.T) fetch.Fetcher {
	t.Helper()
	f, err := fetch.New(fetch.Config{Timeout: 5 * time.Second, MaxBodyBytes: 1 << 20, AllowedContentTypes: nil})
	if err != nil {
		t.Fatalf("fetch.New: %v", err)
	}
	return f
}

func TestFetch_RemoteMapURL(t *testing.T) {
	mapDoc := sourceMapJSON{
		Version:        3,
		Sources:        []string{"src/app.ts", "src/lib/util.ts", "node_modules/foo/index.js"},
		SourcesContent: []string{"console.log('app')", "", "module.exports = {}"},
	}
	mapJSON, _ := json.Marshal(mapDoc)

	mux := http.NewServeMux()
	mux.HandleFunc("/app.js.map", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(mapJSON)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	jsBody := []byte("console.log(1);\n//# sourceMappingURL=app.js.map\n")
	result, err := Fetch(context.Background(), newTestFetcher(t), srv.URL+"/app.js", jsBody)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if result == nil {
		t.Fatal("expected a non-nil Result")
	}
	if got := result.Sources["src/app.ts"]; got != "console.log('app')" {
		t.Errorf("src/app.ts recovered content = %q", got)
	}
	if _, ok := result.Sources["node_modules/foo/index.js"]; ok {
		t.Error("node_modules paths should be filtered out as noise")
	}
	// a non-noise path with no recovered content (empty sourcesContent
	// entry) should still appear in Mapping, just without Sources text.
	found := false
	for _, m := range result.Mapping {
		if m == "src/lib/util.ts" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected src/lib/util.ts in Mapping, got %v", result.Mapping)
	}
}

func TestFetch_NoisePatternsFiltered(t *testing.T) {
	mapDoc := sourceMapJSON{
		Sources: []string{
			"app.ts",
			"node_modules/react/index.js",
			"webpack/runtime/module.js",
			"foo.spec.ts",
			"bar.test.js",
			"/vendor/lib.js",
		},
	}
	mapJSON, _ := json.Marshal(mapDoc)
	result, err := parse(mapJSON)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(result.Mapping) != 1 || result.Mapping[0] != "app.ts" {
		t.Errorf("expected only app.ts to survive noise filtering, got %v", result.Mapping)
	}
}

func TestFetch_InlineDataURI(t *testing.T) {
	mapDoc := sourceMapJSON{
		Sources:        []string{"inline.ts"},
		SourcesContent: []string{"const x = 1;"},
	}
	mapJSON, _ := json.Marshal(mapDoc)
	encoded := base64.StdEncoding.EncodeToString(mapJSON)
	jsBody := []byte("var x=1;\n//# sourceMappingURL=data:application/json;base64," + encoded + "\n")

	result, err := Fetch(context.Background(), newTestFetcher(t), "https://example.com/app.js", jsBody)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if result.Sources["inline.ts"] != "const x = 1;" {
		t.Errorf("expected inline source recovered, got %+v", result.Sources)
	}
}

func TestFetch_NoSourceMappingURL(t *testing.T) {
	result, err := Fetch(context.Background(), newTestFetcher(t), "https://example.com/app.js", []byte("console.log(1);"))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil Result when no sourceMappingURL comment is present, got %+v", result)
	}
}

func TestFetch_SourceMappingURLOnlySearchedInLast4096Bytes(t *testing.T) {
	padding := strings.Repeat("x", searchWindowBytes+100)
	jsBody := []byte("//# sourceMappingURL=should-not-be-found.js.map\n" + padding)

	result, err := Fetch(context.Background(), newTestFetcher(t), "https://example.com/app.js", jsBody)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if result != nil {
		t.Errorf("expected the comment outside the last %d bytes to be missed, got %+v", searchWindowBytes, result)
	}
}

func TestFetch_SourceRootJoined(t *testing.T) {
	mapDoc := sourceMapJSON{
		SourceRoot:     "/src",
		Sources:        []string{"app.ts"},
		SourcesContent: []string{"x"},
	}
	mapJSON, _ := json.Marshal(mapDoc)
	result, err := parse(mapJSON)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := result.Sources["/src/app.ts"]; !ok {
		t.Errorf("expected sourceRoot joined with source path, got %+v", result.Sources)
	}
}
