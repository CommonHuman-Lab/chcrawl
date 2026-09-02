package engine

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/commonhuman-lab/chcrawl/config"
	"github.com/commonhuman-lab/chcrawl/output"
)

func TestEngine_CloseIsNoopWithoutRenderJS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html></html>`))
	}))
	defer srv.Close()

	cfg, err := config.New(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(cfg, output.NewWriter(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
}
