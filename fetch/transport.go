package fetch

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"time"
)

// newTransport builds an *http.Transport tuned for a high-concurrency crawler: generous
// connection reuse, keep-alive enabled, and an explicit TLS verification toggle.
func newTransport(proxy string, insecureSkipVerify bool) (*http.Transport, error) {
	t := &http.Transport{
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: 32,
		MaxConnsPerHost:     0, // unbounded at the transport level; per-host concurrency is gated by the engine
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableCompression:  false,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: insecureSkipVerify}, //nolint:gosec // explicit opt-in for pentest targets with self-signed certs
	}
	if proxy != "" {
		proxyURL, err := url.Parse(proxy)
		if err != nil {
			return nil, err
		}
		t.Proxy = http.ProxyURL(proxyURL)
	}
	return t, nil
}
