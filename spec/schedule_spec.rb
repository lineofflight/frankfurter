# frozen_string_literal: true

require_relative "helper"
require "fugit"

describe "bin/schedule --dry-run" do
  let(:output) do
    %x(APP_ENV=test bundle exec ruby bin/schedule --dry-run 2>&1)
  end

  let(:startup_lines) { output.lines.select { |l| l.start_with?("startup:") } }
  let(:cron_lines) { output.lines.select { |l| l.start_with?("cron:") } }

  it "schedules all enabled providers for startup and cron" do
    _(startup_lines.size).must_equal(Providers.enabled.size)
    _(cron_lines.size).must_equal(Providers.enabled.size)
  end

  it "generates valid cron expressions" do
    cron_lines.each do |line|
      expression = line.match(/cron: (.+) backfill\[/)[1]
      parsed = Fugit::Cron.parse(expression)

      _(parsed).wont_be_nil("Invalid cron: #{expression}")
    end
  end
end
