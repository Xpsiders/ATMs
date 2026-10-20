package testutil

import (
	"testing"
	"time"
)

func TestFakeClockSetAndAdvance(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("local", 3600))
	clock := NewFakeClock(start)
	if clock.Now().Location() != time.UTC {
		t.Fatal("clock time was not normalized to UTC")
	}
	clock.Advance(time.Minute)
	want := start.UTC().Add(time.Minute)
	if !clock.Now().Equal(want) {
		t.Fatalf("Now() = %s, want %s", clock.Now(), want)
	}
}
