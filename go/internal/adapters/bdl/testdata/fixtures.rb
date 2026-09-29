# frozen_string_literal: true

# Writes the .xls fixtures for bdl_test.go with the spreadsheet gem, exactly as bdl_spec.rb's build_xls builds them.
# Go has no .xls writer, so the workbooks are generated once and committed. From the repository root:
#
#   APP_ENV=test mise exec -- bundle exec ruby go/internal/adapters/bdl/testdata/fixtures.rb

require "date"
require "fileutils"
require "spreadsheet"
require "stringio"

def build_xls(sheets)
  sheets = { "Daily Exchange Rates 2024" => sheets } if sheets.is_a?(Array)
  book = Spreadsheet::Workbook.new
  sheets.each do |name, rows|
    sheet = book.create_worksheet(name:)
    sheet.row(0).replace(["Period", "Currency", "Bid", "Ask", "Mid"])
    rows.each_with_index { |values, index| sheet.row(index + 1).replace(values) }
  end
  io = StringIO.new
  book.write(io)
  io.string
end

FIXTURES = {
  "sheets" => {
    "Daily Exchange Rates  2025" => [[Date.new(2025, 1, 2), "USD", 89_500.0, 89_500.0, 89_500.0]],
    "Daily Exchange Rates 2024" => [
      [Date.new(2024, 12, 30), "USD", 89_500.0, 89_500.0, 89_500.0],
      [Date.new(2024, 1, 2), "USD", 14_935.0, 15_065.0, 15_000.0],
    ],
  },
  "mid" => [[Date.new(2024, 1, 2), "USD", 14_935.0, 15_065.0, 15_000.0]],
  "missing_mid" => [
    [Date.new(2024, 1, 2), "USD", 89_500.0, 89_500.0, nil],
    [Date.new(2024, 1, 2), "EUR", 0.0, 0.0, 0.0],
    [Date.new(2024, 1, 2), "GBP", 113_000.0, 113_000.0, 113_000.0],
  ],
  "duplicates" => [
    [Date.new(2025, 11, 17), "USD", 89_500.0, 89_500.0, 89_500.0],
    [Date.new(2025, 11, 17), "USD", 89_500.0, 89_500.0, 89_500.0],
  ],
  "conflict" => [
    [Date.new(2025, 11, 17), "USD", 89_500.0, 89_500.0, 89_500.0],
    [Date.new(2025, 11, 17), "USD", 89_600.0, 89_600.0, 89_600.0],
  ],
}.freeze

dir = File.join(__dir__, "fixtures")
FileUtils.mkdir_p(dir)
FIXTURES.each { |name, sheets| File.binwrite(File.join(dir, "#{name}.xls"), build_xls(sheets)) }
