package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/commonhuman-lab/chcrawl/internal/config"
	"github.com/commonhuman-lab/chcrawl/internal/output"
)

func TestCrawl_DiscoverOpenAPI_WritesOpenAPIEvent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>leaf</body></html>`))
	})
	mux.HandleFunc("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"openapi": "3.0.0",
			"paths": {
				"/pets": {"get": {"responses": {"200": {"description": "ok"}}}}
			}
		}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg, err := config.New(srv.URL+"/",
		config.WithConcurrency(2),
		config.WithPerHostConcurrency(2),
		config.WithMaxPages(5),
		config.WithTimeout(5*time.Second),
		config.WithDiscoverOpenAPI(true),
	)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	var buf bytes.Buffer
	eng, err := New(cfg, output.NewWriter(&buf))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	summary, err := eng.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.OpenAPIEndpoints != 1 {
		t.Errorf("expected 1 OpenAPI endpoint discovered, got %d", summary.OpenAPIEndpoints)
	}

	found := false
	for _, line := range strings.Split(buf.String(), "\n") {
		if line == "" {
			continue
		}
		var evt map[string]interface{}
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			t.Fatalf("invalid JSONL line %q: %v", line, err)
		}
		if evt["type"] == "openapi" {
			found = true
			endpoints, _ := evt["endpoints"].([]interface{})
			if len(endpoints) != 1 {
				t.Errorf("expected 1 endpoint in the openapi event, got %v", endpoints)
			}
		}
	}
	if !found {
		t.Error("expected an 'openapi' JSONL record in the output")
	}
}

func TestCrawl_DiscoverOpenAPI_Disabled_NoEvent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>leaf</body></html>`))
	})
	mux.HandleFunc("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		t.Error("openapi.json should never be probed when DiscoverOpenAPI is off")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg, err := config.New(srv.URL+"/", config.WithConcurrency(2),
		config.WithPerHostConcurrency(2), config.WithMaxPages(5), config.WithTimeout(5*time.Second))
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	var buf bytes.Buffer
	eng, err := New(cfg, output.NewWriter(&buf))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := eng.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestCrawl_RecoverSourceMaps_RecordsSourceMapDiscovery(t *testing.T) {
	sourceMap := `{"version":3,"sources":["app.ts"],"sourcesContent":["console.log('orig')"]}`

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><script src="/app.js"></script></body></html>`))
	})
	mux.HandleFunc("/app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Write([]byte("console.log(1);\n//# sourceMappingURL=app.js.map\n"))
	})
	mux.HandleFunc("/app.js.map", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sourceMap))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg, err := config.New(srv.URL+"/",
		config.WithConcurrency(2),
		config.WithPerHostConcurrency(2),
		config.WithMaxPages(10),
		config.WithTimeout(5*time.Second),
		config.WithRecoverSourceMaps(true),
	)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	var buf bytes.Buffer
	eng, err := New(cfg, output.NewWriter(&buf))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	summary, err := eng.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.SourceMapsRecovered != 1 {
		t.Errorf("expected 1 source recovered, got %d", summary.SourceMapsRecovered)
	}
}

func TestCrawl_RecoverSourceMaps_Disabled_NoMapFetch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><script src="/app.js"></script></body></html>`))
	})
	mux.HandleFunc("/app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Write([]byte("console.log(1);\n//# sourceMappingURL=app.js.map\n"))
	})
	mux.HandleFunc("/app.js.map", func(w http.ResponseWriter, r *http.Request) {
		t.Error("app.js.map should never be fetched when RecoverSourceMaps is off")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg, err := config.New(srv.URL+"/", config.WithConcurrency(2),
		config.WithPerHostConcurrency(2), config.WithMaxPages(10), config.WithTimeout(5*time.Second))
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	var buf bytes.Buffer
	eng, err := New(cfg, output.NewWriter(&buf))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := eng.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
