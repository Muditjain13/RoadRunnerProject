package service

import (
	"testing"
	"time"
)

func TestDedupCache_FirstSeenIsNotDuplicate(t *testing.T) {
	c := newDedupCache(time.Minute)
	if c.checkAndMark("k1") {
		t.Error("checkAndMark() = true on first sight, want false")
	}
}

func TestDedupCache_RepeatWithinTTLIsDuplicate(t *testing.T) {
	c := newDedupCache(time.Minute)
	c.checkAndMark("k1")
	if !c.checkAndMark("k1") {
		t.Error("checkAndMark() = false on repeat within TTL, want true")
	}
}

func TestDedupCache_RepeatAfterTTLIsNotDuplicate(t *testing.T) {
	now := time.Now()
	c := newDedupCache(time.Minute)
	c.now = func() time.Time { return now }

	c.checkAndMark("k1")
	now = now.Add(2 * time.Minute)
	if c.checkAndMark("k1") {
		t.Error("checkAndMark() = true after TTL expired, want false")
	}
}

func TestDedupCache_DifferentKeysIndependent(t *testing.T) {
	c := newDedupCache(time.Minute)
	c.checkAndMark("k1")
	if c.checkAndMark("k2") {
		t.Error("checkAndMark() for a different key = true, want false")
	}
}
