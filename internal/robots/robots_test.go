package robots

import (
	"context"
	"net/url"
	"testing"

	"github.com/commonhuman-lab/chcrawl/internal/fetch"
)

type fakeFetcher struct {
	body       string
	statusCode int
	err        error
}

func (f *fakeFetcher) Fetch(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	status := f.statusCode
	if status == 0 {
		status = 200
	}
	return &fetch.Response{StatusCode: status, Body: []byte(f.body)}, nil
}

func TestChecker_LongestPrefixMatchWithAllowTieBreak(t *testing.T) {
	body := `
User-agent: *
Disallow: /admin
Allow: /admin/public
Disallow: /private/
`
	c := New(&fakeFetcher{body: body}, "*")
	cases := []struct {
		path string
		want bool
	}{
		{"/", true},
		{"/admin", false},
		{"/admin/secret", false},
		{"/admin/public", true},
		{"/admin/public/x", true},
		{"/private/data", false},
	}
	for _, tc := range cases {
		u, _ := url.Parse("https://example.com" + tc.path)
		if got := c.Allowed(context.Background(), u); got != tc.want {
			t.Errorf("Allowed(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestChecker_EmptyDisallowAllowsEverything(t *testing.T) {
	body := "User-agent: *\nDisallow:\n"
	c := New(&fakeFetcher{body: body}, "*")
	u, _ := url.Parse("https://example.com/anything")
	if !c.Allowed(context.Background(), u) {
		t.Errorf("expected empty Disallow to allow everything")
	}
}

func TestChecker_MissingRobotsTxtAllowsEverything(t *testing.T) {
	c := New(&fakeFetcher{statusCode: 404}, "*")
	u, _ := url.Parse("https://example.com/anything")
	if !c.Allowed(context.Background(), u) {
		t.Errorf("expected missing robots.txt (404) to allow everything")
	}
}

func TestChecker_SpecificUserAgentGroupOverridesWildcard(t *testing.T) {
	body := `
User-agent: *
Disallow: /

User-agent: chcrawl
Disallow: /admin
`
	c := New(&fakeFetcher{body: body}, "chcrawl")
	u, _ := url.Parse("https://example.com/public")
	if !c.Allowed(context.Background(), u) {
		t.Errorf("expected the chcrawl-specific group (which doesn't disallow /public) to apply, not the wildcard group")
	}
	admin, _ := url.Parse("https://example.com/admin")
	if c.Allowed(context.Background(), admin) {
		t.Errorf("expected /admin to be disallowed under the chcrawl-specific group")
	}
}
