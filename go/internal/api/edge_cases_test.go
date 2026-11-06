package api

import (
	"context"
	"net/http"
	"testing"
)

// spec/edge_cases_spec.rb

func TestHandlesUnfoundPages(t *testing.T) {
	a := newTestApp(t)
	a.get("/foo")
	a.status(http.StatusNotFound)
}

func TestWillNotProcessInvalidDate(t *testing.T) {
	a := newTestApp(t)
	a.get("/v1/2010-31-01")
	a.status(http.StatusUnprocessableEntity)
}

func TestWillNotProcessInvalidAmount(t *testing.T) {
	a := newTestApp(t)
	a.get("/v1/latest?amount=0&from=USD&to=EUR")
	a.status(http.StatusUnprocessableEntity)
}

func TestWillNotProcessDateBefore2000(t *testing.T) {
	a := newTestApp(t)
	a.get("/v1/1999-01-01")
	a.status(http.StatusNotFound)
}

func TestWillNotProcessUnavailableBase(t *testing.T) {
	a := newTestApp(t)
	a.get("/v1/latest?base=UAH")
	a.status(http.StatusNotFound)
}

func TestHandlesMalformedQueries(t *testing.T) {
	a := newTestApp(t)
	a.get("/v1/latest?base=USD?callback=?")
	a.status(http.StatusNotFound)
}

func TestDoesNotReturnStaleDates(t *testing.T) {
	a := newTestApp(t)
	a.get("/v1/latest")
	date := a.json()["date"]
	_, err := a.db.ExecContext(context.Background(), `DELETE FROM rates WHERE provider = 'ECB' AND date = (
		SELECT date FROM rates WHERE provider = 'ECB' AND date <= ? ORDER BY date DESC LIMIT 1)`,
		today(t))
	if err != nil {
		t.Fatal(err)
	}
	a.get("/v1/latest")
	if got := a.json()["date"]; got == date {
		t.Fatalf("date still %v", got)
	}
}

func TestWillNotProcessCircularConversions(t *testing.T) {
	a := newTestApp(t)
	a.get("/v1/latest?from=EUR&to=EUR")
	a.status(http.StatusUnprocessableEntity)
	a.get("/v1/latest?from=USD&to=USD")
	a.status(http.StatusUnprocessableEntity)
}
