package vcrtest_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func get(t *testing.T, c *http.Client, u string) (string, error) {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}

func TestMatchOnMethodAndHost(t *testing.T) {
	c := vcrtest.Client(t, "banrep", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host), vcrtest.AllowPlaybackRepeats)
	for range 2 {
		body, err := get(t, c, "https://www.datos.gov.co/anything?at=all")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body, "vigenciadesde") {
			t.Fatalf("unexpected body %.80q", body)
		}
	}
}

func TestInteractionsPlayOnceWithoutRepeats(t *testing.T) {
	c := vcrtest.Client(t, "banrep", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host))
	if _, err := get(t, c, "https://www.datos.gov.co/"); err != nil {
		t.Fatal(err)
	}
	if _, err := get(t, c, "https://www.datos.gov.co/"); err == nil {
		t.Fatal("second request replayed a used interaction")
	}
}

func TestURIMatchIgnoresQueryOrderAndEscaping(t *testing.T) {
	c := vcrtest.Client(t, "banrep")
	q := url.Values{
		"$where": {"vigenciadesde>='2026-03-16T00:00:00.000' AND vigenciadesde<='2026-03-24T00:00:00.000'"},
		"$limit": {"50000"},
		"$order": {"vigenciadesde ASC"},
	}
	if _, err := get(t, c, "https://www.datos.gov.co/resource/32sa-8pi3.json?"+q.Encode()); err != nil {
		t.Fatal(err)
	}
}

func TestURIMatchRejectsOtherQueries(t *testing.T) {
	c := vcrtest.Client(t, "banrep")
	if _, err := get(t, c, "https://www.datos.gov.co/resource/32sa-8pi3.json?$limit=1"); err == nil {
		t.Fatal("matched a request with a different query")
	}
}

func TestBodyMatchComparesFormFields(t *testing.T) {
	c := vcrtest.Client(t, "bfm", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI, vcrtest.Body))
	form := url.Values{"filterData": {"EUR"}, "dateFilterDebut": {"2026/09/21"}, "dateFilterFin": {"2026/09/23"}}
	resp, err := c.PostForm("https://www.banky-foibe.mg/admin/wp-json/bfm/cours_mid_en_ar_filter", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	form.Set("filterData", "XXX")
	if _, err := c.PostForm("https://www.banky-foibe.mg/admin/wp-json/bfm/cours_mid_en_ar_filter", form); err == nil {
		t.Fatal("matched a different form body")
	}
}

func TestSecretPlaceholders(t *testing.T) {
	vcrtest.SetSecrets(t)
	c := vcrtest.Client(t, "fred")
	q := url.Values{
		"api_key":           {"<FRED_API_KEY>"},
		"file_type":         {"json"},
		"observation_start": {"2026-03-01"},
		"series_id":         {"DEXBZUS"},
	}
	if _, err := get(t, c, "https://api.stlouisfed.org/fred/series/observations?"+q.Encode()); err != nil {
		t.Fatal(err)
	}
}

func TestRealKeysMatchPlaceholders(t *testing.T) {
	t.Setenv("FRED_API_KEY", "s3cret")
	c := vcrtest.Client(t, "fred")
	u := "https://api.stlouisfed.org/fred/series/observations?api_key=s3cret&file_type=json&observation_start=2026-03-01&series_id=DEXBZUS"
	if _, err := get(t, c, u); err != nil {
		t.Fatal(err)
	}
}
