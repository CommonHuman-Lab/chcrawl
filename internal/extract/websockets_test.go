package extract

import (
	"context"
	"net/url"
	"testing"

	"github.com/commonhuman-lab/chcrawl/internal/fetch"
)

func TestWebSocketExtractor_AbsoluteAndRelativeForms(t *testing.T) {
	src := `
		const a = new WebSocket("wss://example.com/socket");
		const b = "wss://cdn.example.com/live";
		const c = new WebSocket("/ws/chat");
	`
	baseURL, _ := url.Parse("https://example.com/app.js")
	resp := &fetch.Response{ContentType: "application/javascript", Body: []byte(src)}
	got, err := WebSocketExtractor{}.Extract(context.Background(), Input{Resp: resp, BaseURL: baseURL})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	urls := map[string]bool{}
	for _, d := range got {
		urls[d.URL] = true
	}
	for _, want := range []string{"wss://example.com/socket", "wss://cdn.example.com/live", "wss://example.com/ws/chat"} {
		if !urls[want] {
			t.Errorf("expected %q among discoveries, got %+v", want, got)
		}
	}
}

func TestWebSocketExtractor_HTTPBaseGivesWSNotWSS(t *testing.T) {
	baseURL, _ := url.Parse("http://example.com/app.js")
	resp := &fetch.Response{ContentType: "application/javascript", Body: []byte(`new WebSocket("/ws")`)}
	got, err := WebSocketExtractor{}.Extract(context.Background(), Input{Resp: resp, BaseURL: baseURL})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got) != 1 || got[0].URL != "ws://example.com/ws" {
		t.Errorf("expected ws://example.com/ws for an http base, got %+v", got)
	}
}
