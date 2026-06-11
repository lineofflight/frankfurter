# frozen_string_literal: true

require_relative "helper"
require "fugit"
require "provider/adapters"
require "tmpdir"

describe "bin/schedule --dry-run" do
  let(:output) do
    %x(APP_ENV=test bundle exec ruby bin/schedule --dry-run 2>&1)
  end

  let(:startup_lines) { output.lines.select { |l| l.start_with?("startup:") } }
  let(:cron_lines) { output.lines.select { |l| l.start_with?("cron:") } }

  let(:enabled_count) { Provider.all.count { |p| Provider::Adapters.const_defined?(p.key) } }
  let(:scheduled_count) do
    Provider.all.count { |p| Provider::Adapters.const_defined?(p.key) && p.publish_schedule }
  end

  it "schedules all enabled providers for startup and only configured providers for cron" do
    _(startup_lines.size).must_equal(enabled_count)
    _(cron_lines.size).must_equal(scheduled_count)
  end

  it "generates valid cron expressions" do
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
            def join = nil
          end
        end
      RUBY

      File.write(File.join(dir, "rufus-scheduler.rb"), scheduler_stub)

      output = %x(APP_ENV=test bundle exec ruby -I #{dir} bin/schedule 2>&1)

      _($CHILD_STATUS.success?).must_equal(true, output)
      _(output.lines.map(&:chomp).grep(/\Astartup: \d+s\z/)).wont_be_empty
    end
  end
end
