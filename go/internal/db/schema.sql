-- Dumped from the migrated Ruby test database with `sqlite3 db/frankfurter_test.sqlite3 .schema`. Regenerate after a
-- Ruby migration instead of editing by hand, and bump the schema_info version at the end to match.
CREATE TABLE `schema_info` (`version` integer DEFAULT (0) NOT NULL);
CREATE TABLE `providers` (`key` varchar(255) NOT NULL PRIMARY KEY, `name` varchar(255) NOT NULL, "data_url" varchar(255), `terms_url` varchar(255), `coverage_start` date, `pivot_currency` varchar(255), `rate_type` varchar(255), `country_code` varchar(2), `publish_schedule` varchar(255), `publish_cadence` varchar(255), `frequency` varchar(255) DEFAULT ('daily') NOT NULL);
CREATE TABLE `weekly_rates` (`bucket_date` date NOT NULL, `provider` varchar(255) NOT NULL, `base` varchar(255) NOT NULL, `quote` varchar(255) NOT NULL, `rate` double precision NOT NULL, PRIMARY KEY (`provider`, `bucket_date`, `base`, `quote`));
CREATE INDEX `weekly_rates_bucket_date_quote_index` ON `weekly_rates` (`bucket_date`, `quote`);
CREATE TABLE `monthly_rates` (`bucket_date` date NOT NULL, `provider` varchar(255) NOT NULL, `base` varchar(255) NOT NULL, `quote` varchar(255) NOT NULL, `rate` double precision NOT NULL, PRIMARY KEY (`provider`, `bucket_date`, `base`, `quote`));
CREATE INDEX `monthly_rates_bucket_date_quote_index` ON `monthly_rates` (`bucket_date`, `quote`);
CREATE TABLE `currencies` (`iso_code` varchar(255) NOT NULL PRIMARY KEY, `start_date` date NOT NULL, `end_date` date NOT NULL);
CREATE TABLE `currency_coverages` (`provider_key` varchar(255) NOT NULL, `iso_code` varchar(255) NOT NULL, `start_date` date, `end_date` date, PRIMARY KEY (`provider_key`, `iso_code`));
CREATE TABLE `blended_rates` (`date` date NOT NULL, `quote` varchar(255) NOT NULL, `rate` double precision NOT NULL, PRIMARY KEY (`quote`, `date`));
CREATE INDEX `blended_rates_date_index` ON `blended_rates` (`date`);
CREATE TABLE `blended_weekly_rates` (`bucket_date` date NOT NULL, `quote` varchar(255) NOT NULL, `rate` double precision NOT NULL, PRIMARY KEY (`quote`, `bucket_date`));
CREATE INDEX `blended_weekly_rates_bucket_date_index` ON `blended_weekly_rates` (`bucket_date`);
CREATE TABLE `blended_monthly_rates` (`bucket_date` date NOT NULL, `quote` varchar(255) NOT NULL, `rate` double precision NOT NULL, PRIMARY KEY (`quote`, `bucket_date`));
CREATE INDEX `blended_monthly_rates_bucket_date_index` ON `blended_monthly_rates` (`bucket_date`);
CREATE TABLE `rates`(`date` date DEFAULT (NULL) NOT NULL, `base` varchar(255) DEFAULT (NULL) NOT NULL, `quote` varchar(255) DEFAULT (NULL) NOT NULL, `mid` double precision DEFAULT (NULL) NULL, `provider` varchar(255) DEFAULT (NULL) NOT NULL, `bid` double precision, `ask` double precision, rate REAL GENERATED ALWAYS AS (
  COALESCE(mid,
    CASE
      WHEN provider = 'BOJA' AND bid = 0 THEN ask
      WHEN bid IS NOT NULL AND ask IS NOT NULL
        THEN CAST(printf('%.12g', (bid + ask) / 2.0) AS REAL)
    END
  )
) VIRTUAL);
CREATE INDEX `rates_date_index` ON `rates` (`date`);
CREATE INDEX `rates_provider_base_index` ON `rates` (`provider`, `base`);
CREATE INDEX `rates_provider_quote_index` ON `rates` (`provider`, `quote`);
CREATE UNIQUE INDEX `rates_provider_date_base_quote_index` ON `rates` (`provider`, `date`, `base`, `quote`);
CREATE TABLE `currency_exclusions` (`provider_key` varchar(255) NOT NULL, `iso_code` varchar(255) NOT NULL, `start_date` date NOT NULL, `end_date` date NOT NULL, PRIMARY KEY (`provider_key`, `iso_code`));
CREATE INDEX `weekly_rates_bucket_date_provider_base_quote_index` ON `weekly_rates` (`bucket_date`, `provider`, `base`, `quote`);
CREATE INDEX `monthly_rates_bucket_date_provider_base_quote_index` ON `monthly_rates` (`bucket_date`, `provider`, `base`, `quote`);
CREATE TABLE `rate_spikes` (`provider` varchar(255) NOT NULL, `date` date NOT NULL, `base` varchar(255) NOT NULL, `quote` varchar(255) NOT NULL, PRIMARY KEY (`provider`, `base`, `quote`, `date`));
INSERT INTO schema_info (version) VALUES (45);
