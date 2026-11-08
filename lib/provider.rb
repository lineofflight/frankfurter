# frozen_string_literal: true

require "blended_rate"
require "bucket"
require "cache"
require "carry_forward"
require "dataset"
require "db"
require "json"
require "log"
require "money/currency"
require "currency_coverage"
require "currency_summary"
require "provider/adapters/adapter"
require "rate"
require "rate_components"
require "rate_precision"
require "rate_rollup"
require "rate_spike"
require "rate_validation"

# An institution that publishes exchange rates in one or more datasets. One adapter fetches all of them.
class Provider < Sequel::Model(:providers)
  plugin :static_cache

  # Reviewed non-currency labels remain available in provider history without triggering unknown-currency alerts.
  NON_CURRENCY_CODES = { "NB" => ["I44", "TWI"], "RBA" => ["FXRTWI"] }.freeze

  PROVIDER_FIELDS = ["key", "name", "country_code", "terms_url"].freeze
  DATASET_FIELDS = [
    "key", "default", "blends", "rate_type", "frequency", "publish_cadence", "publish_schedule", "pivot_currency",
    "data_url", "coverage_start",
  ].freeze

  class << self
    # Provider metadata is config-as-data — JSON files in db/seeds/providers are the source of truth. Re-seeding on
    # every boot syncs changes from the image (new providers, updated schedules) without manual intervention.
    def seed
      dir = File.expand_path("../db/seeds/providers", __dir__)
      files = Dir["#{dir}/*.json"].to_h { |f| [File.basename(f), JSON.parse(File.read(f))] }
      providers, datasets = parse_seeds(files)
      db.transaction do
        Dataset.dataset.delete
        dataset.delete
        dataset.multi_insert(providers)
        Dataset.dataset.multi_insert(datasets)
      end
      load_cache
      Dataset.load_cache
      return unless db.table_exists?(:currency_exclusions)

      recognized = db[:currency_exclusions].select_map(:iso_code).uniq.select { |code| Money::Currency.find(code) }
      return if recognized.empty?

      counterparts = db[:rates].where(Sequel.|({ base: recognized }, { quote: recognized }))
        .select(:base, :quote).distinct.all.flat_map(&:values)
      CurrencySummary.refresh(db, recognized | counterparts)
    end

    private

    # A file describes one provider. Its `datasets` array lists the series it publishes; without one, the file is its
    # own single default dataset. Every file is checked before anything is replaced.
    def parse_seeds(files)
      parsed = files.map do |name, data|
        key = data["key"]
        entries = data.key?("datasets") ? data["datasets"] : [data.slice(*DATASET_FIELDS).merge("default" => true)]
        allowed = PROVIDER_FIELDS + (data.key?("datasets") ? ["datasets"] : DATASET_FIELDS - ["default"])
        check_seed(name, data.keys - allowed, "unknown fields")
        raise ArgumentError, "#{name}: datasets must be a non-empty list" unless entries.is_a?(Array) && entries.any?

        entries.each do |entry|
          check_seed(name, entry.keys - DATASET_FIELDS, "unknown dataset fields")
          next unless data.key?("datasets")
          next if /\A#{Regexp.escape(key.to_s)}\.[A-Z0-9]+\z/.match?(entry["key"].to_s)

          raise ArgumentError, "#{name}: dataset key #{entry["key"].inspect} is not #{key}.<SUFFIX>"
        end
        raise ArgumentError, "#{name}: no default dataset" if entries.none? { |entry| entry["default"] == true }

        [PROVIDER_FIELDS.to_h { |field| [field.to_sym, data[field]] }, entries.map { |entry| dataset_row(key, entry) }]
      end

      providers = parsed.map(&:first)
      datasets = parsed.flat_map(&:last)
      [providers, datasets].each do |rows|
        check_seed("seeds", rows.map { |row| row[:key] }.tally.select { |_, count| count > 1 }.keys, "duplicate keys")
      end
      [providers, datasets]
    end

    def dataset_row(provider_key, entry)
      {
        key: entry["key"],
        provider_key:,
        is_default: entry["default"] == true,
        blends: entry["blends"],
        rate_type: entry["rate_type"],
        frequency: entry["frequency"] || "daily",
        publish_cadence: entry["publish_cadence"],
        publish_schedule: entry["publish_schedule"],
        pivot_currency: entry["pivot_currency"],
        data_url: entry["data_url"],
        coverage_start: entry["coverage_start"],
      }
    end

    def check_seed(name, offending, problem)
      raise ArgumentError, "#{name}: #{problem}: #{offending.join(", ")}" if offending.any?
    end
  end

  def datasets
    Dataset.all.select { |dataset| dataset.provider_key == key }.sort_by(&:key)
  end

  # The datasets the bare provider key selects.
  def default_datasets
    datasets.select(&:is_default)
  end

  # Provider-level metadata comes from the first default dataset.
  def primary_dataset
    default_datasets.first
  end

  [:rate_type, :pivot_currency, :data_url, :publish_cadence, :publish_schedule, :frequency].each do |field|
    define_method(field) { primary_dataset&.public_send(field) }
  end

  def coverage_start
    datasets.filter_map(&:coverage_start).min
  end

  # Coverage and exclusions combine across the default datasets, as the bare key does.
  def currency_coverages
    CurrencyCoverage.where(dataset_key: default_datasets.map(&:key)).all
  end

  # Older migrations load Provider before the exclusions table exists.
  def currency_exclusions
    require "currency_exclusion"
    CurrencyExclusion.where(dataset_key: default_datasets.map(&:key)).all
  end

  def unknown_currencies(exclusions = currency_exclusions)
    codes = exclusions.map(&:iso_code).uniq - NON_CURRENCY_CODES.fetch(key, [])
    codes.reject { |code| Money::Currency.find(code) }.sort
  end

  def adapter
    Adapters.const_get(key)
  end

  def start_date
    (currency_coverages + currency_exclusions).map { |c| c.start_date.to_s }.min
  end

  def end_date
    (currency_coverages + currency_exclusions).map { |c| c.end_date.to_s }.max
  end

  # The oldest across the datasets, so one fetch catches up every series. Nil while any dataset has none, which sends
  # backfill back to coverage_start.
  def last_synced
    dates = datasets.map(&:last_synced)
    dates.min unless dates.empty? || dates.include?(nil)
  end

  # Judged against the first default dataset's schedule, from the provider's latest observation.
  def publishes_missed(reference_date: Date.today)
    primary_dataset&.publishes_missed(reference_date:, last_date: end_date)
  end

  def backfill(after: last_synced || coverage_start)
    if after && after >= Date.today
      Log.info("#{key}: up to date")
      return
    end

    Log.info("#{key}: backfilling from #{after || "start"}")
    # Many adapters read after as exclusive, but coverage_start is the first day to store. Start the day before it, and
    # keep out anything dated earlier: inclusive adapters return that day too, and BCCR pads each window to 100 days.
    floor = coverage_start if after && after == coverage_start
    after -= 1 if floor
    series = datasets.to_h { |dataset| [dataset.key, dataset] }
    fetched = false
    adapter.fetch_each(after:) do |records|
      records.reject! { |r| r[:date] < floor } if floor
      fetched = true
      RateValidation.reject!(records, lead_days: adapter.lead_days)
      records.each do |r|
        r[:dataset_key] = dataset_key_for(r, series)
        r[:rate] = RatePrecision.normalize(r[:rate])
      end
      warn_revisions(records) if adapter.revises?

      inserted = db.transaction(savepoint: true) do
        before = db.get(Sequel.lit("total_changes()"))
        Rate.dataset.insert_conflict(target: [:dataset_key, :date, :base, :quote])
          .multi_insert(records.map { |record| RateComponents.attributes(record) })
        count = db.get(Sequel.lit("total_changes()")) - before
        refresh_derived(records, series) if count.positive?
        count
      end

      Log.info("#{key}: inserted #{inserted} rates")
      next if inserted.zero?

      # Purge stays last: purging before the blend refresh commits would let the edge re-cache stale blends.
      Cache.purge_debounced
      db.run("PRAGMA optimize")
    end
    Log.info("#{key}: fetched no records") unless fetched
  rescue StandardError => e
    Log.error("#{key}: #{e.class}: #{e.message}, skipping")
  end

  private

  # An adapter tags each record with its dataset when the provider publishes several. An untagged record belongs to the
  # provider's only one.
  def dataset_key_for(record, series)
    tag = record.delete(:dataset)
    return tag if tag && series.key?(tag)
    raise ArgumentError, "unknown dataset #{tag.inspect}" if tag
    raise ArgumentError, "untagged record from a provider with several datasets" unless series.size == 1

    series.keys.first
  end

  def refresh_derived(records, series)
    affected_currencies = records.flat_map { |r| [r[:base], r[:quote]] }.uniq
    dates = records.map { |r| r[:date] }.uniq
    keys = records.map { |r| r[:dataset_key] }.uniq
    # Usually the previous observation, judged now that its successor has arrived.
    rescreened = keys.flat_map { |dataset_key| RateSpike.refresh(dataset_key, dates) }.uniq
    refresh_rollups(dates, rescreened)
    refresh_currency_summaries(affected_currencies)
    return unless keys.any? { |dataset_key| series.fetch(dataset_key).blends? }

    # A late arrival at date d joins the carry-forward contributor set of anchors through d + LOOKBACK_DAYS, so those
    # stored blends change too, as do those of a rescreened date. Inside the transaction: the write lock serializes
    # concurrent backfills' refreshes, and a failed refresh rolls back the insert so the next fetch re-ingests and
    # retries.
    changed = dates + rescreened
    BlendedRate.refresh(changed.min..(changed.max + CarryForward::LOOKBACK_DAYS))
  end

  # Insert-only backfill never rewrites a stored row, so a source that revises a published value in place leaves us
  # holding the old one. Report the drift; the fix is the documented delete-and-refetch.
  def warn_revisions(records)
    stored = Rate.where(dataset_key: records.map { |r| r[:dataset_key] }.uniq, date: records.map { |r| r[:date] }.uniq)
      .as_hash([:dataset_key, :date, :base, :quote], :rate)
    drifted = records.filter_map do |r|
      value = stored[[r[:dataset_key], r[:date], r[:base], r[:quote]]]
      [r, value] if value && value != r[:rate]
    end
    return if drifted.empty?

    detail = drifted.first(5).map do |r, value|
      "#{r[:date]} #{r[:base]}/#{r[:quote]} stored #{value} fetched #{r[:rate]}"
    end
    noun = drifted.size == 1 ? "1 stored rate differs" : "#{drifted.size} stored rates differ"
    Log.warn("#{key}: #{noun} from source: #{detail.join(", ")}")
  end

  # Rescreened dates leave a dataset's own averages alone but change the blended buckets that hold them.
  def refresh_rollups(dates, rescreened = [])
    # Older data migrations load Provider before migration 030 creates these tables. Require the grouped models only
    # when backfill actually needs them; normal application startup always migrates before starting ingestion.
    require "blended_weekly_rate"
    require "blended_monthly_rate"

    weeks = []
    months = []
    datasets.group_by(&:label).each_value do |pool|
      keys = pool.map(&:key)
      pool_weeks = refresh_rollup(:weekly_rates, Bucket.week, keys, dates)
      pool_months = refresh_rollup(:monthly_rates, Bucket.month, keys, dates)
      pool.select(&:blends?).each do |dataset|
        weeks |= pool_weeks | buckets(Bucket.week, dataset.key, rescreened)
        months |= pool_months | buckets(Bucket.month, dataset.key, rescreened)
      end
    end
    return unless datasets.any?(&:blends?)

    BlendedWeeklyRate.refresh(weeks)
    BlendedMonthlyRate.refresh(months)
  end

  def buckets(bucket_expr, dataset_key, dates)
    return [] if dates.empty?

    db[:rates].where(dataset_key:, date: dates).select_map(bucket_expr).uniq
  end

  def refresh_currency_summaries(iso_codes)
    datasets.each { |dataset| CurrencySummary.refresh(db, iso_codes, dataset: dataset.key) }
  end

  # Replaces the pool's rollups in every bucket holding one of the dates.
  def refresh_rollup(table, bucket_expr, dataset_keys, dates)
    buckets = db[:rates]
      .where(dataset_key: dataset_keys, date: dates)
      .select_map(bucket_expr)
      .uniq

    return [] if buckets.empty?

    db[table].where(dataset_key: dataset_keys, bucket_date: buckets).delete
    rows = db[:rates].where(dataset_key: dataset_keys).where(bucket_expr => buckets)
    db[table].insert(RateRollup::COLUMNS, RateRollup.source(rows, bucket_expr))
    buckets
  end
end
