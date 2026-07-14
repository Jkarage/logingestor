package ingest

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/logbus"
)

func Test_Limiter_BurstThenThrottleThenRecover(t *testing.T) {
	l := NewLimiter()
	id := uuid.New()
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

	// burst=10, rate=10/s. First 10 events allowed from the full bucket.
	if ok, _ := l.AllowN(id, 10, 10, 10, now); !ok {
		t.Fatal("first burst of 10 should be allowed")
	}

	// 11th event (same instant) should be throttled with a Retry-After.
	ok, retry := l.AllowN(id, 10, 10, 1, now)
	if ok {
		t.Fatal("event past burst should be throttled")
	}
	if retry <= 0 {
		t.Errorf("expected positive Retry-After, got %v", retry)
	}

	// After 1 second, ~10 tokens refilled -> allowed again.
	if ok, _ := l.AllowN(id, 10, 10, 1, now.Add(time.Second)); !ok {
		t.Error("after refill the source should be allowed again")
	}
}

func Test_Limiter_ZeroRateUnlimited(t *testing.T) {
	l := NewLimiter()
	if ok, _ := l.AllowN(uuid.New(), 0, 0, 1000, time.Now()); !ok {
		t.Error("zero rate/burst should be treated as unlimited")
	}
}

func Test_KeepRecord(t *testing.T) {
	// WARN/ERROR always kept regardless of rate.
	if !KeepRecord(logbus.LevelError, 0, 0.99) {
		t.Error("ERROR must always be kept")
	}
	if !KeepRecord(logbus.LevelWarn, 0, 0.99) {
		t.Error("WARN must always be kept")
	}
	// DEBUG/INFO sampled by keepRate vs r.
	if KeepRecord(logbus.LevelInfo, 0.5, 0.6) {
		t.Error("INFO with r>keepRate should be dropped")
	}
	if !KeepRecord(logbus.LevelInfo, 0.5, 0.4) {
		t.Error("INFO with r<keepRate should be kept")
	}
	if !KeepRecord(logbus.LevelDebug, 1.0, 0.99) {
		t.Error("keepRate 1.0 keeps everything")
	}
}
