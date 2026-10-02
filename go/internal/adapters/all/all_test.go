package all_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	_ "github.com/lineofflight/frankfurter/go/internal/adapters/all"
)

// Every adapter registers under a provider key seeded in db/seeds/providers,
// from a package named after the key.
func TestRegisteredKeysAreSeededProviders(t *testing.T) {
	keys := adapter.All()
	if len(keys) == 0 {
		t.Fatal("no adapters registered")
	}
	for _, key := range keys {
		if _, err := os.Stat(filepath.Join("..", "..", "..", "..", "db", "seeds", "providers", strings.ToLower(key)+".json")); err != nil {
			t.Errorf("%s: no seed file: %v", key, err)
		}
		if _, err := os.Stat(filepath.Join("..", strings.ToLower(key))); err != nil {
			t.Errorf("%s: no package internal/adapters/%s", key, strings.ToLower(key))
		}
	}
}
