# frozen_string_literal: true

require_relative "helper"
require "versions/v2/rate_query"
require "blended_weekly_rate"
require "blended_monthly_rate"

describe "Materialized grouped blends" do
  it "stores weekly and monthly blends separately from provider rollups" do
    _([:blended_weekly_rates, :blended_monthly_rates] - DB.tables).must_be_empty
  end
end

[[:BlendedWeeklyRate, WeeklyRate], [:BlendedMonthlyRate, MonthlyRate]].each do |name, source|
  describe name.to_s do
    before do
      _(Object.const_defined?(name)).must_equal(true, "#{name} must materialize provider buckets")
      @model = Object.const_get(name)
      source.dataset.delete
      @model.dataset.delete
      @date = Date.new(2024, 1, 1)
      source.dataset.multi_insert([
        { bucket_date: @date, provider: "ECB", base: "EUR", quote: "USD", rate: 2.0 },
        { bucket_date: @date, provider: "ECB", base: "EUR", quote: "CHF", rate: 1.0 },
        { bucket_date: @date, provider: "BOC", base: "USD", quote: "EUR", rate: 0.5 },
      ])
    end

    it "stores the full USD blend without request rounding" do
      @model.rebuild

      _(@model.where(bucket_date: @date, quote: "EUR").get(:rate)).must_equal(0.5)
      _(@model.where(bucket_date: @date, quote: "CHF").get(:rate)).must_equal(0.5)
      _(@model.ready?).must_equal(true)
    end

    it "refreshes changed buckets and leaves other stored values untouched" do
      other = @date + 40
      source.dataset.insert(bucket_date: other, provider: "ECB", base: "USD", quote: "EUR", rate: 0.75)
      @model.rebuild
      source.dataset.where(bucket_date: @date, quote: "CHF").update(rate: 1.5)
      @model.refresh([@date])

      _(@model.where(bucket_date: @date, quote: "CHF").get(:rate)).must_equal(0.75)
      _(@model.where(bucket_date: other, quote: "EUR").get(:rate)).must_equal(0.75)
    end

    it "removes every stored bucket whose source disappeared" do
      other = @date + 40
      source.dataset.insert(bucket_date: other, provider: "ECB", base: "USD", quote: "EUR", rate: 0.75)
      @model.rebuild
      source.dataset.where(bucket_date: @date).delete
      @model.rebuild

      _(@model.where(bucket_date: @date).count).must_equal(0)
      _(@model.where(bucket_date: other).count).must_be(:>, 0)
      source.dataset.delete
      @model.rebuild

      _(@model.dataset.count).must_equal(0)
    end

    it "falls back when any requested source bucket has not been materialized" do
      other = @date + 40
      source.dataset.insert(bucket_date: other, provider: "ECB", base: "USD", quote: "EUR", rate: 0.75)
      @model.refresh([@date])

      _(@model.ready?).must_equal(false)
      _(@model.read(@date..other)).must_be_nil
      _(@model.read(@date..@date)).wont_be_empty
    end

    it "does not snap back over a bucket without a USD bridge" do
      @model.rebuild
      empty = @date + 40
      source.dataset.insert(bucket_date: empty, provider: "ECB", base: "EUR", quote: "JPY", rate: 150.0)
      @model.refresh([empty])

      _(@model.read(empty..empty)).must_be_nil
    end

    it "populates missing buckets without rebuilding existing history" do
      _(@model.respond_to?(:populate)).must_equal(true)
      @model.rebuild
      before = @model.where(bucket_date: @date, quote: "EUR").get(:rate)
      other = @date + 40
      source.dataset.insert(bucket_date: other, provider: "ECB", base: "USD", quote: "EUR", rate: 0.75)
      calls = []
      original = @model.method(:refresh)
      @model.stub(:refresh, lambda { |dates|
        calls.concat(dates)
        original.call(dates)
      },) { @model.populate }

      _(calls).must_equal([other])
      _(@model.where(bucket_date: @date, quote: "EUR").get(:rate)).must_equal(before)
      _(@model.where(bucket_date: other, quote: "EUR").get(:rate)).must_equal(0.75)
      _(@model.populate).must_equal(0)
    end

    it "does not report a changed cache when missing buckets have no computable blend" do
      _(@model.respond_to?(:populate)).must_equal(true)
      @model.rebuild
      source.dataset.insert(bucket_date: @date + 40, provider: "ECB", base: "EUR", quote: "JPY", rate: 150.0)

      _(@model.populate).must_equal(0)
    end

    it "rolls back replacement if the blend fails" do
      @model.rebuild
      before = @model.dataset.order(:bucket_date, :quote).all.map(&:values)
      source.dataset.where(quote: "CHF").update(rate: 9.0)

      _ do
        Blender.stub(:new, ->(*) { raise "failed blend" }) { @model.refresh([@date]) }
      end.must_raise(RuntimeError)
      _(@model.dataset.order(:bucket_date, :quote).all.map(&:values)).must_equal(before)
    end

    it "restores stored rows when insertion fails after deleting the old bucket" do
      @model.rebuild
      before = @model.dataset.order(:bucket_date, :quote).all.map(&:values)
      source.dataset.where(quote: "CHF").update(rate: 9.0)
      deleted = false
      model = @model
      date = @date
      failing = @model.dataset.with_extend(Module.new do
        define_method(:multi_insert) do |*|
          deleted = model.where(bucket_date: date).empty?
          raise "failed insert"
        end
      end)

      @model.stub(:dataset, -> { failing }) do
        _ { @model.refresh([@date]) }.must_raise(RuntimeError)
      end

      _(deleted).must_equal(true)
      _(@model.dataset.order(:bucket_date, :quote).all.map(&:values)).must_equal(before)
    end

    it "bounds source reads even when a caller refreshes decades at once" do
      source.dataset.delete
      dates = Array.new(205) { |i| @date + (i * 7) }
      dates.each do |date|
        source.dataset.insert(bucket_date: date, provider: "ECB", base: "USD", quote: "EUR", rate: 0.8)
      end
      sizes = []
      logger = Object.new
      table = source.table_name.to_s
      logger.define_singleton_method(:info) do |sql|
        return unless sql.include?("SELECT * FROM `#{table}`") && sql.include?("`bucket_date` IN")

        sizes << sql.scan(/'\d{4}-\d{2}-\d{2}'/).size
      end
      DB.loggers << logger
      begin
        @model.refresh(dates)
      ensure
        DB.loggers.delete(logger)
      end

      _(sizes).wont_be_empty
      _(sizes.max).must_be(:<=, BlendedRollup::BATCH_BUCKETS)
      _(@model.where(quote: "EUR").count).must_equal(dates.size)
    end
  end
