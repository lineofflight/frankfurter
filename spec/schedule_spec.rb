# frozen_string_literal: true

require_relative "helper"
require "fugit"
require "provider/adapters"

describe "bin/schedule --dry-run" do
  let(:output) do
    %x(APP_ENV=test bundle exec ruby bin/schedule --dry-run 2>&1)
  end

  let(:startup_lines) { output.lines.select { |l| l.start_with?("startup:") } }
  let(:cron_lines) { output.lines.select { |l| l.start_with?("cron:") } }

  let(:enabled_count) { Provider.all.count { |p| Provider::Adapters.const_defined?(p.key) } }
  let(:scheduled_count) do
    Provider.all.count { |p| Provider::Adapters.const_defined?(p.key) && p.publish_time && p.publish_days }
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
end
