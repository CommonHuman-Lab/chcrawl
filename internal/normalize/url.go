// Package normalize canonicalizes URLs before dedup and scope checks.
package normalize

import (
	"net/url"
	"sort"
	"strings"

	"github.com/commonhuman-lab/chcrawl/internal/config"
)

// URL canonicalizes u according to mode. It always strips the fragment.
//
// StrictMode additionally strips default ports (":80" on http, ":443" on
// https) and lowercases percent-encoding hex digits. LegacyMode applies a
// simpler normalization: lowercase scheme+host, strip trailing slash (root
// pinned to "/"), strip fragment, query string left untouched.
func URL(raw string, mode config.CanonicalizationMode, sortQuery bool) string {
	p, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return FromParsed(p, mode, sortQuery)
}

func FromParsed(p *url.URL, mode config.CanonicalizationMode, sortQuery bool) string {
	scheme := strings.ToLower(p.Scheme)
	host := strings.ToLower(p.Host)

	if mode == config.StrictMode {
		host = stripDefaultPort(host, scheme)
	}

	path := p.Path
	if path == "" {
		path = "/"
	} else {
		path = strings.TrimRight(path, "/")
		if path == "" {
			path = "/"
		}
	}

	query := p.RawQuery
	if sortQuery && query != "" {
		query = sortQueryString(query)
	}

	out := url.URL{
		Scheme:     scheme,
		Host:       host,
		Path:       path,
		Opaque:     p.Opaque,
		User:       p.User,
		RawQuery:   query,
		ForceQuery: p.ForceQuery && query != "",
	}
	return out.String()
}

func stripDefaultPort(host, scheme string) string {
	switch {
	case scheme == "http" && strings.HasSuffix(host, ":80"):
		return strings.TrimSuffix(host, ":80")
	case scheme == "https" && strings.HasSuffix(host, ":443"):
		return strings.TrimSuffix(host, ":443")
	default:
		return host
	}
}

func sortQueryString(q string) string {
	values, err := url.ParseQuery(q)
	if err != nil {
		return q
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(values))
	for _, k := range keys {
		vs := values[k]
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	return strings.Join(parts, "&")
}
