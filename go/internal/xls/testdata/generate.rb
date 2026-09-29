# frozen_string_literal: true

# Writes workbook.xls for xls_test.go with the spreadsheet gem, then reads it back with the gem and writes the cells it
# sees to workbook.json, so the test checks the Go reader against the gem. The shared strings overflow into CONTINUE
# records, with strings split across them, some wide and some compressed. From the repository root:
#
#   APP_ENV=test mise exec -- bundle exec ruby go/internal/xls/testdata/generate.rb

require "date"
require "json"
require "spreadsheet"
require "stringio"

book = Spreadsheet::Workbook.new

strings = book.create_worksheet(name: "Strings")
100.times do |i|
  strings.row(i).replace([
    "Row #{i} " + "abcdefghij" * (i % 7),
    "Cotización #{i}",
    "Курс #{i} " + "ж" * (i % 13),
    "Rate 💱 #{i}",
  ])
end
strings.row(100).replace(["Ж" + "a" * 5000]) # wide, then compressed once the Cyrillic falls in an earlier record
strings.row(101).replace(["é" * 5000])

values = book.create_worksheet(name: "Año Курс")
values.row(0).replace([1, -7, 123_456, -1_000_000, 1.25, -0.5, 3.14159, 1e20, 89_500.0])
values.row(1).replace([Date.new(2026, 9, 7), DateTime.new(2026, 9, 7, 18, 30, 15), nil, true, false, "end"])
values.row(3).replace([nil, nil, 42])

io = StringIO.new
book.write(io)
File.binwrite(File.join(__dir__, "workbook.xls"), io.string)

def cell(value)
  case value
  when nil then { Kind: 0 }
  when String then { Kind: 1, String: value }
  when true, false then { Kind: 4, Number: value ? 1 : 0 }
  when DateTime then { Kind: 3, Date: value.strftime("%Y-%m-%dT%H:%M:%SZ") }
  when Date then { Kind: 3, Date: value.strftime("%Y-%m-%dT00:00:00Z") }
  when Numeric then { Kind: 2, Number: value }
  else raise "unexpected #{value.class}"
  end
end

sheets = Spreadsheet.open(StringIO.new(io.string)).worksheets.map do |sheet|
  rows = sheet.each(0).map do |row|
    cells = row.to_a
    cells.pop while cells.any? && cells.last.nil?
    cells.map { |value| cell(value) }
  end
  { Name: sheet.name, Rows: rows }
end
File.write(File.join(__dir__, "workbook.json"), JSON.generate(sheets))
