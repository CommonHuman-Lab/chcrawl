package retry

import (
	"errors"
	"testing"
	"time"
)

func TestDefault_RetriesRetryableStatusesAndTransportErrors(t *testing.T) {
	p := NewDefault()
	p.MaxRetries = 2

	if d := p.Next(0, 500, "", nil); !d.Retry {
		t.Errorf("expected retry on 500, got %+v", d)
	}
	if d := p.Next(0, 404, "", nil); d.Retry {
		t.Errorf("expected no retry on 404, got %+v", d)
	}
	if d := p.Next(0, 0, "", errors.New("conn refused")); !d.Retry {
		t.Errorf("expected retry on transport error, got %+v", d)
	}
	if d := p.Next(2, 500, "", nil); d.Retry {
		t.Errorf("expected no retry once MaxRetries exhausted, got %+v", d)
	}
}

func TestDefault_HonorsRetryAfterWhenLargerThanBackoff(t *testing.T) {
	p := NewDefault()
	d := p.Next(0, 429, "10", nil)
	if !d.Retry || d.Delay < 10*time.Second {
		t.Errorf("expected Retry-After=10s to be honored, got %+v", d)
	}
}

func TestLegacy_FlatFiveSecondFloorAnd429Only(t *testing.T) {
	p := NewLegacy()

	d := p.Next(0, 429, "", nil)
	if !d.Retry || d.Delay != 5*time.Second {
		t.Errorf("expected flat 5s backoff with no Retry-After, got %+v", d)
	}

	d = p.Next(0, 429, "2", nil)
	if !d.Retry || d.Delay != 5*time.Second {
		t.Errorf("Retry-After=2 should still floor at 5s, got %+v", d)
	}

	d = p.Next(0, 429, "7", nil)
	if !d.Retry || d.Delay != 7*time.Second {
		t.Errorf("Retry-After=7 should be honored (above the floor), got %+v", d)
	}

	if d := p.Next(0, 500, "", nil); d.Retry {
		t.Errorf("legacy policy must not retry non-429 statuses, got %+v", d)
	}
	if d := p.Next(2, 429, "", nil); d.Retry {
		t.Errorf("expected no retry once MaxRetries(2) exhausted, got %+v", d)
	}
}
