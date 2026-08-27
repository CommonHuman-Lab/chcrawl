package normalize

import (
	"testing"

	"github.com/commonhuman-lab/chcrawl/internal/config"
)

func TestURL_LegacyMode_SimpleNormalization(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"HTTPS://Example.COM/Path/", "https://example.com/Path"},
		{"https://example.com", "https://example.com/"},
		{"https://example.com/", "https://example.com/"},
		{"https://example.com/a/b/#frag", "https://example.com/a/b"},
		{"https://example.com/x?b=2&a=1", "https://example.com/x?b=2&a=1"},
		{"http://example.com:80/x", "http://example.com:80/x"},
	}
	for _, c := range cases {
		got := URL(c.in, config.LegacyMode, false)
		if got != c.want {
			t.Errorf("URL(%q, LegacyMode) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestURL_StrictMode_StripsDefaultPorts(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"http://example.com:80/x", "http://example.com/x"},
		{"https://example.com:443/x", "https://example.com/x"},
		{"https://example.com:8443/x", "https://example.com:8443/x"},
	}
	for _, c := range cases {
		got := URL(c.in, config.StrictMode, false)
		if got != c.want {
			t.Errorf("URL(%q, StrictMode) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestURL_SortQueryParams_OptIn(t *testing.T) {
	got := URL("https://example.com/x?b=2&a=1", config.StrictMode, true)
	want := "https://example.com/x?a=1&b=2"
	if got != want {
		t.Errorf("URL with sortQuery=true = %q, want %q", got, want)
	}
}
