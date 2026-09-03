// Package retry provides pluggable retry/backoff policies for the fetcher.
package retry

import (
	"math"
	"math/rand"
	"strconv"
	"time"
)

// Decision is what a Policy wants the fetcher to do after one attempt.
type Decision struct {
	Retry bool
	Delay time.Duration
}

// Policy decides whether to retry a failed or rate-limited fetch attempt. attempt is 0-indexed;
// statusCode is 0 when err is non-nil (a transport-level failure rather than an HTTP response).
type Policy interface {
	Next(attempt int, statusCode int, retryAfter string, err error) Decision
}

// Default is an exponential-backoff-with-jitter policy that retries transport errors and a
// configurable set of HTTP statuses (429 and 5xx by default, since a 5xx is often transient).
type Default struct {
	MaxRetries      int
	BaseDelay       time.Duration
	MaxDelay        time.Duration
	RetryableStatus map[int]bool
}

// NewDefault returns the Default policy with sane defaults: 3 retries, exponential backoff from
// 500ms up to 30s with full jitter, retrying 429 and all 5xx statuses.
func NewDefault() *Default {
	return &Default{
		MaxRetries: 3,
		BaseDelay:  500 * time.Millisecond,
		MaxDelay:   30 * time.Second,
		RetryableStatus: map[int]bool{
			429: true, 500: true, 502: true, 503: true, 504: true,
		},
	}
}

func (d *Default) Next(attempt int, statusCode int, retryAfter string, err error) Decision {
	if attempt >= d.MaxRetries {
		return Decision{Retry: false}
	}
	if err == nil && !d.RetryableStatus[statusCode] {
		return Decision{Retry: false}
	}

	delay := time.Duration(float64(d.BaseDelay) * math.Pow(2, float64(attempt)))
	if delay > d.MaxDelay {
		delay = d.MaxDelay
	}
	delay = time.Duration(rand.Int63n(int64(delay) + 1)) // full jitter, [0, delay]

	if ra, ok := parseRetryAfter(retryAfter); ok && ra > delay {
		delay = ra
	}
	return Decision{Retry: true, Delay: delay}
}

// Legacy is a simpler, flat-backoff retry policy: 429-only, with a Retry-After floor. Not used by default.
type Legacy struct {
	MaxRetries int
	FlatDelay  time.Duration
}

// NewLegacy returns the Legacy policy with its default constants: 2 retries, 5.0s flat backoff floor.
func NewLegacy() *Legacy {
	return &Legacy{MaxRetries: 2, FlatDelay: 5 * time.Second}
}

func (l *Legacy) Next(attempt int, statusCode int, retryAfter string, err error) Decision {
	if attempt >= l.MaxRetries || err != nil || statusCode != 429 {
		return Decision{Retry: false}
	}
	delay := l.FlatDelay
	if ra, ok := parseRetryAfter(retryAfter); ok && ra > delay {
		delay = ra
	}
	return Decision{Retry: true, Delay: delay}
}

func parseRetryAfter(v string) (time.Duration, bool) {
	if v == "" {
		return 0, false
	}
	secs, err := strconv.ParseFloat(v, 64)
	if err != nil || secs < 0 {
		return 0, false
	}
	return time.Duration(secs * float64(time.Second)), true
}
