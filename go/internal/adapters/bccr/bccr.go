// Package bccr fetches rates from Banco Central de Costa Rica (BCCR), which
// publishes the daily reference exchange rate for the US dollar against the
// Costa Rican colón through the SDDE API.
//
// The API returns a pivoted table (dates as columns) with at most 100 date
// columns per request, so backfill is chunked in 90-day periods. Like the Ruby
// adapter, Fetch returns whatever the table holds without clipping it to the
// window.
package bccr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const (
	baseURL  = "https://apim.bccr.fi.cr/SDDE/api"
	tokenURL = baseURL + "/Bccr.GE.SDDE.IndicadoresSitioExterno.ServiciosUsuario.API/Token/GenereCSRF"
	dataURL  = baseURL + "/Bccr.GE.SDDE.IndicadoresSitioExterno.GrupoVariables.API/CuadroGrupoVariables/ObtenerDatosCuadro"

	// groupID 1 is the USD buy/sell reference rate group.
	groupID = 1
	// sellIndicator 318 is the sell (venta) rate, CRC per 1 USD.
	sellIndicator = 318
)

func init() {
	adapter.Register("BCCR", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BCCR rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 90 }

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	token, err := a.Get(ctx, tokenURL, nil)
	if err != nil {
		return nil, err
	}

	if upto.IsZero() {
		upto = a.Today()
	}
	start := ""
	if !after.IsZero() {
		start = after.Format(time.DateOnly)
	}
	query := url.Values{
		"IdGrupoVariable":        {strconv.Itoa(groupID)},
		"FechaInicio":            {start + "T00:00:00"},
		"FechaFin":               {upto.Format(time.DateOnly)},
		"CantidadSeriesAMostrar": {"100"},
	}
	req, err := a.NewRequest(ctx, http.MethodGet, dataURL+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	// http.rb sends token_csrf as Token-Csrf, which is what the API was
	// recorded accepting.
	req.Header.Set("Token-Csrf", string(token))
	req.Header.Set("Origin", "https://sdd.bccr.fi.cr")
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	return parse(resp.Body)
}

type table struct {
	Columns []struct {
		Field string `json:"field"`
		Title string `json:"tituloIngles"`
	} `json:"columnas"`
	Indicators []struct {
		ID     int                `json:"idIndicador"`
		Series map[string]*string `json:"series"`
	} `json:"indicadoresRaiz"`
}

func parse(data []byte) ([]adapter.Rate, error) {
	var t table
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	if t.Columns == nil || t.Indicators == nil {
		return nil, errors.New("response missing columnas/indicadoresRaiz envelope")
	}

	var series map[string]*string
	found := false
	for _, i := range t.Indicators {
		if i.ID == sellIndicator {
			series, found = i.Series, true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("sell indicator %d missing from group %d response", sellIndicator, groupID)
	}

	var rates []adapter.Rate
	if series == nil && len(t.Columns) > 1 {
		return nil, fmt.Errorf("sell indicator %d has no series", sellIndicator)
	}
	// The first column holds the indicator names.
	for _, col := range t.Columns[min(1, len(t.Columns)):] {
		index := strings.TrimPrefix(col.Field, "serie")
		value := series["serie"+index+"Ingles"]
		if value == nil || *value == "" {
			continue
		}
		// Titles look like "20 mar 2026"; Go matches month names
		// case-insensitively.
		date, err := time.Parse("2 Jan 2006", col.Title)
		if err != nil {
			return nil, err
		}
		rate, err := strconv.ParseFloat(strings.TrimSpace(*value), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid rate %q on %s: %w", *value, col.Title, err)
		}
		if rate == 0 {
			continue
		}
		rates = append(rates, adapter.Rate{Date: date, Base: "USD", Quote: "CRC", Rate: rate})
	}
	return rates, nil
}
