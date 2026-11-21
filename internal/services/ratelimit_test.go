package services

import (
	"testing"
	"time"
)

func TestLimiterBurstAndRefill(t *testing.T) {
	limiter := NewLimiter(2, 5)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		if !limiter.Allow("device", start) {
			t.Fatalf("request %d should be allowed within burst", i+1)
		}
	}
	if limiter.Allow("device", start) {
		t.Fatal("request beyond burst should be denied")
	}
	if !limiter.Allow("device", start.Add(500*time.Millisecond)) {
		t.Fatal("one token should have refilled after 500ms")
	}
	if limiter.Allow("device", start.Add(500*time.Millisecond)) {
		t.Fatal("only one token should have refilled")
	}
	if !limiter.Allow("device", start.Add(2*time.Second)) {
		t.Fatal("tokens should have refilled to the burst cap")
	}
}

func TestLimiterKeysAreIndependent(t *testing.T) {
	limiter := NewLimiter(0.1, 1)
	now := time.Now()
	if !limiter.Allow("a", now) {
		t.Fatal("first key should be allowed")
	}
	if limiter.Allow("a", now) {
		t.Fatal("first key should be limited")
	}
	if !limiter.Allow("b", now) {
		t.Fatal("second key should have its own bucket")
	}
}
