# frozen_string_literal: true

require_relative "helper"
require "open3"
require "tmpdir"

describe "Rate spikes migration" do
  def run_migration_script(script)
    setup = <<~RUBY
      require "db"
      Sequel.extension(:migration)
      require "provider"
      require "blended_weekly_rate"
      require "blended_monthly_rate"
      Provider.seed
    RUBY
    Dir.mktmpdir do |dir|
      env = { "DATABASE_URL" => "sqlite://#{File.join(dir, "migration.sqlite3")}", "APP_ENV" => "test" }
      initialize = 'require "db"; Sequel.extension(:migration); Sequel::Migrator.run(DB, "db/migrate", target: 44)'
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", initialize)

      _(status.success?).must_equal(true, output)
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", setup + script)

      _(status.success?).must_equal(true, output)
    end
  end

  it "flags stored one-day typos and leaves rates and blends to a rebuild" do
    run_migration_script(<<~'RUBY')
      rows = { "2024-01-18" => 3.26, "2024-01-19" => 3.26, "2024-01-22" => 43.26, "2024-01-23" => 3.26 }
        .flat_map do |date, mid|
          [
            { provider: "CBG", date:, base: "SLE", quote: "GMD", mid: },
            { provider: "CBG", date:, base: "USD", quote: "GMD", mid: 70.0 },
          ]
        end
      # Lebanon's 2023 devaluation persists, so it stays in the blend.
      rows += { "2023-01-31" => 1507.5, "2023-02-01" => 15000.0, "2023-02-02" => 15000.0 }.map do |date, mid|
        { provider: "BDL", date:, base: "USD", quote: "LBP", mid: }
      end
      DB[:rates].multi_insert(rows)
      [:weekly_rates, :monthly_rates].zip([Bucket.week, Bucket.month]).each do |table, bucket|
        DB[table].insert(
          [:bucket_date, :provider, :base, :quote, :rate],
          DB[:rates].select(bucket, :provider, :base, :quote, Sequel.function(:avg, :rate))
            .group(bucket, :provider, :base, :quote),
        )
      end
      blends = [BlendedRate, BlendedWeeklyRate, BlendedMonthlyRate]
      blends.each(&:rebuild)
      typo = -> { DB[:blended_rates].where(quote: "SLE", date: "2024-01-22").get(:rate) }
      abort "fixture blend missing the typo" unless typo.call && (typo.call - (70.0 / 43.26)).abs < 1e-9
      snapshot = -> { blends.to_h { |model| [model, model.dataset.order(*model.primary_key).all] } }
      before = snapshot.call
      stored = DB[:rates].order(:provider, :date, :base, :quote).all

      require "rake"
      load "lib/tasks/db.rake"
      Rake::Task["db:setup"].invoke
      abort "migration incomplete" unless DB[:schema_info].get(:version) >= 45

      spikes = DB[:rate_spikes].select_map([:provider, :date, :base, :quote]).map { |row| row.map(&:to_s) }
      abort "wrong spikes: #{spikes.inspect}" unless spikes == [["CBG", "2024-01-22", "SLE", "GMD"]]
      abort "changed rates" unless DB[:rates].order(:provider, :date, :base, :quote).all == stored
      abort "touched the blends" unless snapshot.call == before

      blends.each(&:rebuild)
      abort "typo still blended" if typo.call
      leone = DB[:blended_rates].where(quote: "SLE").select_map(:rate)
      abort "SLE blend #{leone.inspect}" unless leone.all? { |rate| (rate - (70.0 / 3.26)).abs < 1e-9 }
      lbp = DB[:blended_rates].where(quote: "LBP", date: "2023-02-02").get(:rate)
      abort "LBP blend #{lbp.inspect}" unless lbp == 15000.0
      DB.disconnect
    RUBY
  end
end
