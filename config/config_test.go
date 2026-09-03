package config

import "testing"

func TestNew_ScopeOptionsPopulateFields(t *testing.T) {
	o, err := New("https://example.com/",
		WithIncludeSubdomains(true),
		WithAllowedDomains([]string{"a.com", "b.com"}),
		WithDeniedDomains([]string{"c.com"}),
		WithIncludePatterns([]string{`^https://example\.com/api/`}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !o.IncludeSubdomains {
		t.Error("expected IncludeSubdomains to be true")
	}
	if len(o.AllowedDomains) != 2 || o.AllowedDomains[0] != "a.com" || o.AllowedDomains[1] != "b.com" {
		t.Errorf("expected AllowedDomains [a.com b.com], got %v", o.AllowedDomains)
	}
	if len(o.DeniedDomains) != 1 || o.DeniedDomains[0] != "c.com" {
		t.Errorf("expected DeniedDomains [c.com], got %v", o.DeniedDomains)
	}
	if len(o.IncludePatterns) != 1 || !o.IncludePatterns[0].MatchString("https://example.com/api/x") {
		t.Errorf("expected one compiled IncludePatterns entry matching the API path, got %v", o.IncludePatterns)
	}
}

func TestNew_IncludeSubdomainsRequiresSameOrigin(t *testing.T) {
	_, err := New("https://example.com/", WithSameOrigin(false), WithIncludeSubdomains(true))
	if err == nil {
		t.Fatal("expected an error when IncludeSubdomains is set without SameOrigin")
	}
}

func TestWithIncludePatterns_SkipsInvalidRegex(t *testing.T) {
	o, err := New("https://example.com/", WithIncludePatterns([]string{`[`, `valid.*`}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(o.IncludePatterns) != 1 || o.IncludePatterns[0].String() != "valid.*" {
		t.Errorf("expected the invalid pattern to be silently skipped, got %v", o.IncludePatterns)
	}
}
