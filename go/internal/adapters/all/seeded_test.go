package all_test

import (
	"context"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/dbtest"
	"github.com/lineofflight/frankfurter/go/internal/provider"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// spec/provider_spec.rb "resolves all seeded providers".
func TestResolvesAllSeededProviders(t *testing.T) {
	ctx := context.Background()
	conn := dbtest.New(t)
	if err := rates.SeedProviders(ctx, conn); err != nil {
		t.Fatal(err)
	}
	providers, err := provider.All(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) == 0 {
		t.Fatal("no providers seeded")
	}
	for _, p := range providers {
		if _, ok := adapter.Lookup(p.Key); !ok {
			t.Errorf("%s: no adapter registered", p.Key)
		}
	}
}
