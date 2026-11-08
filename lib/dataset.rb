# frozen_string_literal: true

require "db"
require "fugit"
require "rate"

# A named rate series an institution publishes, with its own frequency, schedule and metadata. Rates, rollups, spikes,
# coverage and exclusions belong to a dataset, keyed like its provider when the provider publishes one series (`ECB`)
# and suffixed when it publishes several (`NBP.A`, `NBP.B`).
#
# A provider's default datasets are what its bare key selects. Rows from a default dataset report the provider key and
# rows from any other dataset report the dataset key, so a reported key always selects the row it labels.
class Dataset < Sequel::Model(:datasets)
  plugin :static_cache

  many_to_one :provider, key: :provider_key

  # Carry-forward window per observation frequency and publish cadence (#646). A daily value goes stale in two weeks; a
  # monthly or quarterly value stands for its whole period plus the lag before the next one lands.
  LOOKBACK_DAYS = { "daily" => 14, "weekly" => 14, "monthly" => 45, "quarterly" => 120 }.freeze

  class << self
    # Keys of datasets whose values stand for longer than a day. Their rows never enter the blend or the currency
    # catalogue: a value that stands for a month or a quarter is observed once and served for the whole period, so on
    # most days it is weeks stale, and the recency decay, which counts from the row date, cannot see that.
    def non_blending_keys
      all.reject(&:blends?).map(&:key)
    end

    # Dataset keys for the provider or dataset keys a caller names. A provider key selects its default datasets. Unknown
    # keys pass through, so they select nothing, as they always have.
    def resolve(keys)
      require "provider"

      keys.flat_map do |key|
        provider = Provider[key]
        provider ? provider.default_datasets.map(&:key) : [key]
      end.uniq
    end

    def label(key)
      self[key]&.label || key
    end

    # Keys whose rows roll up together with the given dataset's (RateRollup): a provider's default datasets pool, and
    # any other dataset, or an unknown key, stands alone.
    def pool(key)
      keys = all.select { |dataset| dataset.label == label(key) }.map(&:key)
      keys.empty? ? [key] : keys
    end

    # SQL for Dataset#label over a dataset_key column.
    def label_expression(column = :dataset_key)
      relabelled = all.reject { |dataset| dataset.label == dataset.key }.to_h { |dataset| [dataset.key, dataset.label] }
      return Sequel[column] if relabelled.empty?

      Sequel.case(relabelled, Sequel[column], Sequel[column])
    end
  end

  def label
    is_default ? provider_key : key
  end

  def frequency
    super || "daily"
  end

  # Whether the dataset's rows enter the blend. Seeds may say so; otherwise only daily observations blend.
  def blends?
    blends.nil? ? frequency == "daily" : blends
  end

  # The wider of the frequency and cadence windows. A provider that releases a month of daily fixings in arrears has
  # nothing newer than the last batch for weeks at a time, and the daily window alone would leave its latest query empty
  # for most of every month.
  def lookback_days
    [frequency, publish_cadence].compact.map { |period| LOOKBACK_DAYS.fetch(period) }.max
  end

  def end_date
    require "currency_coverage"
    require "currency_exclusion"

    [CurrencyCoverage, CurrencyExclusion].filter_map { |model| model.where(dataset_key: key).max(:end_date) }.max&.to_s
  end

  def last_synced
    # There seems to be no Sequel-level way to make the aggregate auto-cast.
    date = Rate.where(dataset_key: key).max(:date)
    Date.parse(date) if date
  end

  # Scheduled publishes missed since the last observation, in the unit of the publish cadence.
  def publishes_missed(reference_date: Date.today, last_date: end_date)
    return unless publish_schedule
    return unless last_date

    cron = Fugit::Cron.parse(publish_schedule)
    last_date = Date.parse(last_date.to_s)

    case publish_cadence
    when "daily"
      count_fire_days(cron, last_date, reference_date)
    when "weekly"
      count_missed_buckets(cron, last_date, reference_date, :week)
    when "monthly"
      count_missed_buckets(cron, last_date, reference_date, :month)
    when "quarterly"
      count_missed_buckets(cron, last_date, reference_date, :quarter)
    else
      raise ArgumentError, "#{key}: unknown publish_cadence #{publish_cadence.inspect}"
    end
  end

  private

  def count_fire_days(cron, last_date, reference_date)
    count = 0
    ((last_date + 1)..(reference_date - 1)).each do |date|
      count += 1 if cron_fires_on?(cron, date)
    end
    count
  end

  def cron_fires_on?(cron, date)
    start = Time.utc(date.year, date.month, date.day) - 1
    eod = Time.utc(date.year, date.month, date.day, 23, 59, 59).to_i
    cron.next_time(start).to_i <= eod
  end

  def count_missed_buckets(cron, last_date, reference_date, granularity)
    today_utc = Time.utc(reference_date.year, reference_date.month, reference_date.day, 23, 59, 59)
    prev_fire = cron.previous_time(today_utc)
    return 0 unless prev_fire

    prev_fire_date = Date.new(prev_fire.year, prev_fire.month, prev_fire.day)
    expected_data_end = bucket_start(prev_fire_date, granularity) - 1
    expected_bucket = bucket_start(expected_data_end, granularity)
    our_bucket = bucket_start(last_date, granularity)
    return 0 if our_bucket >= expected_bucket

    count_buckets_between(our_bucket, expected_bucket, granularity)
  end

  def bucket_start(date, granularity)
    case granularity
    when :week  then date - (date.cwday - 1)
    when :month then Date.new(date.year, date.month, 1)
    when :quarter then Date.new(date.year, (((date.month - 1) / 3) * 3) + 1, 1)
    end
  end

  def count_buckets_between(from_bucket, to_bucket, granularity)
    count = 0
    cursor = from_bucket
    while cursor < to_bucket
      cursor = case granularity
               when :week  then cursor + 7
               when :month then cursor.next_month
               when :quarter then cursor >> 3
               end
      count += 1
    end
    count
  end
end
