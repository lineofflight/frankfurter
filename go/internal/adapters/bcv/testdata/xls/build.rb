# frozen_string_literal: true

# Writes the .xls fixtures for bcv_test.go with the spreadsheet gem, exactly as spec/provider/adapters/bcv_spec.rb builds
# them in memory. Go has no .xls writer, so the workbooks are checked in. Regenerate from the repository root:
#
#   APP_ENV=test mise exec -- bundle exec ruby go/internal/adapters/bcv/testdata/xls/build.rb

require "spreadsheet"
require "stringio"

DIR = __dir__

def write(name, book)
  io = StringIO.new
  book.write(io)
  File.binwrite(File.join(DIR, "#{name}.xls"), io.string)
end

# Mirrors the spec's build_xls helper.
def build_xls(sheets)
  book = Spreadsheet::Workbook.new
  sheets.each do |value_date, rows|
    sheet = book.create_worksheet(name: value_date.delete("/"))
    sheet.row(0).replace([nil, "BANCO CENTRAL DE VENEZUELA"])
    sheet.row(4).replace([nil, "Fecha Operacion: 01/01/2026", nil, "Fecha Valor: #{value_date}"])
    sheet.row(7).replace([nil, nil, nil, "(a) Cotización M.E./US$", nil, "Bs./M.E."])
    sheet.row(8).replace([nil, nil, "Moneda/País", "Compra (BID)", "Venta (ASK)", "Compra (BID)", "Venta (ASK)"])
    rows.each_with_index { |values, i| sheet.row(10 + i).replace(values) }
  end
  book
end

write("ask", build_xls({
  "09/09/2026" => [
    [nil, "EUR", "Zona Euro", 1.16328, 1.1633, 951.63936288, 954.02442394],
    [nil, "USD", "E.U.A.", 1.0, 1.0, 818.0515455, 820.1018],
  ],
}))

write("fecha_valor", build_xls({
  "07/09/2026" => [[nil, "USD", "E.U.A.", 1.0, 1.0, 812.654073, 814.6908]],
  "04/09/2026" => [[nil, "USD", "E.U.A.", 1.0, 1.0, 800.0, 802.0]],
}))

write("mxp", build_xls({
  "09/09/2026" => [[nil, "MXP", "Mexico", 16.913, 16.9148, 48.36821057, 48.48943416]],
}))

write("skips", build_xls({
  "09/09/2026" => [
    [nil, "USD", "E.U.A.", 1.0, 1.0, 818.0515455, 820.1018],
    [nil, "CAD", "Canada", 1.37699, 1.37703, nil, nil],
    [nil, "JPY", "Japon", 154.168, 154.177, 0, 0],
    [nil, ""],
    [nil, "(*) Tipo de Cambio de Referencia producto de las operaciones"],
  ],
}))

book = Spreadsheet::Workbook.new
sheet = book.create_worksheet(name: "09092026")
sheet.row(8).replace([nil, nil, "Moneda/País", "Compra (BID)", "Venta (ASK)", "Compra (BID)", "Venta (ASK)"])
sheet.row(10).replace([nil, "USD", "E.U.A.", 1.0, 1.0, 818.0515455, 820.1018])
write("no_fecha_valor", book)

book = Spreadsheet::Workbook.new
sheet = book.create_worksheet(name: "09092026")
sheet.row(4).replace([nil, "Fecha Operacion: 08/09/2026", nil, "Fecha Valor: 09/09/2026"])
sheet.row(10).replace([nil, "USD", "E.U.A.", 1.0, 1.0, 818.0515455, 820.1018])
write("no_ask", book)
