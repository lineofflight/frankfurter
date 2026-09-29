package migrate

import (
	"context"
	"database/sql"
	"slices"

	"github.com/lineofflight/frankfurter/go/internal/db"
)

// relabelOldManat is 027. The Bank of Lithuania labels its old-manat series as AZN without restating the values
// (1 "AZN" = 0.00063 LTL on 2005-12-30 is old manat) and switched to new manat on 2006-01-09. The adapter now emits
// AZM before that date; this relabels what is stored, then rebuilds LB's AZN and AZM rollups and both codes' currency
// summaries from the relabelled rates.
//
// Ruby also relabels the pre-2006 AZN blends and recomputes January 2006 with the blend code of today, which reads
// columns and tables this old schema lacks. When any row was relabelled, Go clears blended_rates instead, handing the
// daily blend to the scheduler's rebuild (as 036-040 do); on a database with nothing to relabel both leave it alone.
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

// predecessorSeries are 028's seven series in the shape of 027: a source labels a currency's whole history with its
// current code without restating values across a redenomination (issue #623).
var predecessorSeries = []relabel{
	{"LB", "TMT", "TMM", "2009-01-01"},
	{"SARB", "ZMW", "ZMK", "2013-01-01"},
	{"RB", "RUB", "RUR", "1998-01-01"},
	{"CBU", "RUB", "RUR", "1998-01-06"},
	{"BCCH", "BRL", "BRR", "1994-07-01"},
	{"CBR", "TJS", "TJR", "2000-10-30"},
	{"NBU", "TJS", "TJR", "2000-10-30"},
}

// relabelPredecessors is 028: relabel each series before its cutover, rebuild the touched providers' rollups and the
// codes' currency summaries. Ruby then recomputes the blend from the earliest relabelled row to the latest plus the
// carry-forward lookback; Go clears blended_rates for the scheduler's rebuild, for the reason given at 027.
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

// rebuildRollups replaces the weekly and monthly rollups of the rates matching scope.
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

// resummarize recomputes each code's provider coverages and catalogue row from every stored rate, the way 027 and 028
// do by hand (they predate CurrencySummary).
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
