// Package sourcemap recovers original (pre-minification) JS source from
// .js.map files. It is a standalone utility, not wired into the core crawl
// loop — a caller invokes it per JS file it already knows about.
package sourcemap

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/commonhuman-lab/chcrawl/internal/fetch"
)

// sourceMappingRe finds a "//# sourceMappingURL=..." or
// "//@ sourceMappingURL=..." comment.
var sourceMappingRe = regexp.MustCompile(`//[#@]\s*sourceMappingURL=(\S+)`)

// noisePatterns filters out source paths that are never useful for
// endpoint-mining purposes: vendored/generated code, not application logic.
var noisePatterns = regexp.MustCompile(`node_modules/|webpack/runtime|__webpack_require__|\.spec\.[jt]s$|\.test\.[jt]s$|/vendor/`)

// searchWindowBytes bounds the sourceMappingURL search to the last 4096
// bytes of the file, since the comment is always near the end of a
// minified bundle.
const searchWindowBytes = 4096

// Result is the recovered source map data for one JS file.
type Result struct {
	// Sources maps each original (pre-minification) source path to its
	// recovered text, where available.
	Sources map[string]string
	// Mapping records, for the one JS URL this Result was built for, the
	// list of original source paths it maps to (in map-file order).
	Mapping []string
}

// Len returns the number of recovered sources.
func (r *Result) Len() int { return len(r.Sources) }

type sourceMapJSON struct {
	Version        int      `json:"version"`
	Sources        []string `json:"sources"`
	SourcesContent []string `json:"sourcesContent"`
	SourceRoot     string   `json:"sourceRoot"`
}

// Fetch locates and recovers the source map for one already-fetched JS
// file. jsURL is the JS file's URL (used to resolve a relative map URL);
// jsBody is its already-downloaded content.
func Fetch(ctx context.Context, fetcher fetch.Fetcher, jsURL string, jsBody []byte) (*Result, error) {
	mapURLRaw, ok := findMapURL(jsBody)
	if !ok {
		return nil, nil
	}

	var raw []byte
	if strings.HasPrefix(mapURLRaw, "data:") {
		decoded, err := decodeDataURI(mapURLRaw)
		if err != nil {
			return nil, fmt.Errorf("sourcemap: decoding inline data URI: %w", err)
		}
		raw = decoded
	} else {
		base, err := url.Parse(jsURL)
		if err != nil {
			return nil, fmt.Errorf("sourcemap: parsing JS URL: %w", err)
		}
		ref, err := url.Parse(mapURLRaw)
		if err != nil {
			return nil, fmt.Errorf("sourcemap: parsing map URL %q: %w", mapURLRaw, err)
		}
		mapURL := base.ResolveReference(ref).String()

		resp, err := fetcher.Fetch(ctx, fetch.Request{URL: mapURL, Method: "GET"})
		if err != nil {
			return nil, fmt.Errorf("sourcemap: fetching %s: %w", mapURL, err)
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("sourcemap: %s returned status %d", mapURL, resp.StatusCode)
		}
		raw = resp.Body
	}

	return parse(raw)
}

func parse(raw []byte) (*Result, error) {
	var doc sourceMapJSON
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("sourcemap: invalid map JSON: %w", err)
	}

	result := &Result{Sources: map[string]string{}}
	for i, src := range doc.Sources {
		path := src
		if doc.SourceRoot != "" {
			path = joinSourceRoot(doc.SourceRoot, src)
		}
		if noisePatterns.MatchString(path) {
			continue
		}
		result.Mapping = append(result.Mapping, path)
		if i < len(doc.SourcesContent) && doc.SourcesContent[i] != "" {
			result.Sources[path] = doc.SourcesContent[i]
		}
	}
	return result, nil
}

func joinSourceRoot(root, src string) string {
	root = strings.TrimRight(root, "/")
	src = strings.TrimLeft(src, "/")
	if root == "" {
		return src
	}
	return root + "/" + src
}

func findMapURL(body []byte) (string, bool) {
	window := body
	if len(window) > searchWindowBytes {
		window = window[len(window)-searchWindowBytes:]
	}
	m := sourceMappingRe.FindSubmatch(window)
	if m == nil {
		return "", false
	}
	return string(m[1]), true
}

func decodeDataURI(uri string) ([]byte, error) {
	i := strings.Index(uri, ",")
	if i == -1 {
		return nil, fmt.Errorf("malformed data URI")
	}
	meta, payload := uri[:i], uri[i+1:]
	if !strings.Contains(meta, "base64") {
		return nil, fmt.Errorf("unsupported (non-base64) data URI encoding")
	}
	return base64.StdEncoding.DecodeString(payload)
}
