package scope

import (
	"net/url"
	"regexp"
	"testing"
)

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return u
}

func TestExactOriginScope(t *testing.T) {
	seed := mustParse(t, "https://example.com/")
	cases := []struct {
		url  string
		want bool
	}{
		{"https://example.com/page", true},
		{"https://example.com:443/page", false}, // different Host string, exact match only
		{"http://example.com/page", false},      // different scheme
		{"https://sub.example.com/page", false}, // different host, no subdomain leniency
		{"https://other.com/page", false},
	}
	for _, c := range cases {
		got := ExactOriginScope{}.InScope(mustParse(t, c.url), seed)
		if got != c.want {
			t.Errorf("ExactOriginScope.InScope(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestSubdomainScope(t *testing.T) {
	s := SubdomainScope{RootDomain: "example.com"}
	cases := []struct {
		url  string
		want bool
	}{
		{"https://example.com/", true},
		{"https://app.example.com/", true},
		{"https://deep.app.example.com/", true},
		{"https://notexample.com/", false},
		{"https://example.com.evil.com/", false},
		{"https://example.com:8443/", true}, // port stripped before comparison
	}
	for _, c := range cases {
		got := s.InScope(mustParse(t, c.url), nil)
		if got != c.want {
			t.Errorf("SubdomainScope.InScope(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestAllowDenyScope(t *testing.T) {
	cases := []struct {
		name   string
		policy AllowDenyScope
		url    string
		want   bool
	}{
		{"empty allow means allow all", AllowDenyScope{}, "https://anything.com/", true},
		{"deny wins over empty allow", AllowDenyScope{Deny: []string{"bad.com"}}, "https://bad.com/", false},
		{"allow list restricts", AllowDenyScope{Allow: []string{"good.com"}}, "https://other.com/", false},
		{"allow list permits listed", AllowDenyScope{Allow: []string{"good.com"}}, "https://good.com/", true},
		{"allow subdomain matches", AllowDenyScope{Allow: []string{"good.com"}}, "https://api.good.com/", true},
		{"deny takes precedence over allow", AllowDenyScope{Allow: []string{"good.com"}, Deny: []string{"good.com"}}, "https://good.com/", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.policy.InScope(mustParse(t, c.url), nil)
			if got != c.want {
				t.Errorf("InScope(%q) = %v, want %v", c.url, got, c.want)
			}
		})
	}
}

func TestRegexScope(t *testing.T) {
	cases := []struct {
		name   string
		policy RegexScope
		url    string
		want   bool
	}{
		{"no rules allows everything", RegexScope{}, "https://example.com/anything", true},
		{"exclude blocks a match", RegexScope{Exclude: []*regexp.Regexp{regexp.MustCompile(`/admin`)}}, "https://example.com/admin/x", false},
		{"exclude doesn't block a non-match", RegexScope{Exclude: []*regexp.Regexp{regexp.MustCompile(`/admin`)}}, "https://example.com/public", true},
		{"include restricts to matches", RegexScope{Include: []*regexp.Regexp{regexp.MustCompile(`/api/`)}}, "https://example.com/other", false},
		{"include permits a match", RegexScope{Include: []*regexp.Regexp{regexp.MustCompile(`/api/`)}}, "https://example.com/api/x", true},
		{
			"exclude takes precedence over include",
			RegexScope{
				Include: []*regexp.Regexp{regexp.MustCompile(`/api/`)},
				Exclude: []*regexp.Regexp{regexp.MustCompile(`/api/private`)},
			},
			"https://example.com/api/private", false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.policy.InScope(mustParse(t, c.url), nil)
			if got != c.want {
				t.Errorf("InScope(%q) = %v, want %v", c.url, got, c.want)
			}
		})
	}
}

func TestCompositeScope_RequiresAllPoliciesToPass(t *testing.T) {
	seed := mustParse(t, "https://example.com/")
	c := CompositeScope{Policies: []Policy{
		ExactOriginScope{},
		RegexScope{Exclude: []*regexp.Regexp{regexp.MustCompile(`/admin`)}},
	}}

	if !c.InScope(mustParse(t, "https://example.com/public"), seed) {
		t.Error("expected same-origin, non-excluded URL to be in scope")
	}
	if c.InScope(mustParse(t, "https://example.com/admin"), seed) {
		t.Error("expected the excluded path to fail scope despite matching origin")
	}
	if c.InScope(mustParse(t, "https://other.com/public"), seed) {
		t.Error("expected the off-origin URL to fail scope despite not being excluded")
	}
}

func TestCompositeScope_EmptyPoliciesAllowsEverything(t *testing.T) {
	c := CompositeScope{}
	if !c.InScope(mustParse(t, "https://anything.example/"), nil) {
		t.Error("an empty CompositeScope should allow everything (vacuous AND)")
	}
}
