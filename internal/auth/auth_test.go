package auth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/commonhuman-lab/chcrawl/fetch"
)

func newTestFetcher(t *testing.T) fetch.Fetcher {
	t.Helper()
	f, err := fetch.New(fetch.Config{
		Timeout:             5 * time.Second,
		MaxBodyBytes:        1 << 20,
		AllowedContentTypes: []string{"html", "json"},
	})
	if err != nil {
		t.Fatalf("fetch.New: %v", err)
	}
	return f
}

func TestFormLogin_SubmitsHiddenFieldsAndCredentials(t *testing.T) {
	var gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<html><body>
				<form action="/do-login" method="post">
					<input type="hidden" name="csrf_token" value="abc123">
					<input type="text" name="username">
					<input type="password" name="password">
				</form>
			</body></html>`))
			return
		}
	})
	mux.HandleFunc("/do-login", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Set-Cookie", "session=xyz789")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	result, err := FormLogin(context.Background(), newTestFetcher(t), srv.URL+"/login", "username", "alice", "password", "hunter2", nil)
	if err != nil {
		t.Fatalf("FormLogin: %v", err)
	}
	if result.Cookies != "session=xyz789" {
		t.Errorf("expected session cookie to be captured, got %q", result.Cookies)
	}
	if !strings.Contains(gotBody, "csrf_token=abc123") {
		t.Errorf("expected hidden csrf_token to be replayed, got body %q", gotBody)
	}
	if !strings.Contains(gotBody, "username=alice") || !strings.Contains(gotBody, "password=hunter2") {
		t.Errorf("expected credentials in submitted body, got %q", gotBody)
	}
}

func TestFormLogin_ExtractsBearerTokenFromJSONResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<form method="post"><input name="u"><input name="p"></form>`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"tok-xyz"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	result, err := FormLogin(context.Background(), newTestFetcher(t), srv.URL+"/login", "u", "alice", "p", "hunter2", nil)
	if err != nil {
		t.Fatalf("FormLogin: %v", err)
	}
	if result.Headers["Authorization"] != "Bearer tok-xyz" {
		t.Errorf("expected Authorization: Bearer tok-xyz, got %q", result.Headers["Authorization"])
	}
}

func TestBearerLogin_ClientCredentials(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "grant_type=client_credentials") {
			t.Errorf("expected grant_type=client_credentials in body, got %q", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"token":"abc"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	result, err := BearerLogin(context.Background(), newTestFetcher(t), srv.URL+"/token", "id", "secret", "")
	if err != nil {
		t.Fatalf("BearerLogin: %v", err)
	}
	if result.Headers["Authorization"] != "Bearer abc" {
		t.Errorf("expected Authorization: Bearer abc, got %q", result.Headers["Authorization"])
	}
}

func TestBasicAuthHeader(t *testing.T) {
	h, err := BasicAuthHeader("alice:pass:with:colons")
	if err != nil {
		t.Fatalf("BasicAuthHeader: %v", err)
	}
	want := "Basic YWxpY2U6cGFzczp3aXRoOmNvbG9ucw=="
	if h["Authorization"] != want {
		t.Errorf("got %q, want %q", h["Authorization"], want)
	}

	if _, err := BasicAuthHeader("no-colon-here"); err == nil {
		t.Errorf("expected error for credential with no colon")
	}
}
