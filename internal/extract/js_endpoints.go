package extract

import (
	"context"
	"regexp"
	"strings"

	"github.com/commonhuman-lab/chcrawl/internal/fetch"
)

// The regexes below deliberately require the matched path to contain
// "/rest/" or "/api/" — any endpoint that doesn't follow that convention is
// invisible to this extractor. A more general miner (matching arbitrary
// path conventions) is future work, not a drop-in replacement for these
// patterns.
var (
	methodTemplateRe = regexp.MustCompile("(?i)\\b(get|post|put|patch|delete)\\s*\\(\\s*`([^`]*/(?:rest|api)/[^`]*)`")
	staticPathRe     = regexp.MustCompile(`["'](/(?:rest|api)/[A-Za-z0-9/_\-?=&%]+)["']`)
	varAssignRe      = regexp.MustCompile(`(?:this\.)?(\w+)\s*=\s*[^;]+?["'](/(?:rest|api)/[A-Za-z0-9/_\-]+)["']`)
	varSuffixRe      = regexp.MustCompile(`(?i)\b(get|post|put|patch|delete)\s*\(\s*(?:this\.)?(\w+)\s*\+\s*["']([/A-Za-z0-9_\-]+)["']`)
	chunkFileRe      = regexp.MustCompile(`\b(chunk-[A-Za-z0-9_\-]+\.js)\b`)
	templateVarRe    = regexp.MustCompile(`\$\{([^}]*)\}`)

	hostVarHint = regexp.MustCompile(`(?i)host|server|url|base|origin`)
	idVarHint   = regexp.MustCompile(`(?i)id|num|page|limit|offset|count`)

	methodKeywordRe = regexp.MustCompile(`(?i)\b(get|post|put|patch|delete)\s*\(\s*$`)
	trailingAlphaRe = regexp.MustCompile(`^[A-Za-z]+$`)
)

var (
	metaJSEndpoint = map[string]string{"source": "js_endpoint"}
	metaJSChunk    = map[string]string{"source": "js_chunk", "asset": "js"}
)

// JSEndpointExtractor mines API endpoint candidates and webpack chunk
// filenames out of raw JavaScript source.
type JSEndpointExtractor struct{}

func (JSEndpointExtractor) Name() string { return "js_endpoints" }

func (JSEndpointExtractor) Applies(resp *fetch.Response) bool {
	return strings.Contains(strings.ToLower(resp.ContentType), "javascript")
}

func (JSEndpointExtractor) Extract(ctx context.Context, in Input) ([]Discovery, error) {
	src := string(in.Resp.Body)
	origin := in.BaseURL.Scheme + "://" + in.BaseURL.Host

	seen := map[string]bool{}
	var out []Discovery
	emit := func(method, absURL string) {
		key := method + ":" + absURL
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, Discovery{Kind: "link", URL: absURL, Method: method, Meta: metaJSEndpoint})
	}

	for _, m := range methodTemplateRe.FindAllStringSubmatch(src, -1) {
		method := strings.ToUpper(m[1])
		resolved := resolveTemplate(m[2], origin)
		emit(method, joinOrigin(origin, resolved))
	}

	for _, m := range staticPathRe.FindAllStringSubmatchIndex(src, -1) {
		path := src[m[2]:m[3]]
		method := inferMethod(src, m[0])
		emit(method, origin+path)
		if !strings.Contains(path, "?") {
			if segs := strings.Split(strings.TrimRight(path, "/"), "/"); len(segs) > 0 {
				last := segs[len(segs)-1]
				if trailingAlphaRe.MatchString(last) {
					emit(method, origin+path+"/1")
				}
			}
		}
	}

	varBases := map[string]string{}
	for _, m := range varAssignRe.FindAllStringSubmatch(src, -1) {
		varBases[m[1]] = m[2]
	}
	for _, m := range varSuffixRe.FindAllStringSubmatch(src, -1) {
		method, varName, suffix := strings.ToUpper(m[1]), m[2], m[3]
		base, ok := varBases[varName]
		if !ok {
			continue
		}
		combined := strings.TrimRight(base, "/") + "/" + strings.TrimLeft(suffix, "/")
		emit(method, origin+combined)
	}

	for _, m := range chunkFileRe.FindAllStringSubmatch(src, -1) {
		if abs, err := resolve(in.BaseURL, m[1]); err == nil {
			out = append(out, Discovery{Kind: "link", URL: abs, Meta: metaJSChunk})
		}
	}

	return out, nil
}

// resolveTemplate substitutes ${...} placeholders using variable-name
// heuristics: names suggesting a host/base become the origin, names
// suggesting an id/index become "1", anything else becomes the literal
// "test".
func resolveTemplate(tmpl, origin string) string {
	return templateVarRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		name := templateVarRe.FindStringSubmatch(match)[1]
		switch {
		case hostVarHint.MatchString(name):
			return origin
		case idVarHint.MatchString(name):
			return "1"
		default:
			return "test"
		}
	})
}

func joinOrigin(origin, resolved string) string {
	if strings.HasPrefix(resolved, "http://") || strings.HasPrefix(resolved, "https://") {
		return resolved
	}
	if strings.HasPrefix(resolved, "/") {
		return origin + resolved
	}
	return origin + "/" + resolved
}

// inferMethod scans up to 60 characters before a static-path match for a
// preceding HTTP method call, defaulting to GET when none is found.
func inferMethod(src string, matchStart int) string {
	start := matchStart - 60
	if start < 0 {
		start = 0
	}
	window := src[start:matchStart]
	if m := methodKeywordRe.FindStringSubmatch(window); m != nil {
		return strings.ToUpper(m[1])
	}
	return "GET"
}
