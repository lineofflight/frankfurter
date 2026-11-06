package migrate

import (
	"context"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// screenRateSpikes is 045. It flags every stored one-day typo (see
// rates.DetectSpikes) so blends leave it out, without touching what providers
// published. Backfill keeps the flags current from here on. The blend tables
// are left as they are: refreshing all history is a full rebuild, too slow for
// a migration that runs before the app starts, and clearing them would send
// every request to live compute until the scheduler rebuilds. Run
// `frankfurter blend-rebuild` after deploy; it rebuilds in place.
func screenRateSpikes(ctx context.Context, q db.Querier) error {
	return exec(ctx, q,
		"CREATE TABLE `rate_spikes` (`provider` varchar(255) NOT NULL, `date` date NOT NULL, `base` varchar(255) "+
			"NOT NULL, `quote` varchar(255) NOT NULL, PRIMARY KEY (`provider`, `base`, `quote`, `date`))",
		"INSERT INTO `rate_spikes` (`provider`, `date`, `base`, `quote`) "+
			rates.DetectSpikes(rates.Daily.Dataset()).SQL(),
	)
}
