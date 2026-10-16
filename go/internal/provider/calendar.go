package provider

import (
	"fmt"
	"time"

	"github.com/adhocore/gronx"

	"github.com/lineofflight/frankfurter/go/internal/db"
)

// MissedSince counts the publications due after endDate (a stored date) and
// before reference. The unit follows the publish cadence: fire days of the
// schedule for daily, whole buckets for weekly, monthly and quarterly, where
// each fire is taken to cover the bucket before the one it lands in. ok is
// false when the provider has no schedule.
func (p Provider) MissedSince(endDate string, reference time.Time) (n int, ok bool, err error) {
	if p.PublishSchedule == "" {
		return 0, false, nil
	}
	if !gronx.IsValid(p.PublishSchedule) {
		return 0, false, fmt.Errorf("%s: invalid publish_schedule %q", p.Key, p.PublishSchedule)
	}
	last, err := db.ParseDate(endDate)
	if err != nil {
		return 0, false, fmt.Errorf("%s: end date: %w", p.Key, err)
	}
	switch p.PublishCadence {
	case "daily":
		return countFireDays(p.PublishSchedule, last, reference), true, nil
	case "weekly", "monthly", "quarterly":
		return countMissedBuckets(p.PublishSchedule, last, reference, p.PublishCadence), true, nil
	default:
		return 0, false, fmt.Errorf("%s: unknown publish_cadence %q", p.Key, p.PublishCadence)
	}
}

// countFireDays counts the days strictly between last and reference on which
// the schedule fires (in UTC).
func countFireDays(expr string, last, reference time.Time) int {
	n := 0
	for d := last.AddDate(0, 0, 1); d.Before(reference); d = d.AddDate(0, 0, 1) {
		if firesOn(expr, d) {
			n++
		}
	}
	return n
}

func firesOn(expr string, day time.Time) bool {
	next, err := gronx.NextTickAfter(expr, day.Add(-time.Second), false)
	return err == nil && next.Before(day.AddDate(0, 0, 1))
}

func countMissedBuckets(expr string, last, reference time.Time, granularity string) int {
	endOfDay := reference.Add(24*time.Hour - time.Second)
	fire, err := gronx.PrevTickBefore(expr, endOfDay, false)
	if err != nil {
		return 0
	}
	fireDate := time.Date(fire.Year(), fire.Month(), fire.Day(), 0, 0, 0, 0, time.UTC)
	expectedDataEnd := bucketStart(fireDate, granularity).AddDate(0, 0, -1)
	expected := bucketStart(expectedDataEnd, granularity)
	n := 0
	for cursor := bucketStart(last, granularity); cursor.Before(expected); n++ {
		switch granularity {
		case "weekly":
			cursor = cursor.AddDate(0, 0, 7)
		case "monthly":
			cursor = cursor.AddDate(0, 1, 0)
		case "quarterly":
			cursor = cursor.AddDate(0, 3, 0)
		}
	}
	return n
}

func bucketStart(d time.Time, granularity string) time.Time {
	switch granularity {
	case "weekly":
		return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
	case "monthly":
		return time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
	default: // quarterly
		return time.Date(d.Year(), (d.Month()-1)/3*3+1, 1, 0, 0, 0, 0, time.UTC)
	}
}
