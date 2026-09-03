package fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetch_DelayAppliedBeforeEachRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	f, err := New(Config{Timeout: 5 * time.Second, MaxBodyBytes: 1024, Delay: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	start := time.Now()
	if _, err := f.Fetch(context.Background(), Request{URL: srv.URL}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if _, err := f.Fetch(context.Background(), Request{URL: srv.URL}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Errorf("expected at least 2x50ms delay across two fetches, elapsed only %s", elapsed)
	}
}

func TestFetch_CookiesSurvivesRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/set", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "session=abc123")
		http.Redirect(w, r, "/landing", http.StatusFound)
	})
	mux.HandleFunc("/landing", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("ok"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f, err := New(Config{Timeout: 5 * time.Second, MaxBodyBytes: 1024})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := f.Fetch(context.Background(), Request{URL: srv.URL + "/set"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if resp.Headers.Get("Set-Cookie") != "" {
		t.Fatalf("sanity check failed: the final response shouldn't itself carry Set-Cookie")
	}
	if resp.Cookies != "session=abc123" {
		t.Errorf("expected the redirect hop's cookie to survive via the jar, got Cookies=%q", resp.Cookies)
	}
}

func TestFetch_CookiesPersistAcrossRequestsOnSameFetcher(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/set", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "session=xyz")
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/other", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("cookie seen: " + r.Header.Get("Cookie")))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f, err := New(Config{Timeout: 5 * time.Second, MaxBodyBytes: 1024})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := f.Fetch(context.Background(), Request{URL: srv.URL + "/set"}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	resp, err := f.Fetch(context.Background(), Request{URL: srv.URL + "/other"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(string(resp.Body), "session=xyz") {
		t.Errorf("expected the jar-stored cookie from the first request to be sent on the second, got body %q", resp.Body)
	}
}

func TestFetch_NoDelayByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	f, err := New(Config{Timeout: 5 * time.Second, MaxBodyBytes: 1024})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	start := time.Now()
	if _, err := f.Fetch(context.Background(), Request{URL: srv.URL}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("expected a fast fetch with no delay configured, took %s", elapsed)
	}
}

func TestFetch_DelayRespectsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	f, err := New(Config{Timeout: 5 * time.Second, MaxBodyBytes: 1024, Delay: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = f.Fetch(ctx, Request{URL: srv.URL})
	if err == nil {
		t.Fatalf("expected the delay to be cut short by context cancellation, got no error")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("expected cancellation to cut the 5s delay short, took %s", elapsed)
	}
}
