package cache

import (
	"github.com/lineofflight/frankfurter/go/internal/provider"
	"github.com/lineofflight/frankfurter/go/internal/schedule"
)

// Cache is what the backfill and the scheduler purge through.
var (
	_ provider.Cache = (*Cache)(nil)
	_ schedule.Cache = (*Cache)(nil)
)
