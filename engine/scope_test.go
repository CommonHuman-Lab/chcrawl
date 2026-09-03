package engine

import (
	"testing"

	"github.com/commonhuman-lab/chcrawl/config"
	"github.com/commonhuman-lab/chcrawl/internal/scope"
	"github.com/commonhuman-lab/chcrawl/output"
)

// buildEngineScope constructs an Engine (no network I/O happens during
// New) and returns the CompositeScope's wrapped policies, so tests can
// assert exactly which scope.Policy values a given config produces.
func buildEngineScope(t *testing.T, seedURL string, opts ...config.Option) []scope.Policy {
	t.Helper()
	cfg, err := config.New(seedURL, opts...)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	eng, err := New(cfg, output.NewWriter(&recordingWriter{}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	composite, ok := eng.scope.(scope.CompositeScope)
	if !ok {
		t.Fatalf("expected eng.scope to be a scope.CompositeScope, got %T", eng.scope)
	}
	return composite.Policies
}

func findPolicy[T scope.Policy](policies []scope.Policy) (T, bool) {
	for _, p := range policies {
		if v, ok := p.(T); ok {
			return v, true
		}
	}
	var zero T
	return zero, false
}

func TestNewEngine_ScopePolicy_DefaultIsExactOrigin(t *testing.T) {
	policies := buildEngineScope(t, "https://example.com/")
	if _, ok := findPolicy[scope.ExactOriginScope](policies); !ok {
		t.Error("expected ExactOriginScope to be present by default")
	}
	if _, ok := findPolicy[scope.SubdomainScope](policies); ok {
		t.Error("expected SubdomainScope to be absent by default")
	}
}

func TestNewEngine_ScopePolicy_IncludeSubdomainsBuildsSubdomainScope(t *testing.T) {
	policies := buildEngineScope(t, "https://example.com/", config.WithIncludeSubdomains(true))
	sub, ok := findPolicy[scope.SubdomainScope](policies)
	if !ok {
		t.Fatal("expected SubdomainScope to be present")
	}
	if sub.RootDomain != "example.com" {
		t.Errorf("expected RootDomain %q, got %q", "example.com", sub.RootDomain)
	}
	if _, ok := findPolicy[scope.ExactOriginScope](policies); ok {
		t.Error("expected ExactOriginScope to be absent when IncludeSubdomains is set")
	}
}

func TestNewEngine_ScopePolicy_IncludeSubdomainsStripsPort(t *testing.T) {
	policies := buildEngineScope(t, "https://example.com:8443/", config.WithIncludeSubdomains(true))
	sub, ok := findPolicy[scope.SubdomainScope](policies)
	if !ok {
		t.Fatal("expected SubdomainScope to be present")
	}
	if sub.RootDomain != "example.com" {
		t.Errorf("expected port-stripped RootDomain %q, got %q", "example.com", sub.RootDomain)
	}
}

func TestNewEngine_ScopePolicy_AllowDenyDomainsBuildAllowDenyScope(t *testing.T) {
	policies := buildEngineScope(t, "https://example.com/",
		config.WithAllowedDomains([]string{"a.com", "b.com"}),
		config.WithDeniedDomains([]string{"c.com"}),
	)
	ad, ok := findPolicy[scope.AllowDenyScope](policies)
	if !ok {
		t.Fatal("expected AllowDenyScope to be present")
	}
	if len(ad.Allow) != 2 || ad.Allow[0] != "a.com" || ad.Allow[1] != "b.com" {
		t.Errorf("expected Allow [a.com b.com], got %v", ad.Allow)
	}
	if len(ad.Deny) != 1 || ad.Deny[0] != "c.com" {
		t.Errorf("expected Deny [c.com], got %v", ad.Deny)
	}
}

func TestNewEngine_ScopePolicy_IncludeAndExcludePatternsShareOneRegexScope(t *testing.T) {
	policies := buildEngineScope(t, "https://example.com/",
		config.WithIncludePatterns([]string{"include-me"}),
		config.WithExcludePatterns([]string{"exclude-me"}),
	)
	var regexScopes []scope.RegexScope
	for _, p := range policies {
		if rs, ok := p.(scope.RegexScope); ok {
			regexScopes = append(regexScopes, rs)
		}
	}
	if len(regexScopes) != 1 {
		t.Fatalf("expected exactly one RegexScope, got %d", len(regexScopes))
	}
	if len(regexScopes[0].Include) != 1 || len(regexScopes[0].Exclude) != 1 {
		t.Errorf("expected one Include and one Exclude pattern on the shared RegexScope, got Include=%v Exclude=%v",
			regexScopes[0].Include, regexScopes[0].Exclude)
	}
}

func TestNewEngine_ScopePolicy_OmitsUnsetOptionalPolicies(t *testing.T) {
	policies := buildEngineScope(t, "https://example.com/", config.WithSameOrigin(false))
	if len(policies) != 0 {
		t.Errorf("expected no policies when SameOrigin is false and nothing else is set, got %v", policies)
	}
}
