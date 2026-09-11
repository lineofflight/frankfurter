# frozen_string_literal: true

require "json"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # U.S. Department of the Treasury reporting rates of exchange. Quarterly: one figure per currency, foreign units per
    # USD, effective from the quarter end for the following quarter's federal reporting, with mid-quarter amendments
    # carrying their own effective date. Frequency quarterly, so these never blend (#646, #647).
    #
    # Rows are keyed by a "Country-Currency" label, not an ISO code, and the labels drift: a currency can appear under
    # several labels over the years, and a label can keep a predecessor's magnitudes past a redenomination. CURRENCIES
    # maps each label we relay to a code, with date bounds where a label's values change unit; anything unmapped is
    # dropped, and a label listed earlier wins when two map to the same pair on the same date.
    class UST < Adapter
      API_URL = "https://api.fiscaldata.treasury.gov/services/api/fiscal_service/v1/accounting/od/rates_of_exchange"
      PAGE_SIZE = 10_000
      # Amendments to a quarter land up to two months after its record date, under that record date.
      AMENDMENT_WINDOW_MONTHS = 4

      # label => code, or [[code, from, until], ...] with exclusive `until` and either bound nil for open.
      CURRENCIES = {
        "Euro Zone-Euro" => "EUR",
        "Afghanistan-Afghani" => [["AFN", "2003-01-01", nil]],
        "Albania-Lek" => "ALL",
        "Algeria-Dinar" => "DZD",
        "Angola-Kwanza" => "AOA",
        "Antigua & Barbuda-East Caribbean Dollar" => "XCD",
        "Argentina-Peso" => "ARS",
        "Armenia-Dram" => "AMD",
        "Australia-Dollar" => "AUD",
        "Austria-Schilling" => [["ATS", nil, "2002-03-01"]],
        "Azerbaijan-Manat" => "AZN",
        "Azerbaijan-Second Manat" => [["AZM", nil, "2006-01-01"]],
        "Bahamas-Dollar" => "BSD",
        "Bahrain-Dinar" => "BHD",
        "Bangladesh-Taka" => "BDT",
        "Barbados-Dollar" => "BBD",
        "Belarus-New Ruble" => "BYN",
        "Belarus-Ruble" => [["BYR", nil, "2016-07-01"]],
        "Belgium-Francs" => [["BEF", nil, "2002-03-01"]],
        "Belize-Dollar" => "BZD",
        "Benin-Cfa Franc" => "XOF",
        "Bermuda-Dollar" => "BMD",
        "Bolivia-Boliviano" => "BOB",
        "Bosnia-Marka" => "BAM",
        "Botswana-Pula" => "BWP",
        "Brazil-Real" => "BRL",
        "Brunei-Dollar" => "BND",
        "Bulgaria-Lev New" => "BGN",
        "Burkina Faso-Cfa Franc" => "XOF",
        "Burma-Kyat" => "MMK",
        "Myanmar-Kyat" => "MMK",
        "Burundi-Franc" => "BIF",
        "Cambodia-Riel" => "KHR",
        "Cameroon-Cfa Franc" => "XAF",
        "Canada-Dollar" => "CAD",
        "Cape Verde-Escudo" => "CVE",
        "Cayman Islands-Dollar" => "KYD",
        "Central African Republic-Cfa Franc" => "XAF",
        "Chad-Cfa Franc" => "XAF",
        "Chile-Peso" => "CLP",
        "China-Renminbi" => "CNY",
        "Colombia-Peso" => "COP",
        "Comoros-Franc" => "KMF",
        "Congo-Cfa Franc" => "XAF",
        "Costa Rica-Colon" => "CRC",
        "Cote D'Ivoire-Cfa Franc" => "XOF",
        "Croatia-Kuna" => [["HRK", nil, "2023-01-01"]],
        "Cuba-Chavito" => "CUC",
        "Cuba-Peso" => "CUP",
        "Curacao-Caribbean Guilder" => "XCG",
        "Cyprus-Pound" => [["CYP", nil, "2008-01-01"]],
        "Czech Republic-Koruna" => "CZK",
        "Democratic Republic Of Congo-Congolese Franc" => "CDF",
        "Democratic Republic Of Congo-Franc" => "CDF",
        "Denmark-Krone" => "DKK",
        "Djibouti-Franc" => "DJF",
        "Dominican Republic-Peso" => "DOP",
        "Egypt-Pound" => "EGP",
        "El Salvador-Colon" => [["SVC", nil, "2015-01-01"]],
        "Equatorial Guinea-Cfa Franc" => "XAF",
        "Eritrea-Nakfa" => "ERN",
        "Estonia-Kroon" => [["EEK", nil, "2011-01-01"]],
        "Eswatini-Lilangeni" => "SZL",
        "Swaziland-Lilangeni" => "SZL",
        "Ethiopia-Birr" => "ETB",
        "Fiji-Dollar" => "FJD",
        "Finland-Markka" => [["FIM", nil, "2002-03-01"]],
        "France-Franc" => [["FRF", nil, "2002-03-01"]],
        "Gabon-Cfa Franc" => "XAF",
        "Gambia-Dalasi" => "GMD",
        "Georgia-Lari" => "GEL",
        "Germany-Mark" => [["DEM", nil, "2002-03-01"]],
        "Ghana-Cedi" => "GHS",
        "Ghana-Second Cedi" => [["GHC", nil, "2007-07-01"], ["GHS", "2007-07-01", "2015-01-01"]],
        "Greece-Drachma" => [["GRD", nil, "2002-03-01"]],
        "Grenada-East Caribbean Dollar" => "XCD",
        "Guatemala-Quetzal" => "GTQ",
        "Guinea Bissau-Cfa Franc" => "XOF",
        "Guinea-Franc" => "GNF",
        "Guyana-Dollar" => "GYD",
        "Haiti-Gourde" => "HTG",
        "Honduras-Lempira" => "HNL",
        "Hong Kong-Dollar" => "HKD",
        "Hungary-Forint" => "HUF",
        "Iceland-Krona" => "ISK",
        "India-Rupee" => "INR",
        "Indonesia-Rupiah" => "IDR",
        "Iran-Rial" => "IRR",
        "Iraq-Dinar" => "IQD",
        "Ireland-Pound" => [["IEP", nil, "2002-03-01"]],
        "Israel-Shekel" => "ILS",
        "Italy-Lira" => [["ITL", nil, "2002-03-01"]],
        "Jamaica-Dollar" => "JMD",
        "Japan-Yen" => "JPY",
        "Jordan-Dinar" => "JOD",
        "Kazakhstan-Tenge" => "KZT",
        "Kenya-Shilling" => "KES",
        "Korea-Won" => "KRW",
        "Kuwait-Dinar" => "KWD",
        "Kyrgyzstan-Som" => "KGS",
        "Laos-Kip" => "LAK",
        "Latvia-Lats" => [["LVL", nil, "2014-01-01"]],
        "Lebanon-Pound" => "LBP",
        "Lesotho-Maloti" => "LSL",
        "Liberia-Dollar" => "LRD",
        "Libya-Dinar" => "LYD",
        "Lithuania-Lita" => [["LTL", nil, "2015-01-01"]],
        "Luxembourg-Franc" => [["LUF", nil, "2002-03-01"]],
        "Macao-Mop" => "MOP",
        "Madagascar-Ariary" => "MGA",
        "Malawi-Kwacha" => "MWK",
        "Malaysia-Ringgit" => "MYR",
        "Maldives-Rufiyaa" => "MVR",
        "Mali-Cfa Franc" => "XOF",
        "Maltese-Lira" => [["MTL", nil, "2008-01-01"]],
        "Mauritania-First Ouguiya" => "MRO",
        "Mauritania-Ouguiya" => [["MRO", nil, "2018-06-30"], ["MRU", "2018-06-30", nil]],
        "Mauritius-Rupee" => "MUR",
        "Mexico-Peso" => "MXN",
        "Moldova-Leu" => "MDL",
        "Mongolia-Tugrik" => "MNT",
        "Morocco-Dirham" => "MAD",
        "Mozambique-Metical" => "MZN",
        "Namibia-Dollar" => "NAD",
        "Nepal-Rupee" => "NPR",
        "Netherlands Antilles-Guilder" => "ANG",
        "Netherlands-Guilder" => [["NLG", nil, "2002-03-01"]],
        "New Zealand-Dollar" => "NZD",
        "Nicaragua-Cordoba" => "NIO",
        "Niger-Cfa Franc" => "XOF",
        "Nigeria-Naira" => "NGN",
        "Norway-Krone" => "NOK",
        "Oman-Rial" => "OMR",
        "Pakistan-Rupee" => "PKR",
        "Papua New Guinea-Kina" => "PGK",
        "Paraguay-Guarani" => "PYG",
        "Peru-Sol" => "PEN",
        "Philippines-Peso" => "PHP",
        "Poland-Zloty" => "PLN",
        "Portugal-Escudo" => [["PTE", nil, "2002-03-01"]],
        "Qatar-Riyal" => "QAR",
        "Republic Of North Macedonia-Denar" => "MKD",
        "Romania-New Leu" => "RON",
        "Romania-Third Leu" => [["ROL", nil, "2005-07-01"]],
        "Russia-Ruble" => "RUB",
        "Rwanda-Franc" => "RWF",
        "Sao Tome & Principe-New Dobras" => "STN",
        "Sao Tome & Principe-Dobras" => [["STD", nil, "2018-01-01"]],
        "Saudi Arabia-Riyal" => "SAR",
        "Senegal-Cfa Franc" => "XOF",
        "Serbia-Dinar" => "RSD",
        "Seychelles-Rupee" => "SCR",
        "Sierra Leone-Leone" => [["SLL", nil, "2022-07-01"], ["SLE", "2022-07-01", nil]],
        "Singapore-Dollar" => "SGD",
        "Slovak-Korun" => [["SKK", nil, "2009-01-01"]],
        "Slovenia-Tolars" => [["SIT", nil, "2007-01-01"]],
        "Solomon Islands-Dollar" => "SBD",
        "Somali-Shilling" => "SOS",
        "South Africa-Rand" => "ZAR",
        "South Sudan-Sudanese Pound" => "SSP",
        "Spain-Peseta" => [["ESP", nil, "2002-03-01"]],
        "Sri Lanka-Rupee" => "LKR",
        "St. Lucia-East Caribbean Dollar" => "XCD",
        "Sudan-Pound" => "SDG",
        "Sudan-Sudanese Pound" => "SDG",
        "Sudan-Dinar" => [["SDG", "2007-07-01", nil]],
        "Suriname-Dollar" => "SRD",
        "Suriname-Guilder" => [["SRD", "2004-07-01", nil]],
        "Sweden-Krona" => "SEK",
        "Switzerland-Franc" => "CHF",
        "Syria-Pound" => "SYP",
        "Taiwan-Dollar" => "TWD",
        "Tajikistan-Somoni" => "TJS",
        "Tanzania-Shilling" => "TZS",
        "Thailand-Baht" => "THB",
        "Togo-Cfa Franc" => "XOF",
        "Tonga-Pa'Anga" => "TOP",
        "Trinidad & Tobago-Dollar" => "TTD",
        "Tunisia-Dinar" => "TND",
        "Turkey-New Lira" => "TRY",
        "Turkey-Lira" => [["TRL", nil, "2005-01-01"]],
        "Turkmenistan-New Manat" => "TMT",
        "Turkmenistan-Manat" => [["TMM", nil, "2009-01-01"]],
        "Uganda-Shilling" => "UGX",
        "Ukraine-Hryvnia" => "UAH",
        "United Arab Emirates-Dirham" => "AED",
        "United Kingdom-Pound" => "GBP",
        "Uruguay-Peso" => "UYU",
        "Uzbekistan-Som" => "UZS",
        "Vanuatu-Vatu" => "VUV",
        "Venezuela-Bolivar Soberano" => "VES",
        "Venezuela-Bolivar" => "VEF",
        "Venezuela-Soberano" => [["VEF", "2008-01-01", nil]],
        "Vietnam-Dong" => "VND",
        "Western Samoa-Tala" => "WST",
        "Yemen-Rial" => "YER",
        "Zambia-New Kwacha" => "ZMW",
        "Zambia-Kwacha" => [["ZMK", nil, "2013-01-01"]],
        "Zimbabwe-Gold" => "ZWG",
        "Zimbabwe-Rtgs" => [["ZWL", "2019-02-22", nil]],
      }.freeze

      PRIORITY = CURRENCIES.keys.each_with_index.to_h.freeze

      def fetch(after: nil, upto: nil)
        upto ||= Date.today
        rows = []
        page = 1
        loop do
          body = JSON.parse(http.get(API_URL, params: query(after, page)).to_s)
          rows.concat(body["data"])
          break if page >= Integer(body.dig("meta", "total-pages") || 1)

          page += 1
        end

        # Parse once over every page, so a record date straddling a page boundary still collapses to one row per pair.
        # The lower bound is inclusive: a fresh backfill starts on coverage_start, and a re-fetch from last_synced picks
        # up amendments effective that day; the insert is conflict-free, so replaying a day costs nothing.
        parse(rows).select { |r| (after.nil? || r[:date] >= after) && r[:date] <= upto }
      end

      # One record per (date, quote): amendments carry their own effective date, and when two labels reach the same pair
      # on the same date the one listed first in CURRENCIES wins.
      def parse(rows)
        rows.filter_map { |row| record(row) }
          .group_by { |r| [r[:date], r[:quote]] }
          .map { |_, group| group.min_by { |r| r[:priority] } }
          .map { |r| r.except(:priority) }
      end

      private

      def query(after, page)
        params = { "page[size]" => PAGE_SIZE, "page[number]" => page, "sort" => "record_date",
                   "fields" => "record_date,effective_date,country_currency_desc,exchange_rate", }
        params["filter"] = "record_date:gte:#{after << AMENDMENT_WINDOW_MONTHS}" if after
        params
      end

      def record(row)
        label = row["country_currency_desc"]
        date = Date.parse(row["effective_date"])
        code = code_for(label, date)
        return unless code

        { date:, base: "USD", quote: code, rate: Float(row["exchange_rate"]), priority: PRIORITY[label] }
      end

      def code_for(label, date)
        entry = CURRENCIES[label]
        return entry unless entry.is_a?(Array)

        entry.each do |code, from, till|
          next if from && date < Date.parse(from)
          next if till && date >= Date.parse(till)

          return code
        end
        nil
      end
    end
  end
end
