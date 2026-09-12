# frozen_string_literal: true

require_relative "helper"
require "fugit"
require "provider/adapters"
require "tmpdir"

describe "bin/schedule --dry-run" do
  let(:db_url) { DB.opts[:uri] || ENV["DATABASE_URL"] || "sqlite://#{Dir.pwd}/db/frankfurter_test.sqlite3" }
  let(:output) do
    `DATABASE_URL=#{db_url} APP_ENV=test bundle exec ruby bin/schedule --dry-run 2>&1`
  end

  let(:startup_lines) { output.lines.select { |l| l.start_with?("startup:") } }
  let(:cron_lines) { output.lines.select { |l| l.start_with?("cron:") } }

  let(:enabled_count) { Provider.all.count { |p| Provider::Adapters.const_defined?(p.key) } }
  let(:scheduled_count) do
    Provider.all.count { |p| Provider::Adapters.const_defined?(p.key) && p.publish_schedule }
  end

  it "schedules enabled providers at startup and configured providers with valid cron expressions" do
    _(startup_lines.size).must_equal(enabled_count)
    _(cron_lines.size).must_equal(scheduled_count)

    cron_lines.each do |line|
      expression = line.match(/cron: (.+) backfill\[/)[1]
      parsed = Fugit::Cron.parse(expression)

      _(parsed).wont_be_nil("Invalid cron: #{expression}")
    end
  end

  it "schedules startup backfills with a numeric stagger" do
    Dir.mktmpdir do |dir|
      scheduler_stub = <<~RUBY
        module Rufus
          class Scheduler
            def initialize(max_work_threads:); end
            def in(delay); puts "startup: \#{delay}"; end
            def cron(*) = nil
            def every(*) = nil
            def join = nil
          end
        end
      RUBY

      File.write(File.join(dir, "rufus-scheduler.rb"), scheduler_stub)

      output = `DATABASE_URL=#{db_url} APP_ENV=test bundle exec ruby -I #{dir} bin/schedule 2>&1`

      _($CHILD_STATUS.success?).must_equal(true, output)
      _(output.lines.map(&:chomp).grep(/\Astartup: \d+s\z/)).wont_be_empty
    end
  end
end

describe "Grouped blend startup" do
  before do
    require "rufus-scheduler"
    require "blended_weekly_rate"
    require "blended_monthly_rate"
    BlendedRate.rebuild
    @timers = []
    timers = @timers
    scheduler = Object.new
    [:in, :every].each do |method|
      scheduler.define_singleton_method(method) do |delay, **options, &block|
        timers << { method:, delay:, options:, block: }
      end
    end
    [:cron, :join].each { |method| scheduler.define_singleton_method(method) { |*| nil } }

    Rufus::Scheduler.stub(:new, scheduler) { load File.expand_path("../bin/schedule", __dir__) }
    @job = Struct.new(:cancelled) do
      def unschedule = self.cancelled = true
    end.new(false)
  end

  def population_timer
    timer = @timers.find { |entry| entry[:method] == :every && entry[:options][:first_in] == "30s" }

    _(timer).wont_be_nil("population must retry independently of provider startup timers")
    _(timer[:options][:overlap]).must_equal(false)
    timer[:block]
  end

  it "builds grouped tables even when the daily blend is already ready" do
    Cache.stub(:purge_debounced, nil) { population_timer.call(@job) }

    _(BlendedWeeklyRate.ready?).must_equal(true)
    _(BlendedMonthlyRate.ready?).must_equal(true)
    _(@job.cancelled).must_equal(true)
  end

  it "retries a failed population and stops only after both grouped builds finish" do
    timer = population_timer
    Cache.stub(:purge_debounced, nil) do
      BlendedMonthlyRate.stub(:populate, -> { raise Sequel::DatabaseError, "database is busy" }) do
        _ { timer.call(@job) }.must_raise(Sequel::DatabaseError)
      end
      _(@job.cancelled).must_equal(false)
      _(BlendedWeeklyRate.ready?).must_equal(true)
      _(BlendedMonthlyRate.dataset.empty?).must_equal(true)

      timer.call(@job)
    end

    _(@job.cancelled).must_equal(true)
    _(BlendedMonthlyRate.ready?).must_equal(true)
  end
end
