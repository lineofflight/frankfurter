package migrate

import (
	"context"

	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

var (
	weekBucket  = rates.WeekBucket("`date`")
	monthBucket = rates.MonthBucket("`date`")
)

// rollupInsert averages rates matching where into table's buckets, as
// Provider#refresh_rollup does.
func rollupInsert(table, bucket, where string) string {
	if where != "" {
		where = " WHERE " + where
	}
	return "INSERT INTO `" + table + "` (`bucket_date`, `provider`, `base`, `quote`, `rate`) SELECT " + bucket +
		", `provider`, `base`, `quote`, avg(`rate`) FROM `rates`" + where + " GROUP BY `provider`, `base`, `quote`, " +
		bucket
}

var migrations = []Migration{
	{Version: 1, Name: "create_currencies",
		Up: sqlStep(
			"CREATE TABLE `rates` (`date` date NOT NULL, `base` varchar(255) NOT NULL, `quote` varchar(255) NOT NULL, `rate` double precision NOT NULL, `provider` varchar(255) NOT NULL)",
			"CREATE UNIQUE INDEX `rates_provider_date_quote_index` ON `rates` (`provider`, `date`, `quote`)",
		),
		Down: sqlStep("DROP TABLE `rates`"),
	},
	{Version: 2, Name: "add_base_to_unique_index",
		Up: sqlStep(
			"DROP INDEX `rates_provider_date_quote_index`",
			"CREATE UNIQUE INDEX `rates_provider_date_base_quote_index` ON `rates` (`provider`, `date`, `base`, `quote`)",
		),
		Down: sqlStep(
			"DROP INDEX `rates_provider_date_base_quote_index`",
			"CREATE UNIQUE INDEX `rates_provider_date_quote_index` ON `rates` (`provider`, `date`, `quote`)",
		),
	},
	{Version: 3, Name: "clear_boc_data",
		Up: sqlStep("DELETE FROM `rates` WHERE `provider` IN ('BOC', 'CBA', 'CBR', 'NBP', 'NBRB', 'NBU', 'BNM')"),
	},
	{Version: 4, Name: "create_providers",
		Up: sqlStep(
			"CREATE TABLE `providers` (`key` varchar(255) NOT NULL PRIMARY KEY, `name` varchar(255) NOT NULL, `description` varchar(255), `url` varchar(255))",
		),
		Down: sqlStep("DROP TABLE `providers`"),
	},
	{Version: 5, Name: "clear_tcmb_data",
		Up: sqlStep("DELETE FROM `rates` WHERE (`provider` = 'TCMB')"),
	},
	{Version: 6, Name: "rename_provider_url",
		Up: sqlStep(
			"ALTER TABLE `providers` RENAME COLUMN `url` TO `data_url`",
			"ALTER TABLE `providers` ADD COLUMN `terms_url` varchar(255)",
		),
		Down: sqlStep(
			"ALTER TABLE `providers` DROP COLUMN `terms_url`",
			"ALTER TABLE `providers` RENAME COLUMN `data_url` TO `url`",
		),
	},
	{Version: 7, Name: "add_provider_currency_indexes",
		Up: sqlStep(
			"CREATE INDEX `rates_provider_quote_index` ON `rates` (`provider`, `quote`)",
			"CREATE INDEX `rates_provider_base_index` ON `rates` (`provider`, `base`)",
		),
		Down: sqlStep(
			"DROP INDEX `rates_provider_base_index`",
			"DROP INDEX `rates_provider_quote_index`",
		),
	},
	{Version: 8, Name: "merge_nbp_providers",
		// Delete NBP.B rows that overlap with NBP (pre-2004 when both tables
		// carried EUR, USD, etc.)
		Up: sqlStep(
			"DELETE FROM rates WHERE provider = 'NBP.B' AND (date, base, quote) IN (SELECT date, base, quote FROM rates WHERE provider = 'NBP')",
			"UPDATE `rates` SET `provider` = 'NBP' WHERE (`provider` = 'NBP.B')",
			"DELETE FROM `providers` WHERE (`key` = 'NBP.B')",
		),
		Down: irreversible,
	},
	{Version: 9, Name: "add_date_index",
		Up:   sqlStep("CREATE INDEX `rates_date_index` ON `rates` (`date`)"),
		Down: sqlStep("DROP INDEX `rates_date_index`"),
	},
	{Version: 10, Name: "add_provider_publish_schedule",
		Up: sqlStep(
			"ALTER TABLE `providers` ADD COLUMN `publish_time` integer",
			"ALTER TABLE `providers` ADD COLUMN `publish_days` varchar(255)",
		),
		Down: sqlStep(
			"ALTER TABLE `providers` DROP COLUMN `publish_days`",
			"ALTER TABLE `providers` DROP COLUMN `publish_time`",
		),
	},
	{Version: 11, Name: "rename_boj_to_boja",
		Up:   renameProvider("BOJ", "BOJA", "rates", "providers"),
		Down: renameProvider("BOJA", "BOJ", "rates", "providers"),
	},
	{Version: 12, Name: "add_outlier_to_rates",
		Up:   sqlStep("ALTER TABLE `rates` ADD COLUMN `outlier` boolean DEFAULT (0) NOT NULL"),
		Down: sqlStep("ALTER TABLE `rates` DROP COLUMN `outlier`"),
	},
	{Version: 13, Name: "drop_outlier_from_rates",
		Up:   sqlStep("ALTER TABLE `rates` DROP COLUMN `outlier`"),
		Down: sqlStep("ALTER TABLE `rates` ADD COLUMN `outlier` boolean DEFAULT (0)"),
	},
	{Version: 14, Name: "add_coverage_start_to_providers",
		Up:   sqlStep("ALTER TABLE `providers` ADD COLUMN `coverage_start` date"),
		Down: sqlStep("ALTER TABLE `providers` DROP COLUMN `coverage_start`"),
	},
	{Version: 15, Name: "rename_bot_to_bota",
		Up:   renameProvider("BOT", "BOTA", "rates", "providers"),
		Down: renameProvider("BOTA", "BOT", "rates", "providers"),
	},
	{Version: 16, Name: "add_pivot_currency_to_providers",
		Up:   sqlStep("ALTER TABLE `providers` ADD COLUMN `pivot_currency` varchar(255)"),
		Down: sqlStep("ALTER TABLE `providers` DROP COLUMN `pivot_currency`"),
	},
	{Version: 17, Name: "create_rollup_tables",
		Up: sqlStep(
			"CREATE TABLE `weekly_rates` (`bucket_date` date NOT NULL, `provider` varchar(255) NOT NULL, `base` varchar(255) NOT NULL, `quote` varchar(255) NOT NULL, `rate` double precision NOT NULL, PRIMARY KEY (`provider`, `bucket_date`, `base`, `quote`))",
			"CREATE INDEX `weekly_rates_bucket_date_quote_index` ON `weekly_rates` (`bucket_date`, `quote`)",
			"CREATE TABLE `monthly_rates` (`bucket_date` date NOT NULL, `provider` varchar(255) NOT NULL, `base` varchar(255) NOT NULL, `quote` varchar(255) NOT NULL, `rate` double precision NOT NULL, PRIMARY KEY (`provider`, `bucket_date`, `base`, `quote`))",
			"CREATE INDEX `monthly_rates_bucket_date_quote_index` ON `monthly_rates` (`bucket_date`, `quote`)",
			rollupInsert("weekly_rates", weekBucket, ""),
			rollupInsert("monthly_rates", monthBucket, ""),
		),
		Down: sqlStep("DROP TABLE `weekly_rates`", "DROP TABLE `monthly_rates`"),
	},
	{Version: 18, Name: "create_currency_tables",
		Up: sqlStep(
			"CREATE TABLE `currencies` (`iso_code` varchar(255) NOT NULL PRIMARY KEY, `start_date` date NOT NULL, `end_date` date NOT NULL)",
			"CREATE TABLE `currency_coverages` (`provider_key` varchar(255) NOT NULL, `iso_code` varchar(255) NOT NULL, PRIMARY KEY (`provider_key`, `iso_code`))",
			`INSERT INTO currencies (iso_code, start_date, end_date)
SELECT iso_code, MIN(start_date), MAX(end_date)
FROM (
  SELECT quote AS iso_code, MIN(date) AS start_date, MAX(date) AS end_date
  FROM rates GROUP BY quote
  UNION ALL
  SELECT base AS iso_code, MIN(date) AS start_date, MAX(date) AS end_date
  FROM rates GROUP BY base
)
GROUP BY iso_code
ORDER BY iso_code`,
			`INSERT INTO currency_coverages (provider_key, iso_code)
SELECT provider, iso_code FROM (
  SELECT DISTINCT provider, quote AS iso_code FROM rates
  UNION
  SELECT DISTINCT provider, base AS iso_code FROM rates
)
ORDER BY provider, iso_code`,
		),
		Down: sqlStep("DROP TABLE `currency_coverages`", "DROP TABLE `currencies`"),
	},
	{Version: 19, Name: "add_dates_to_currency_coverages",
		Up: sqlStep(
			"ALTER TABLE `currency_coverages` ADD COLUMN `start_date` date",
			"ALTER TABLE `currency_coverages` ADD COLUMN `end_date` date",
			`UPDATE currency_coverages
SET start_date = (
  SELECT MIN(date) FROM rates
  WHERE rates.provider = currency_coverages.provider_key
    AND (rates.quote = currency_coverages.iso_code OR rates.base = currency_coverages.iso_code)
),
end_date = (
  SELECT MAX(date) FROM rates
  WHERE rates.provider = currency_coverages.provider_key
    AND (rates.quote = currency_coverages.iso_code OR rates.base = currency_coverages.iso_code)
)`,
		),
		Down: sqlStep(
			"ALTER TABLE `currency_coverages` DROP COLUMN `start_date`",
			"ALTER TABLE `currency_coverages` DROP COLUMN `end_date`",
		),
	},
	{Version: 20, Name: "replace_description_with_rate_type_and_country_code",
		Up: sqlStep(
			"ALTER TABLE `providers` ADD COLUMN `rate_type` varchar(255)",
			"ALTER TABLE `providers` ADD COLUMN `country_code` varchar(2)",
			"ALTER TABLE `providers` DROP COLUMN `description`",
		),
		Down: sqlStep(
			"ALTER TABLE `providers` ADD COLUMN `description` varchar(255)",
			"ALTER TABLE `providers` DROP COLUMN `rate_type`",
			"ALTER TABLE `providers` DROP COLUMN `country_code`",
		),
	},
	{Version: 21, Name: "rename_bnm_to_nbm",
		Up: sqlStep(
			"UPDATE `rates` SET `provider` = 'NBM' WHERE (`provider` = 'BNM')",
			"UPDATE `currency_coverages` SET `provider_key` = 'NBM' WHERE (`provider_key` = 'BNM')",
			// Clear stale NBM rollups from before the original rename, then
			// re-key BNM.
			"DELETE FROM `weekly_rates` WHERE (`provider` = 'NBM')",
			"UPDATE `weekly_rates` SET `provider` = 'NBM' WHERE (`provider` = 'BNM')",
			"DELETE FROM `monthly_rates` WHERE (`provider` = 'NBM')",
			"UPDATE `monthly_rates` SET `provider` = 'NBM' WHERE (`provider` = 'BNM')",
		),
		Down: sqlStep(
			"UPDATE `currency_coverages` SET `provider_key` = 'BNM' WHERE (`provider_key` = 'NBM')",
			"UPDATE `rates` SET `provider` = 'BNM' WHERE (`provider` = 'NBM')",
			"UPDATE `weekly_rates` SET `provider` = 'BNM' WHERE (`provider` = 'NBM')",
			"UPDATE `monthly_rates` SET `provider` = 'BNM' WHERE (`provider` = 'NBM')",
		),
	},
	{Version: 22, Name: "replace_publish_time_and_days_with_publish_schedule",
		Up: sqlStep(
			"ALTER TABLE `providers` ADD COLUMN `publish_schedule` varchar(255)",
			"ALTER TABLE `providers` ADD COLUMN `publish_cadence` varchar(255)",
			"ALTER TABLE `providers` DROP COLUMN `publish_time`",
			"ALTER TABLE `providers` DROP COLUMN `publish_days`",
		),
		Down: sqlStep(
			"ALTER TABLE `providers` ADD COLUMN `publish_time` integer",
			"ALTER TABLE `providers` ADD COLUMN `publish_days` varchar(255)",
			"ALTER TABLE `providers` DROP COLUMN `publish_schedule`",
			"ALTER TABLE `providers` DROP COLUMN `publish_cadence`",
		),
	},
	{Version: 23, Name: "rename_mongolbank_to_bom",
		Up:   renameProvider("MONGOLBANK", "BOM", "rates", "currency_coverages", "weekly_rates", "monthly_rates"),
		Down: renameProvider("BOM", "MONGOLBANK", "rates", "currency_coverages", "weekly_rates", "monthly_rates"),
	},
	{Version: 24, Name: "create_blended_rates",
		Up: sqlStep(
			"CREATE TABLE `blended_rates` (`date` date NOT NULL, `quote` varchar(255) NOT NULL, `rate` double precision NOT NULL, PRIMARY KEY (`quote`, `date`))",
			"CREATE INDEX `blended_rates_date_index` ON `blended_rates` (`date`)",
		),
		Down: sqlStep("DROP TABLE `blended_rates`"),
	},
	{Version: 25, Name: "strip_float_noise_from_rates",
		// Rows stored before RatePrecision existed carry binary float noise
		// from adapter arithmetic, which single-provider responses echo
		// verbatim (issue #579); round in place rather than re-backfill.
		// Rollups and blends are averages and round on output, so they are left
		// alone. Irreversible: the digits were noise.
		Up: sqlStep("UPDATE `rates` SET `rate` = " + rates.PrecisionSQL("`rate`") + " WHERE (`rate` != " +
			rates.PrecisionSQL("`rate`") + ")"),
	},
	{Version: 26, Name: "drop_inverted_cbk_cross_rates",
		// CBK's four East African cross rates were inverted by the adapter; the
		// orientation moved within the unique index, so the stale rows are
		// deleted for a re-backfill to fetch the published values (issue #585).
		Up: sqlStep(
			"DELETE FROM `rates` WHERE ((`provider` = 'CBK') AND (`base` IN ('UGX', 'TZS', 'RWF', 'BIF')) AND (`quote` = 'KES'))",
			"DELETE FROM `weekly_rates` WHERE ((`provider` = 'CBK') AND (`base` IN ('UGX', 'TZS', 'RWF', 'BIF')) AND (`quote` = 'KES'))",
			"DELETE FROM `monthly_rates` WHERE ((`provider` = 'CBK') AND (`base` IN ('UGX', 'TZS', 'RWF', 'BIF')) AND (`quote` = 'KES'))",
		),
	},
	{Version: 27, Name: "relabel_lb_old_manat", Up: relabelOldManat},
	{Version: 28, Name: "relabel_predecessor_currencies", Up: relabelPredecessors},
	{Version: 29, Name: "add_frequency_to_providers",
		Up:   sqlStep("ALTER TABLE `providers` ADD COLUMN `frequency` varchar(255) DEFAULT ('daily') NOT NULL"),
		Down: sqlStep("ALTER TABLE `providers` DROP COLUMN `frequency`"),
	},
	{Version: 30, Name: "create_blended_rollups",
		Up: sqlStep(
			"CREATE TABLE `blended_weekly_rates` (`bucket_date` date NOT NULL, `quote` varchar(255) NOT NULL, `rate` double precision NOT NULL, PRIMARY KEY (`quote`, `bucket_date`))",
			"CREATE INDEX `blended_weekly_rates_bucket_date_index` ON `blended_weekly_rates` (`bucket_date`)",
			"CREATE TABLE `blended_monthly_rates` (`bucket_date` date NOT NULL, `quote` varchar(255) NOT NULL, `rate` double precision NOT NULL, PRIMARY KEY (`quote`, `bucket_date`))",
			"CREATE INDEX `blended_monthly_rates_bucket_date_index` ON `blended_monthly_rates` (`bucket_date`)",
		),
		Down: sqlStep("DROP TABLE `blended_weekly_rates`", "DROP TABLE `blended_monthly_rates`"),
	},
	{Version: 31, Name: "retain_rate_components",
		// Old rows keep their exact effective value as mid. Sequel's
		// set_column_allow_null rebuilds the table, hence the backup copy. The
		// generated rate calls frankfurter_midpoint, a function no longer
		// registered: 033 replaces it with plain SQL, and SQLite only resolves
		// the function when the column is read.
		Up: sqlStep(
			"ALTER TABLE rates RENAME COLUMN rate TO mid",
			"ALTER TABLE `rates` RENAME TO `rates_backup0`",
			"CREATE TABLE `rates`(`date` date DEFAULT (NULL) NOT NULL, `base` varchar(255) DEFAULT (NULL) NOT NULL, `quote` varchar(255) DEFAULT (NULL) NOT NULL, `mid` double precision DEFAULT (NULL) NULL, `provider` varchar(255) DEFAULT (NULL) NOT NULL)",
			"INSERT INTO `rates`(`date`, `base`, `quote`, `mid`, `provider`) SELECT `date`, `base`, `quote`, `mid`, `provider` FROM `rates_backup0`",
			"DROP TABLE `rates_backup0`",
			"CREATE INDEX `rates_date_index` ON `rates` (`date`)",
			"CREATE INDEX `rates_provider_base_index` ON `rates` (`provider`, `base`)",
			"CREATE INDEX `rates_provider_quote_index` ON `rates` (`provider`, `quote`)",
			"CREATE UNIQUE INDEX `rates_provider_date_base_quote_index` ON `rates` (`provider`, `date`, `base`, `quote`)",
			"ALTER TABLE `rates` ADD COLUMN `bid` double precision",
			"ALTER TABLE `rates` ADD COLUMN `ask` double precision",
			`ALTER TABLE rates ADD COLUMN rate REAL GENERATED ALWAYS AS (
  COALESCE(mid,
    CASE
      WHEN provider = 'BOJA' AND bid = 0 THEN frankfurter_midpoint(ask, ask)
      ELSE frankfurter_midpoint(bid, ask)
    END
  )
) VIRTUAL
`,
		),
		Down: sqlStep(
			"UPDATE rates SET mid = rate",
			"ALTER TABLE rates DROP COLUMN rate",
			"ALTER TABLE rates DROP COLUMN bid",
			"ALTER TABLE rates DROP COLUMN ask",
			"ALTER TABLE rates RENAME COLUMN mid TO rate",
			"ALTER TABLE `rates` RENAME TO `rates_backup0`",
			"CREATE TABLE `rates`(`date` date DEFAULT (NULL) NOT NULL, `base` varchar(255) DEFAULT (NULL) NOT NULL, `quote` varchar(255) DEFAULT (NULL) NOT NULL, `rate` double precision DEFAULT (NULL) NOT NULL, `provider` varchar(255) DEFAULT (NULL) NOT NULL)",
			"INSERT INTO `rates`(`date`, `base`, `quote`, `rate`, `provider`) SELECT `date`, `base`, `quote`, `rate`, `provider` FROM `rates_backup0`",
			"DROP TABLE `rates_backup0`",
			"CREATE UNIQUE INDEX `rates_provider_date_base_quote_index` ON `rates` (`provider`, `date`, `base`, `quote`)",
			"CREATE INDEX `rates_provider_quote_index` ON `rates` (`provider`, `quote`)",
			"CREATE INDEX `rates_provider_base_index` ON `rates` (`provider`, `base`)",
			"CREATE INDEX `rates_date_index` ON `rates` (`date`)",
		),
	},
	{Version: 32, Name: "cover_grouped_source_dates",
		// Coverage and snap-back reads select dates while excluding
		// non-blending providers.
		Up:   sqlStep("CREATE INDEX `weekly_rates_bucket_date_provider_index` ON `weekly_rates` (`bucket_date`, `provider`)"),
		Down: sqlStep("DROP INDEX `weekly_rates_bucket_date_provider_index`"),
	},
	{Version: 33, Name: "resolve_rate_midpoint_in_sql",
		// Down keeps the SQL resolver: 031 can still roll back by copying
		// effective rates into mid.
		Up: sqlStep(
			"ALTER TABLE rates DROP COLUMN rate",
			`ALTER TABLE rates ADD COLUMN rate REAL GENERATED ALWAYS AS (
  COALESCE(mid,
    CASE
      WHEN provider = 'BOJA' AND bid = 0 THEN ask
      WHEN bid IS NOT NULL AND ask IS NOT NULL
        THEN CAST(printf('%.12g', (bid + ask) / 2.0) AS REAL)
    END
  )
) VIRTUAL
`,
		),
	},
	{Version: 34, Name: "create_currency_exclusions", Up: createCurrencyExclusions,
		Down: sqlStep("DROP TABLE `currency_exclusions`"),
	},
	{Version: 35, Name: "cover_currency_eligible_rollups",
		Up: sqlStep(
			"DROP INDEX `weekly_rates_bucket_date_provider_index`",
			"CREATE INDEX `weekly_rates_bucket_date_provider_base_quote_index` ON `weekly_rates` (`bucket_date`, `provider`, `base`, `quote`)",
			"CREATE INDEX `monthly_rates_bucket_date_provider_base_quote_index` ON `monthly_rates` (`bucket_date`, `provider`, `base`, `quote`)",
		),
		Down: sqlStep(
			"DROP INDEX `weekly_rates_bucket_date_provider_base_quote_index`",
			"DROP INDEX `monthly_rates_bucket_date_provider_base_quote_index`",
			"CREATE INDEX `weekly_rates_bucket_date_provider_index` ON `weekly_rates` (`bucket_date`, `provider`)",
		),
	},
	{Version: 36, Name: "normalize_bota_sdr", Up: normalizeSDR("BOTA", "TZS")},
	{Version: 37, Name: "recognize_comesa_dollar", Up: recognizeComesaDollar},
	{Version: 38, Name: "normalize_rba_sdr", Up: normalizeSDR("RBA", "AUD")},
	{Version: 39, Name: "normalize_bnm_sdr", Up: normalizeSDR("BNM", "MYR")},
	{Version: 40, Name: "normalize_rbm_sdr", Up: normalizeSDR("RBM", "MWK")},
	{Version: 41, Name: "repair_retired_currency_labels", Up: repairRetiredLabels},
	{Version: 42, Name: "relabel_bdi_old_afghani", Up: relabelOldAfghani},
	{Version: 43, Name: "relabel_successor_values", Up: relabelSuccessorValues},
	{Version: 44, Name: "relabel_nbrm_ecu", Up: relabelNBRMECU},
	{Version: 45, Name: "screen_rate_spikes", Up: screenRateSpikes, Down: sqlStep("DROP TABLE `rate_spikes`")},
	{Version: 46, Name: "relabel_lb_and_cba_units", Up: relabelLBAndCBAUnits},
}

// renameProvider re-keys a provider in each table (provider, or
// key/provider_key where the table names it so).
func renameProvider(from, to string, tables ...string) func(context.Context, db.Querier) error {
	var statements []string
	for _, t := range tables {
		col := "provider"
		switch t {
		case "providers":
			col = "key"
		case "currency_coverages":
			col = "provider_key"
		}
		statements = append(statements, "UPDATE `"+t+"` SET `"+col+"` = "+db.Lit(to)+" WHERE (`"+col+"` = "+
			db.Lit(from)+")")
	}
	return sqlStep(statements...)
}

// createCurrencyExclusions indexes the codes Money cannot name that are already
// stored, per provider, with the dates they span.
func createCurrencyExclusions(ctx context.Context, q db.Querier) error {
	codes := db.LitList(currency.Codes())
	return exec(ctx, q,
		"CREATE TABLE `currency_exclusions` (`provider_key` varchar(255) NOT NULL, `iso_code` varchar(255) NOT NULL, `start_date` date NOT NULL, `end_date` date NOT NULL, PRIMARY KEY (`provider_key`, `iso_code`))",
		"INSERT INTO `currency_exclusions` (`provider_key`, `iso_code`, `start_date`, `end_date`) "+
			"SELECT `provider`, `iso_code`, min(`date`), max(`date`) FROM ("+
			"SELECT `provider`, `base` AS 'iso_code', `date` FROM `rates` WHERE (`base` NOT IN "+codes+") UNION ALL "+
			"SELECT `provider`, `quote` AS 'iso_code', `date` FROM `rates` WHERE (`quote` NOT IN "+codes+")"+
			") AS 't1' GROUP BY `provider`, `iso_code`",
	)
}

// either is Sequel.|({base: codes}, {quote: codes}).
func either(codes ...string) string {
	list := db.LitList(codes)
	return "(`base` IN " + list + " OR `quote` IN " + list + ")"
}
