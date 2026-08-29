package extract

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"sync"

	"github.com/commonhuman-lab/chcrawl/internal/fetch"
)

// These patterns match an absolute wss?:// literal (optionally inside a
// `new WebSocket(...)` call), or a relative "/path" passed to
// `new WebSocket(...)`.
//
// Compilation is deferred to initWebSocketRegexes, called only after
// Extract's cheap byte-scan already found "WebSocket"/"ws://"/"wss://" in
// the body — most pages contain none of those and never pay for compiling
// these 3 regexes at all.
var (
	wsNewAbsoluteRe  *regexp.Regexp
	wsBareAbsoluteRe *regexp.Regexp
	wsNewRelativeRe  *regexp.Regexp
)

var initWebSocketRegexes = sync.OnceFunc(func() {
	wsNewAbsoluteRe = regexp.MustCompile(`new\s+WebSocket\s*\(\s*["'` + "`" + `](wss?://[^"'` + "`" + `\s)]+)["'` + "`" + `]`)
	wsBareAbsoluteRe = regexp.MustCompile(`["'` + "`" + `](wss?://[^"'` + "`" + `\s)]{4,})["'` + "`" + `]`)
	wsNewRelativeRe = regexp.MustCompile(`new\s+WebSocket\s*\(\s*["'` + "`" + `](/[^"'` + "`" + `\s)]+)["'` + "`" + `]`)
})

var metaWebSocket = map[string]string{"source": "websocket"}

// WebSocketExtractor finds WebSocket endpoint URLs referenced in HTML or JS
// source text.
type WebSocketExtractor struct{}

func (WebSocketExtractor) Name() string { return "websockets" }

func (WebSocketExtractor) Applies(resp *fetch.Response) bool {
	ct := strings.ToLower(resp.ContentType)
	return strings.Contains(ct, "html") || strings.Contains(ct, "javascript")
}

func (WebSocketExtractor) Extract(ctx context.Context, in Input) ([]Discovery, error) {
	body := in.Resp.Body
	if !bytes.Contains(body, []byte("WebSocket")) &&
		!bytes.Contains(body, []byte("ws://")) &&
		!bytes.Contains(body, []byte("wss://")) {
		return nil, nil
	}
	initWebSocketRegexes()

	src := string(in.Resp.Body)
	seen := map[string]bool{}
	var out []Discovery
	emit := func(u string) {
		if seen[u] {
			return
		}
		seen[u] = true
		out = append(out, Discovery{Kind: "ws_url", URL: u, Meta: metaWebSocket})
	}

	for _, m := range wsNewAbsoluteRe.FindAllStringSubmatch(src, -1) {
		emit(m[1])
	}
	for _, m := range wsBareAbsoluteRe.FindAllStringSubmatch(src, -1) {
		emit(m[1])
	}
	for _, m := range wsNewRelativeRe.FindAllStringSubmatch(src, -1) {
		wsScheme := "ws"
		if in.BaseURL.Scheme == "https" {
			wsScheme = "wss"
		}
		emit(wsScheme + "://" + in.BaseURL.Host + m[1])
	}

	return out, nil
}
