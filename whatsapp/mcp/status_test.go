package main

import (
	"testing"
	"time"
)

func TestEventFreshnessBeforeFirstEventIsZero(t *testing.T) {
	at, ago := eventFreshness(time.Time{})
	if at != 0 || ago != 0 {
		t.Fatalf("eventFreshness(zero) = (%d, %d), want (0, 0)", at, ago)
	}
}

func TestEventFreshnessNeverReportsNegativeAge(t *testing.T) {
	at, ago := eventFreshness(time.Now().Add(time.Minute))
	if at == 0 {
		t.Fatal("eventFreshness(future) returned zero timestamp")
	}
	if ago < 0 {
		t.Fatalf("eventFreshness(future) age = %d, want non-negative", ago)
	}
}
