package rates

import (
	"context"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
)

// Table is one of the rate tables: daily rates or a provider rollup.
type Table struct {
	Name       string
	DateColumn string
	Precision  Precision
}

// The rate tables.
var (
	Daily   = Table{"rates", "date", Day}
	Weekly  = Table{"weekly_rates", "bucket_date", Week}
	Monthly = Table{"monthly_rates", "bucket_date", Month}

	Tables = []Table{Daily, Weekly, Monthly}
)

// Rollups are the provider rollup tables.
var Rollups = []Table{Weekly, Monthly}

// Query is a composable SELECT over a rate table, the counterpart of a Sequel dataset. Methods return modified copies.
type Query struct {
	Table  Table
	From   string   // table name, or a subquery aliased to it
	Select string   // "*" when empty
	Where  []string // conditions, ANDed
	Order  string
}

// Dataset selects every row of t.
func (t Table) Dataset() Query { return Query{Table: t, From: t.Name} }

// Filter adds a condition.
func (q Query) Filter(cond string) Query {
	q.Where = append(q.Where[:len(q.Where):len(q.Where)], cond)
	return q
}

// Columns replaces the select list.
func (q Query) Columns(sel string) Query {
	q.Select = sel
	return q
}

// OrderBy replaces the ordering.
func (q Query) OrderBy(order string) Query {
	q.Order = order
	return q
}

// Condition is the query's WHERE clause without the keyword, or "1" when it has none.
func (q Query) Condition() string {
	if len(q.Where) == 0 {
		return "1"
	}
	parts := make([]string, len(q.Where))
	for i, w := range q.Where {
		parts[i] = "(" + w + ")"
	}
	return strings.Join(parts, " AND ")
}

// SQL renders the query.
func (q Query) SQL() string {
	sel := q.Select
	if sel == "" {
		sel = "*"
	}
	var b strings.Builder
	b.WriteString("SELECT " + sel + " FROM " + q.From)
	if len(q.Where) > 0 {
		b.WriteString(" WHERE " + q.Condition())
	}
	if q.Order != "" {
		b.WriteString(" ORDER BY " + q.Order)
	}
	return b.String()
}

// NamedCondition keeps rows whose base and quote the Money gem can name (RateScopes.named_currencies).
func NamedCondition() string {
	list := db.LitList(currency.Codes())
	return "(base IN " + list + ") AND (quote IN " + list + ")"
}

// ExpiredCondition matches rows on or after either side's terminal date (RateScopes.expired_currency_condition).
// Rollup buckets are expired when they start after the bucket holding the day before the terminal date, so the
// boundary bucket survives.
func ExpiredCondition(dateColumn string, p Precision) string {
	entries := currency.DefunctCurrencies()
	if len(entries) == 0 {
		return "0"
	}
	parts := make([]string, len(entries))
	for i, e := range entries {
		var expired string
		if p == Day {
			expired = dateColumn + " >= " + db.LitDate(e.TerminalDate)
		} else {
			expired = dateColumn + " > " + BucketSQL(p, db.LitDate(e.TerminalDate.AddDate(0, 0, -1)))
		}
		code := db.Lit(e.ISOCode)
		parts[i] = "((" + expired + ") AND ((base = " + code + ") OR (quote = " + code + ")))"
	}
	return strings.Join(parts, " OR ")
}

// CurrentCondition keeps rows before either side's terminal date (RateScopes.current_currencies).
func CurrentCondition(dateColumn string, p Precision) string {
	return "NOT (" + ExpiredCondition(dateColumn, p) + ")"
}

