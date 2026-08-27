package extract

import (
	"context"
	"net/url"
	"regexp"
	"strings"

	"github.com/commonhuman-lab/chcrawl/internal/fetch"
	"golang.org/x/net/html"
)

// codePathRe mines API-doc-style paths (e.g. "/rest/basket/:bid",
// "/api/Products/{id}") out of <code> block text — a deliberate, tested
// discovery channel, not a throwaway heuristic.
var codePathRe = regexp.MustCompile(`^(/(?:[A-Za-z0-9_\-]+/){2,}(?:[A-Za-z0-9_\-]+|\{[^}]+\}|:[A-Za-z][A-Za-z0-9_]*)(?:\?[^\s"'<>]*)?)`)

var skipHrefPrefixes = []string{"javascript:", "mailto:", "#"}

func skippableHref(v string) bool {
	for _, p := range skipHrefPrefixes {
		if strings.HasPrefix(v, p) {
			return true
		}
	}
	return false
}

// LinkExtractor finds crawlable links from HTML markup: anchors, form
// buttons, common asset tags, SPA data-* attributes, meta-refresh, and the
// <code>-block API-path heuristic.
type LinkExtractor struct{}

func (LinkExtractor) Name() string { return "html_links" }

func (LinkExtractor) Applies(resp *fetch.Response) bool {
	return strings.Contains(strings.ToLower(resp.ContentType), "html")
}

func (LinkExtractor) Extract(ctx context.Context, in Input) ([]Discovery, error) {
	if in.Doc == nil {
		return nil, nil
	}
	var out []Discovery
	add := func(kind, raw string, meta map[string]string) {
		if raw == "" {
			return
		}
		abs, err := resolve(in.BaseURL, raw)
		if err != nil {
			return
		}
		out = append(out, Discovery{Kind: kind, URL: abs, Meta: meta})
	}

	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			attrs := attrMap(n.Attr)
			switch n.Data {
			case "a":
				if href := strings.TrimSpace(attrs["href"]); href != "" && !skippableHref(href) {
					add("link", href, map[string]string{"source": "a_href"})
				}
			case "link":
				if href := strings.TrimSpace(attrs["href"]); href != "" && !skippableHref(href) {
					add("link", href, map[string]string{"source": "link_href"})
				}
			case "script":
				if src := strings.TrimSpace(attrs["src"]); src != "" {
					add("link", src, map[string]string{"source": "script_src", "asset": "js"})
				}
			case "img":
				if src := strings.TrimSpace(attrs["src"]); src != "" {
					add("link", src, map[string]string{"source": "img_src"})
				}
			case "iframe":
				if src := strings.TrimSpace(attrs["src"]); src != "" && !skippableHref(src) {
					add("link", src, map[string]string{"source": "iframe_src"})
				}
			case "button":
				if fa := strings.TrimSpace(attrs["formaction"]); fa != "" && !skippableHref(fa) {
					add("link", fa, map[string]string{"source": "button_formaction"})
				}
			case "meta":
				if strings.EqualFold(attrs["http-equiv"], "refresh") {
					if u, ok := parseMetaRefresh(attrs["content"]); ok {
						add("link", u, map[string]string{"source": "meta_refresh"})
					}
				}
			case "code":
				for _, d := range extractCodePaths(n, in.BaseURL) {
					out = append(out, d)
				}
			}
			for _, dataAttr := range []string{"data-href", "data-url", "data-link", "data-action"} {
				if v := strings.TrimSpace(attrs[dataAttr]); v != "" && !skippableHref(v) && !strings.HasPrefix(v, "{") {
					add("link", v, map[string]string{"source": dataAttr})
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(in.Doc)
	return out, nil
}

// extractCodePaths builds absolute URLs by direct string concatenation of
// scheme+host with the matched path, rather than round-tripping through
// net/url's parser/serializer: that would percent-encode literal "{"/"}"
// characters in REST placeholder paths like "/api/x/{id}". Preserving the
// exact matched text is what makes this heuristic useful as a discovery
// hint in the first place.
func extractCodePaths(codeNode *html.Node, base *url.URL) []Discovery {
	var out []Discovery
	var collect func(n *html.Node)
	collect = func(n *html.Node) {
		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			pathOnly := text
			if i := strings.IndexAny(pathOnly, "? "); i != -1 {
				pathOnly = pathOnly[:i]
			}
			if codePathRe.MatchString(pathOnly) {
				out = append(out, Discovery{
					Kind: "code_path",
					URL:  base.Scheme + "://" + base.Host + text,
					Meta: map[string]string{"source": "code_block"},
				})
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(codeNode)
	return out
}

func parseMetaRefresh(content string) (string, bool) {
	parts := strings.SplitN(content, ";", 2)
	if len(parts) != 2 {
		return "", false
	}
	rest := strings.TrimSpace(parts[1])
	lower := strings.ToLower(rest)
	idx := strings.Index(lower, "url=")
	if idx == -1 {
		return "", false
	}
	u := strings.TrimSpace(rest[idx+4:])
	u = strings.Trim(u, `"'`)
	if u == "" {
		return "", false
	}
	return u, true
}

func attrMap(attrs []html.Attribute) map[string]string {
	m := make(map[string]string, len(attrs))
	for _, a := range attrs {
		m[strings.ToLower(a.Key)] = a.Val
	}
	return m
}

func resolve(base *url.URL, ref string) (string, error) {
	u, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	abs := base.ResolveReference(u)
	abs.Fragment = ""
	return abs.String(), nil
}
