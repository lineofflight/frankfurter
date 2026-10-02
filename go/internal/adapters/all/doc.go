// Package all registers every provider adapter. Import it for its side effect
// where adapters are looked up by key:
//
//	import _ "github.com/lineofflight/frankfurter/go/internal/adapters/all"
//
// all.go is generated; adapter authors never edit it. Whoever integrates new
// adapter packages regenerates it.
package all

//go:generate go run ../../../cmd/genadapters
