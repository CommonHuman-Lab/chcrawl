// Package scope decides whether a discovered URL should be followed.
package scope

import (
	"net/url"
	"regexp"
	"strings"
)

// Policy decides whether u is in-scope relative to the crawl's seed URL.
type Policy interface {
	InScope(u, seed *url.URL) bool
}

// ExactOriginScope requires an exact scheme+host(+port) match. This is the
// default scope policy.
type ExactOriginScope struct{}

func (ExactOriginScope) InScope(u, seed *url.URL) bool {
	return strings.EqualFold(u.Scheme, seed.Scheme) && strings.EqualFold(u.Host, seed.Host)
}

// SubdomainScope allows any host that is the root domain or a subdomain of
// it (e.g. "app.example.com" and "example.com" both match root "example.com").
type SubdomainScope struct {
	RootDomain string
}

func (s SubdomainScope) InScope(u, _ *url.URL) bool {
	host := strings.ToLower(stripPort(u.Host))
	root := strings.ToLower(s.RootDomain)
	return host == root || strings.HasSuffix(host, "."+root)
}

// AllowDenyScope matches hosts against explicit allow/deny domain lists.
// Deny takes precedence over allow. An empty Allow list means "allow
// everything not denied".
type AllowDenyScope struct {
	Allow []string
	Deny  []string
}

func (s AllowDenyScope) InScope(u, _ *url.URL) bool {
	host := strings.ToLower(stripPort(u.Host))
	for _, d := range s.Deny {
		if matchesDomain(host, d) {
			return false
		}
	}
	if len(s.Allow) == 0 {
		return true
	}
	for _, a := range s.Allow {
		if matchesDomain(host, a) {
			return true
		}
	}
	return false
}

func matchesDomain(host, domain string) bool {
	domain = strings.ToLower(domain)
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// RegexScope includes/excludes URLs by regex against the full URL string.
// Exclude takes precedence over include. A nil Include list means "include
// everything not excluded".
type RegexScope struct {
	Include []*regexp.Regexp
	Exclude []*regexp.Regexp
}

func (s RegexScope) InScope(u, _ *url.URL) bool {
	full := u.String()
	for _, re := range s.Exclude {
		if re.MatchString(full) {
			return false
		}
	}
	if len(s.Include) == 0 {
		return true
	}
	for _, re := range s.Include {
		if re.MatchString(full) {
			return true
		}
	}
	return false
}

// CompositeScope requires all wrapped policies to allow the URL (AND
// semantics).
type CompositeScope struct {
	Policies []Policy
}

func (c CompositeScope) InScope(u, seed *url.URL) bool {
	for _, p := range c.Policies {
		if !p.InScope(u, seed) {
			return false
		}
	}
	return true
}

func stripPort(host string) string {
	if i := strings.LastIndex(host, ":"); i != -1 {
		return host[:i]
	}
	return host
}
