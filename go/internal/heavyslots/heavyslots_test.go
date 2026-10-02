package heavyslots

import (
	"os"
	"runtime"
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
	want := max(2, runtime.GOMAXPROCS(0))
	if _, ok := os.LookupEnv("MAX_HEAVY_COMPUTES"); ok {
		want = defaultMax()
	}
	if DefaultMax != want {
		t.Fatalf("DefaultMax %d, want %d", DefaultMax, want)
	}
	t.Setenv("MAX_HEAVY_COMPUTES", "5")
	if got := defaultMax(); got != 5 {
		t.Fatalf("defaultMax() = %d with MAX_HEAVY_COMPUTES=5", got)
	}
	// Ruby's Integer() accepts these too.
	for s, want := range map[string]int{" 3 ": 3, "0x10": 16, "010": 8, "1_0": 10} {
		t.Setenv("MAX_HEAVY_COMPUTES", s)
		if got := defaultMax(); got != want {
			t.Errorf("defaultMax() = %d with MAX_HEAVY_COMPUTES=%q, want %d", got, s, want)
		}
	}
	if New(DefaultMax).Max() != DefaultMax {
		t.Fatal("Max does not report the cap")
	}
}
