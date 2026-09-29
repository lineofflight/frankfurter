package heavyslots

import (
	"os"
	"strconv"
	"testing"
)

func TestHandsOutSlotsUpToTheCapAndRefusesPastIt(t *testing.T) {
	s := New(2)
	if !s.TryAcquire() || !s.TryAcquire() {
		t.Fatal("refused below the cap")
	}
	if s.TryAcquire() {
		t.Fatal("granted past the cap")
	}
	if s.Held() != 2 {
		t.Fatalf("held %d", s.Held())
	}
}

func TestFreesASlotOnRelease(t *testing.T) {
	s := New(1)
	s.TryAcquire()
	s.Release()
	if s.Held() != 0 {
		t.Fatalf("held %d", s.Held())
	}
	if !s.TryAcquire() {
		t.Fatal("refused after release")
	}
}

func TestNeverCountsBelowZero(t *testing.T) {
	s := New(1)
	s.Release()
	if s.Held() != 0 {
		t.Fatalf("held %d", s.Held())
	}
}

func TestReadsTheCapFromTheEnvironment(t *testing.T) {
	want := 2
	if v, ok := os.LookupEnv("MAX_HEAVY_COMPUTES"); ok {
		want, _ = strconv.Atoi(v)
	}
	if DefaultMax != want {
		t.Fatalf("DefaultMax %d, want %d", DefaultMax, want)
	}
	t.Setenv("MAX_HEAVY_COMPUTES", "5")
	if got := defaultMax(); got != 5 {
		t.Fatalf("defaultMax() = %d with MAX_HEAVY_COMPUTES=5", got)
	}
	if New(DefaultMax).Max() != DefaultMax {
		t.Fatal("Max does not report the cap")
	}
}