end

describe "Grouped blend ingestion" do
  before do
    BlendedWeeklyRate.rebuild
    BlendedMonthlyRate.rebuild
    @date = Fixtures.business_day(60)
    @provider = Provider["BCB"].dup
    date = @date
    @adapter = Class.new(Provider::Adapters::Adapter) do
      define_method(:fetch) do |**|
        [{ date:, base: "EUR", quote: "USD", rate: 1.3, mid: nil, bid: 1.2, ask: 1.4 }]
      end
    end
  end

  it "refreshes both affected buckets before purging and leaves other buckets alone" do
    models = [BlendedWeeklyRate, BlendedMonthlyRate]
    buckets = [DB.get(Bucket.week(@date.to_s)), DB.get(Bucket.month(@date.to_s))]
    before = models.zip(buckets).map do |model, bucket|
      model.dataset.exclude(bucket_date: bucket).order(:bucket_date, :quote).all.map(&:values)
    end
    purged = false
    Cache.stub(:purge_debounced, lambda {
      models.zip(buckets).each do |model, bucket|
        stored = model.where(bucket_date: bucket, quote: "EUR").get(:rate)
        # The new provider changes the EUR blend in the bucket; compare to a fresh full-source rebuild.
        model.refresh([bucket])

        _(stored).must_equal(model.where(bucket_date: bucket, quote: "EUR").get(:rate))
      end
      purged = true
    },) do
      @provider.stub(:adapter, @adapter) { @provider.backfill(after: @date - 1) }
    end

    _(purged).must_equal(true)
    stored = Rate.where(provider: @provider.key, date: @date, base: "EUR", quote: "USD")

    _(stored.get([:mid, :bid, :ask, :rate])).must_equal([nil, 1.2, 1.4, 1.3])
    [WeeklyRate, MonthlyRate].zip(buckets).each do |model, bucket|
      _(model.where(provider: @provider.key, bucket_date: bucket, base: "EUR", quote: "USD").get(:rate))
        .must_equal(1.3)
    end
    models.zip(buckets, before).each do |model, bucket, prior|
      _(model.dataset.exclude(bucket_date: bucket).order(:bucket_date, :quote).all.map(&:values)).must_equal(prior)
    end
  end

  it "rolls back raw and provider rollup changes when a grouped refresh fails" do
    weekly = WeeklyRate.dataset.count
    monthly = MonthlyRate.dataset.count
    models = [BlendedWeeklyRate, BlendedMonthlyRate]
    before = models.map { |model| model.dataset.order(:bucket_date, :quote).all.map(&:values) }
    purged = false
    Cache.stub(:purge_debounced, -> { purged = true }) do
      BlendedMonthlyRate.stub(:refresh, ->(*) { raise "failed grouped refresh" }) do
        # Backfill logs and absorbs provider errors, but its transaction must still roll back.
        Log.stub(:error, nil) do
          @provider.stub(:adapter, @adapter) { @provider.backfill(after: @date - 1) }
        end
      end
    end

    _(Rate.where(provider: "BCB", date: @date).count).must_equal(0)
    _(WeeklyRate.dataset.count).must_equal(weekly)
    _(MonthlyRate.dataset.count).must_equal(monthly)
    models.zip(before).each do |model, prior|
      _(model.dataset.order(:bucket_date, :quote).all.map(&:values)).must_equal(prior)
    end
    _(purged).must_equal(false)
  end

  it "updates non-blending provider rollups without recomputing blended tables" do
    provider = Provider["UST"].dup
    called = false
    refresh = ->(*) { called = true }
    BlendedWeeklyRate.stub(:refresh, refresh) do
      BlendedMonthlyRate.stub(:refresh, refresh) do
        Cache.stub(:purge_debounced, nil) do
          provider.stub(:adapter, @adapter) { provider.backfill(after: @date - 1) }
        end
      end
    end

    _(Rate.where(provider: "UST", date: @date).count).must_equal(1)
    _(WeeklyRate.where(provider: "UST").count).must_be(:>, 0)
    _(MonthlyRate.where(provider: "UST").count).must_be(:>, 0)
    _(called).must_equal(false)
  end

  it "rolls back earlier grouped batches and source inserts when a later batch fails" do
    models = [BlendedWeeklyRate, BlendedMonthlyRate]
    before = models.map { |model| model.dataset.order(:bucket_date, :quote).all.map(&:values) }
    source_counts = [WeeklyRate.count, MonthlyRate.count]
    records = Array.new(101) do |i|
      { date: Fixtures.latest_date - (i * 7), base: "EUR", quote: "USD", rate: 1.3 }
    end
    adapter = Class.new(Provider::Adapters::Adapter) do
      define_method(:fetch) { |**| records }
    end
    batches = 0
    refresh = BlendedWeeklyRate.method(:refresh_batch)
    purged = false
    Cache.stub(:purge_debounced, -> { purged = true }) do
      BlendedWeeklyRate.stub(:refresh_batch, lambda { |dates|
        batches += 1
        raise "failed second batch" if batches == 2

        refresh.call(dates)
      },) do
        Log.stub(:error, nil) do
          @provider.stub(:adapter, adapter) { @provider.backfill(after: records.last[:date] - 1) }
        end
      end
    end

    _(batches).must_equal(2)
    _(Rate.where(provider: "BCB").count).must_equal(0)
    _([WeeklyRate.count, MonthlyRate.count]).must_equal(source_counts)
    models.zip(before).each do |model, prior|
      _(model.dataset.order(:bucket_date, :quote).all.map(&:values)).must_equal(prior)
    end
    _(purged).must_equal(false)
  end
