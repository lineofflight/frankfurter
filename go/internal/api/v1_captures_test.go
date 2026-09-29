package api

import (
	"context"
	"net/http"
	"testing"
)

// Behaviour the specs do not cover. Requests the golden corpus can express live there instead.

// A stored row with no resolvable rate (one side of a quote) fails the request, as Ruby's amount * nil raises; a
// query that leaves the row out is unaffected.
func TestV1RowWithoutRate(t *testing.T) {
	a := newTestApp(t)
	_, err := a.db.ExecContext(context.Background(),
		`INSERT INTO rates (provider, date, base, quote, bid) VALUES ('ECB', ?, 'EUR', 'ISK', 150)`, latest())
	if err != nil {
		t.Fatal(err)
	}
	a.get("/v1/latest")
	a.status(http.StatusUnprocessableEntity)
	a.get("/v1/latest?to=USD")
	a.status(http.StatusOK)
}
