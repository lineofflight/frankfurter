package migrate

import (
	"context"
	"database/sql"
	"slices"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// relabelOldManat is 027. The Bank of Lithuania labels its old-manat series as
// AZN without restating the values (1 "AZN" = 0.00063 LTL on 2005-12-30 is old
// manat) and switched to new manat on 2006-01-09. The adapter now emits AZM
// before that date; this relabels what is stored, then rebuilds LB's AZN and
// AZM rollups and both codes' currency summaries from the relabelled rates.
//
// Ruby also relabels the pre-2006 AZN blends and recomputes January 2006 with
// the blend code of today, which reads columns and tables this old schema
// lacks. When any row was relabelled, Go clears blended_rates instead, handing
// the daily blend to the scheduler's rebuild (as 036-040 do); on a database
// with nothing to relabel both leave it alone.
func relabelOldManat(ctx context.Context, q db.Querier) error {
	codes := []string{"AZN", "AZM"}
	res, err := q.ExecContext(ctx,
		"UPDATE `rates` SET `base` = 'AZM' WHERE `provider` = 'LB' AND `base` = 'AZN' AND `date` < '2006-01-09'")
	if err != nil {
		return err
	}
	scope := "`provider` = 'LB' AND `base` IN " + db.LitList(codes)
	if err := rebuildRollups(ctx, q, scope); err != nil {
		return err
	}
	if err := resummarize(ctx, q, codes); err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return exec(ctx, q, "DELETE FROM `blended_rates`")
	}
	return nil
}

type relabel struct {
	provider, code, predecessor, cutover string
}

// predecessorSeries are 028's seven series in the shape of 027: a source labels
// a currency's whole history with its current code without restating values
// across a redenomination (issue #623).
var predecessorSeries = []relabel{
	{"LB", "TMT", "TMM", "2009-01-01"},
	{"SARB", "ZMW", "ZMK", "2013-01-01"},
	{"RB", "RUB", "RUR", "1998-01-01"},
	{"CBU", "RUB", "RUR", "1998-01-06"},
	{"BCCH", "BRL", "BRR", "1994-07-01"},
	{"CBR", "TJS", "TJR", "2000-10-30"},
	{"NBU", "TJS", "TJR", "2000-10-30"},
}

// relabelPredecessors is 028: relabel each series before its cutover, rebuild
// the touched providers' rollups and the codes' currency summaries. Ruby then
// recomputes the blend from the earliest relabelled row to the latest plus the
// carry-forward lookback; Go clears blended_rates for the scheduler's rebuild,
// for the reason given at 027.
func relabelPredecessors(ctx context.Context, q db.Querier) error {
	relabelled := false
	for _, s := range predecessorSeries {
		scope := "`provider` = " + db.Lit(s.provider) + " AND " + either(s.code) + " AND `date` < " + db.Lit(s.cutover)
		var first sql.NullString
		if err := q.QueryRowContext(ctx, "SELECT min(`date`) FROM `rates` WHERE "+scope).Scan(&first); err != nil {
			return err
		}
		if !first.Valid {
			continue
		}
		relabelled = true
		if err := exec(ctx, q,
			"UPDATE `rates` SET `base` = "+db.Lit(s.predecessor)+" WHERE "+scope+" AND `base` = "+db.Lit(s.code),
			"UPDATE `rates` SET `quote` = "+db.Lit(s.predecessor)+" WHERE "+scope+" AND `quote` = "+db.Lit(s.code),
		); err != nil {
			return err
		}
	}

	var providers []string
	groups := map[string][]string{}
	var codes []string
	for _, s := range predecessorSeries {
		if _, ok := groups[s.provider]; !ok {
			providers = append(providers, s.provider)
		}
		for _, c := range []string{s.code, s.predecessor} {
			if !slices.Contains(groups[s.provider], c) {
				groups[s.provider] = append(groups[s.provider], c)
			}
			if !slices.Contains(codes, c) {
				codes = append(codes, c)
			}
		}
	}
	for _, p := range providers {
		if err := rebuildRollups(ctx, q, "`provider` = "+db.Lit(p)+" AND "+either(groups[p]...)); err != nil {
			return err
		}
	}
	if err := resummarize(ctx, q, codes); err != nil {
		return err
	}
	if relabelled {
		return exec(ctx, q, "DELETE FROM `blended_rates`")
	}
	return nil
}

