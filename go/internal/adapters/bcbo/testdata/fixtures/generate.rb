# frozen_string_literal: true

# Writes the workbooks bcbo_spec.rb builds inline with the spreadsheet gem, so the Go tests read the same bytes. From
# the repository root:
#   APP_ENV=test mise exec -- bundle exec ruby go/internal/adapters/bcbo/testdata/fixtures/generate.rb

require "spreadsheet"
require "stringio"

def write(name, sheet_name, rows)
  book = Spreadsheet::Workbook.new
  sheet = book.create_worksheet(name: sheet_name)
  rows.each { |index, values| sheet.row(index).replace(values) }
  io = StringIO.new
  book.write(io)
  File.binwrite(File.join(__dir__, "#{name}.xls"), io.string)
end

yearly = "COTIZACIONES OFICIALES 2024"
daily = "COTIZACION DE MONEDAS"

write("yearly_mid", yearly, {
  5 => [nil, "VENTA", "COMPRA"],
  6 => [1.0, 6.96, 6.86],
})
write("yearly_months", yearly, {
  7 => [2.0, 6.96, 6.86, nil, nil, 7.00, 6.90],
})
write("yearly_prom", yearly, {
  6 => [1.0, 6.96, 6.86],
  37 => ["PROM", "6,96", "6,86"],
})
write("yearly_missing_month", yearly, {
  36 => [31.0, 6.96, 6.86, nil, nil],
})
write("yearly_invalid_date", yearly, {
  35 => [30.0, nil, nil, 6.96, 6.86],
})
write("daily_legacy", daily, {
  11 => ["ESTADOS UNIDOS", "DOLAR VENTA", "", "USD.VENTA", "6.96", ""],
  12 => ["ESTADOS UNIDOS", "DOLAR COMPRA", "", "USD.COMPRA", "6.86", ""],
  13 => ["UNION EUROPEA", "EURO", "", "EUR", "7.91", "0.86"],
  14 => ["ECUADOR", "DÓLAR", "", "USD", "6.86", "1.0"],
  15 => ["", "DERECHO ESPECIAL DE GIRO", "", "USD/D.E.G.", "", "1.36"],
  16 => ["ORO", "ONZA TROY ORO", "", "USD./O.T.F.", "4082.56"],
  17 => ["PLATA", "ONZA TROY PLATA", "", "USD./O.T.F.", "63.73"],
})
write("daily_current", daily, {
  11 => ["Pais / Concepto", "Moneda", "Codigo", "Tipo de Cambio Oficial (TCO) (Bs/USD)"],
  12 => ["ESTADOS UNIDOS", "DOLAR", "USD", "10.5"],
  15 => ["Pais / Region", "Moneda", "Codigo", "TIPO DE CAMBIO EN Bs POR UNIDAD", "TIPO CAMBIO EN M.E."],
  16 => ["UNION EUROPEA", "EURO", "EUR", "11.95314", "0.87843"],
  17 => ["JAPON", "YEN", "JPY", "0.06464", "162.44"],
  40 => ["BOLIVIA (UFV)", "UNIDAD DE FOMENTO DE VIVIENDA", "Bs/UFV", "3.30736"],
  44 => ["ORO", "ONZA TROY ORO", "", "3999.28"],
  45 => ["PLATA", "ONZA TROY PLATA", "", "57.4583"],
  49 => ["BOLIVIA", "DERECHO ESPECIAL DE GIRO", "", "1.35904"],
  53 => ["SOFR (Secured Overnight Financing Rate)*", "", "", "", "0.0355"],
})
