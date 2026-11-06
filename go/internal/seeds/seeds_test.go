package seeds

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestDataMatchesRepository(t *testing.T) {
	repo := os.DirFS(filepath.Join("..", "..", "..", "db", "seeds"))
	seen := map[string]bool{}
	err := fs.WalkDir(repo, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		seen[name] = true
		want, err := fs.ReadFile(repo, name)
		if err != nil {
			return err
		}
		got, err := fs.ReadFile(FS, name)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s differs from db/seeds; run go generate ./internal/seeds", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fs.WalkDir(FS, ".", func(name string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !seen[name] {
			t.Errorf("%s is gone from db/seeds; run go generate ./internal/seeds", name)
		}
		return err
	})
}

func TestProviders(t *testing.T) {
	providers, err := Providers()
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) < 100 {
		t.Fatalf("got %d providers", len(providers))
	}
	byKey := map[string]Provider{}
	for _, p := range providers {
		byKey[p.Key] = p
	}
	if ecb := byKey["ECB"]; ecb.PivotCurrency != "EUR" || ecb.Frequency != "daily" || ecb.CoverageStart != "1999-01-04" {
		t.Errorf("ECB = %+v", ecb)
	}
	if byKey["HMRC"].Frequency != "monthly" {
		t.Errorf("HMRC frequency = %q", byKey["HMRC"].Frequency)
	}
}

func TestParsersLoad(t *testing.T) {
	if pegs, err := Pegs(); err != nil || len(pegs) == 0 {
		t.Fatal(len(pegs), err)
	}
	if d, err := DefunctCurrencies(); err != nil || len(d) == 0 {
		t.Fatal(len(d), err)
	}
	if n, err := NascentCurrencies(); err != nil || len(n) == 0 {
		t.Fatal(len(n), err)
	}
	if p, err := CurrencyPatches(); err != nil || len(p) == 0 {
		t.Fatal(len(p), err)
	}
}