// rebuildRollups replaces the weekly and monthly rollups of the rates matching
// scope.
func rebuildRollups(ctx context.Context, q db.Querier, scope string) error {
	for _, t := range []struct{ table, bucket string }{{"weekly_rates", weekBucket}, {"monthly_rates", monthBucket}} {
		if err := exec(ctx, q,
			"DELETE FROM `"+t.table+"` WHERE "+scope,
			rollupInsert(t.table, t.bucket, scope),
		); err != nil {
			return err
		}
	}
	return nil
}

// resummarize recomputes each code's provider coverages and catalogue row from
// every stored rate, the way 027 and 028 do by hand (they predate
// CurrencySummary).
func resummarize(ctx context.Context, q db.Querier, codes []string) error {
	for _, code := range codes {
		c := db.Lit(code)
		if err := exec(ctx, q,
			"DELETE FROM `currency_coverages` WHERE `iso_code` = "+c,
			"INSERT INTO `currency_coverages` (`provider_key`, `iso_code`, `start_date`, `end_date`) SELECT `provider`, "+
				c+", min(`date`), max(`date`) FROM `rates` WHERE `base` = "+c+" OR `quote` = "+c+" GROUP BY `provider`",
			"DELETE FROM `currencies` WHERE `iso_code` = "+c,
			"INSERT INTO `currencies` (`iso_code`, `start_date`, `end_date`) SELECT "+c+
				", min(`start_date`), max(`end_date`) FROM `currency_coverages` WHERE `iso_code` = "+c+
				" HAVING min(`start_date`) IS NOT NULL",
		); err != nil {
			return err
		}
	}
	return nil
}

// relabelOldAfghani is 042. Banca d'Italia labels its old-afghani quotes AFN.
// Until 2004-03-31 they hold the frozen official rate of 4750 AFA per dollar,
// long past the October 2002 redenomination, and on 2004-04-01 they switch to
// 47.5 new afghani. The adapter now emits AFA before that date; this relabels
// what is stored. The values are right and only the code is wrong, so rows are
// updated in place rather than refetched.
//
// BDI was the only daily AFN source before NBP joined on 2003-11-12, so the
// AFN blend ran at old-afghani magnitudes from 1999 and averaged them with
// NBP's new afghani until the switch. BDI's AFA and AFN rollups are rebuilt for
// the affected buckets and both codes' coverage is recomputed; AFA's defunct
// entry keeps the rows from 2002-10-07 out of blends and the catalogue. As in
// 041, the grouped blends for those buckets are dropped and the daily blend is
// cleared for the scheduler to rebuild.
func relabelOldAfghani(ctx context.Context, q db.Querier) error {
	label := retiredLabel{provider: "BDI", from: "AFN", to: "AFA"}
	scope := "`provider` = 'BDI' AND " + either("AFN") + " AND `date` < '2004-04-01'"
	var needed bool
	if err := q.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM `rates` WHERE "+scope+")").
		Scan(&needed); err != nil {
		return err
	}
	if !needed {
		return nil
	}

	buckets := make([][]string, len(retiredRollups))
	for i, t := range retiredRollups {
		dates, err := column(ctx, q, "SELECT DISTINCT "+t.bucket+" FROM `rates` WHERE "+scope)
		if err != nil {
			return err
		}
		buckets[i] = dates
	}
	rows, err := legacyRows(ctx, q, scope)
	if err != nil {
		return err
	}
	for _, r := range rows {
		// Equal duplicates collapse. A disagreement needs source evidence, not
		// an arbitrary winner.
		if err := relabelRetiredRow(ctx, q, label, r); err != nil {
			return err
		}
	}

	codes := []string{"AFA", "AFN"}
	bdi := "`provider` = 'BDI' AND " + either(codes...)
	for i, t := range retiredRollups {
		list := db.LitList(buckets[i])
		if err := exec(ctx, q,
			"DELETE FROM `"+t.blended+"` WHERE `bucket_date` IN "+list,
			"DELETE FROM `"+t.table+"` WHERE "+bdi+" AND `bucket_date` IN "+list,
			rollupInsert(t.table, t.bucket, bdi+" AND "+t.bucket+" IN "+list),
		); err != nil {
			return err
		}
	}
	if err := rates.RefreshSummaries(ctx, q, codes, "BDI"); err != nil {
		return err
	}

	// Daily readiness checks only the earliest date. Partial invalidation
	// could serve an incomplete table, so clear it entirely. The scheduler
	// rebuilds daily history and populates missing grouped buckets after
	// startup.
	return exec(ctx, q, "DELETE FROM `blended_rates`")
}