end

describe "Grouped read consistency" do
  it "reads source coverage and stored values from the same SQLite snapshot" do
    require "tmpdir"
    Dir.mktmpdir do |dir|
      database = Sequel.sqlite(File.join(dir, "snapshot.sqlite3"))
      writer = nil
      begin
        database.run("PRAGMA journal_mode=WAL")
        database.create_table(:weekly_rates) do
          Date :bucket_date
          String :provider
          String :base
          String :quote
          Float :rate
        end
        database.create_table(:blended_weekly_rates) do
          Date :bucket_date
          String :quote
          Float :rate
        end
        source = Class.new(Sequel::Model(database[:weekly_rates])) do
          include RateScopes

          def self.date_column = :bucket_date
        end
        model = Class.new(Sequel::Model(database[:blended_weekly_rates])) do
          include BlendedRollup
        end
        model.define_singleton_method(:source) { source }
        date = Date.new(2024, 1, 1)
        database[:weekly_rates].insert(bucket_date: date, provider: "ECB", base: "USD", quote: "EUR", rate: 0.8)
        model.refresh([date])
        writer = Sequel.sqlite(File.join(dir, "snapshot.sqlite3"))
        changed = false
        logger = Object.new
        logger.define_singleton_method(:info) do |message|
          next if changed || !message.include?("FROM `weekly_rates`")

          changed = true
          writer.transaction do
            writer[:weekly_rates].update(rate: 0.9)
            writer[:blended_weekly_rates].where(quote: "EUR").update(rate: 0.9)
          end
        end
        database.loggers << logger

        records = model.read(date..date)

        _(changed).must_equal(true)
        _(records.find { |row| row[:quote] == "EUR" }[:rate]).must_equal(0.8)
        _(writer[:blended_weekly_rates].where(quote: "EUR").get(:rate)).must_equal(0.9)
      ensure
        database.disconnect
        writer&.disconnect
      end
    end
  end
end