// NonBlendingKeys lists providers whose values stand for longer than a day (Provider.non_blending_keys). Their rows
// never enter the blend or the currency catalogue: a value that stands for a month is served for the whole period, so
// on most days it is weeks stale, and the recency decay, which counts from the row date, cannot see that.
func NonBlendingKeys(ctx context.Context, q db.Querier) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		"SELECT key FROM providers WHERE coalesce(frequency, 'daily') != 'daily' ORDER BY key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// Blendable restricts t to rows that may enter a blend: named currencies, daily-frequency providers (all but
// nonBlending), before any terminal date. Rollups also recompute terminal-straddling buckets (see eligibleRollups).
func (t Table) Blendable(nonBlending []string) Query {
	q := t.Dataset().Filter(NamedCondition())
	if len(nonBlending) > 0 {
		q = q.Filter("provider NOT IN " + db.LitList(nonBlending))
	}
	q = q.Filter(CurrentCondition(t.DateColumn, t.Precision))
	if t.Precision == Day {
		return q
	}
	return eligibleRollups(q)
}

// eligibleRollups recomputes a terminal-straddling pair only when expired daily observations contaminate its
// average. Stored precision stays for every unaffected pair, including boundary buckets whose daily history is
// incomplete or absent.
func eligibleRollups(q Query) Query {
	t := q.Table
	entries := currency.DefunctCurrencies()
	if len(entries) == 0 {
		return q
	}
	boundaries := make([]string, len(entries))
	for i, e := range entries {
		last := BucketSQL(t.Precision, db.LitDate(e.TerminalDate.AddDate(0, 0, -1)))
		terminal := BucketSQL(t.Precision, db.LitDate(e.TerminalDate))
		code := db.Lit(e.ISOCode)
		boundaries[i] = "((bucket_date = " + last + ") AND (" + last + " = " + terminal + ") AND ((base = " + code +
			") OR (quote = " + code + ")))"
	}

	observations := "FROM rates WHERE (provider = " + t.Name + ".provider) AND (base = " + t.Name + ".base) AND (quote = " +
		t.Name + ".quote) AND (" + BucketSQL(t.Precision, "date") + " = " + t.Name + ".bucket_date)"
	expired := ExpiredCondition("date", Day)
	contaminated := "(" + strings.Join(boundaries, " OR ") + ") AND (EXISTS (SELECT * " + observations + " AND (" +
		expired + ")))"
	eligible := observations + " AND NOT (" + expired + ")"
	value := "(CASE WHEN (" + contaminated + ") THEN (SELECT avg(rate) " + eligible + ") ELSE rate END) AS rate"

	inner := q.Filter("NOT (" + contaminated + ") OR (EXISTS (SELECT * " + eligible + "))").
		Columns("bucket_date, provider, base, quote, " + value)
	return Query{Table: t, From: "(" + inner.SQL() + ") AS " + t.Name}
}

// Between restricts q to [start, end], widened back to the latest date on or before start so a range that opens on a
// weekend or holiday starts from the last publication, and orders by date and quote. A start after today yields
// nothing.
func (q Query) Between(start, end, today time.Time) Query {
	col := q.Table.DateColumn
	if start.After(today) {
		return q.Filter("0")
	}
	begin := db.LitDate(start)
	nearest := q.Columns(col).Filter(col+" <= "+begin).OrderBy(col+" DESC").SQL() + " LIMIT 1"
	return q.Filter(col + " >= coalesce((" + nearest + "), " + begin + ")").
		Filter(col + " <= " + db.LitDate(end)).
		OrderBy(col + ", quote")
}

// Only keeps pairs between a provider's pivot currency and one of currencies, on either side.
func (q Query) Only(currencies ...string) Query {
	list := db.LitList(currencies)
	q.From += " INNER JOIN providers ON (providers.key = " + q.Table.Name + ".provider)"
	return q.Columns(q.Table.Name + ".*").Filter("((base = providers.pivot_currency) AND (quote IN " + list +
		")) OR ((quote = providers.pivot_currency) AND (base IN " + list + "))")
}

// Downsample averages q's rows per provider, pair and bucket, as rows of base, provider, quote, rate and date (the
// bucket, as text), ordered by date.
func (q Query) Downsample(p Precision) string {
	bucket := BucketSQL(p, q.Table.DateColumn)
	return q.Columns("base, provider, quote, avg(rate) AS rate, "+bucket+" AS date").OrderBy("").SQL() +
		" GROUP BY base, provider, quote, " + bucket + " ORDER BY date"
}
