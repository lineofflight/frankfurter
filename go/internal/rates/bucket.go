// Package rates is the rate domain over SQLite: the rate tables and their
// scopes, bucketing, ingest precision and components, validation and purging,
// provider rollups, currency summaries, carry-forward snapshots, output
// rounding, and provider seeding.
//
// Scopes are SQL text. Constants (currency codes, dates) are inlined as
// literals through db.Lit, so fragments compose by concatenation the way Sequel
// datasets chain.
package rates

import (
	"fmt"
	"time"
)

// Precision is a rollup granularity. The zero value is daily.
type Precision string

// Rollup precisions.
const (
	Day   Precision = ""
	Week  Precision = "week"
	Month Precision = "month"
)

// WeekBucket is the SQL for the week bucket of a date expression: January 1
// plus seven days per strftime %W week, so days before the year's first Monday
// fall in a bucket dated January 1.
func WeekBucket(expr string) string {
	return fmt.Sprintf("date(strftime('%%Y-%%m-%%d', strftime('%%Y-01-01', %s), ('+' || (CAST(strftime('%%W', %s) AS integer) * 7) || ' days')))", expr, expr)
}

// MonthBucket is the SQL for the month bucket of a date expression: its first
// day.
func MonthBucket(expr string) string {
	return fmt.Sprintf("strftime('%%Y-%%m-01', %s)", expr)
}

// BucketSQL is Bucket.expression: the bucket of expr at precision p, or expr
// itself when daily.
func BucketSQL(p Precision, expr string) string {
	switch p {
	case Week:
		return WeekBucket(expr)
	case Month:
		return MonthBucket(expr)
	}
	return expr
}

// Bucket computes in Go what BucketSQL computes in SQLite.
func Bucket(p Precision, t time.Time) time.Time {
	y, m, _ := t.Date()
	switch p {
	case Week:
		yday := t.YearDay() - 1
		monday := (int(t.Weekday()) + 6) % 7
		week := (yday + 7 - monday) / 7
		return time.Date(y, 1, 1+7*week, 0, 0, 0, 0, time.UTC)
	case Month:
		return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
	}
	return time.Date(y, m, t.Day(), 0, 0, 0, 0, time.UTC)
}

// SpanSQL is Bucket.span: a date range holding every date of the bucket
// bucketExpr names at precision p, so a scan can seek a date index on dateExpr
// before the exact bucket test. Week buckets count whole weeks from 1 January,
// so a week's dates sit from 7 days before its bucket date to 5 after.
func SpanSQL(p Precision, bucketExpr, dateExpr string) string {
	from, to := "+0 days", "+1 month"
	if p == Week {
		from, to = "-7 days", "+6 days"
	}
	return "(" + dateExpr + " >= date(" + bucketExpr + ", '" + from + "')) AND (" + dateExpr + " < date(" + bucketExpr +
		", '" + to + "'))"
}
