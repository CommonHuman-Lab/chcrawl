package headless

import "testing"

func TestIsAsset(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://x.example/app.js", true},
		{"https://x.example/vendor.min.mjs", true},
		{"https://x.example/style.css?v=2", true},
		{"https://x.example/report.PDF", true}, // case-insensitive
		{"https://x.example/logo.svg#frag", true},
		{"https://x.example/font.woff2", true},
		{"https://x.example/api/users", false},
		{"https://x.example/", false},
		{"https://x.example/dashboard", false},
		{"https://x.example/path.with.dots/page", false},
		{"://not a url", false},
	}
	for _, c := range cases {
		if got := isAsset(c.url); got != c.want {
			t.Errorf("isAsset(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}
