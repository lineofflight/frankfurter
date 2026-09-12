# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cbvs"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CBVS do
      before do
        VCR.insert_cassette("cbvs", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { CBVS.new }

      it "fetches rates with SRD as the quote currency" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 7), upto: Date.new(2026, 9, 8))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["SRD"])
        _(dataset.map { |r| r[:date] }.uniq.sort).must_equal([Date.new(2026, 9, 7), Date.new(2026, 9, 8)])
      end

      it "covers the 11 quoted currencies" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 8), upto: Date.new(2026, 9, 8))

        _(dataset.map { |r| r[:base] }.sort)
          .must_equal(["AWG", "BBD", "BRL", "CNY", "EUR", "GBP", "GYD", "TTD", "USD", "XCD", "XCG"])
      end

      it "emits the transfer midpoint of the closing fixing" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 8), upto: Date.new(2026, 9, 8))
        usd = dataset.find { |r| r[:base] == "USD" }

        # 15:00 fixing: buy 37,803, sell 37,923. The 10:00 fixing that day had USD at 37,612 / 37,682.
        _(usd[:rate]).must_equal(37.863)
      end

      it "normalises GYD from per 100 to per unit" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 8), upto: Date.new(2026, 9, 8))
        gyd = dataset.find { |r| r[:base] == "GYD" }

        _(gyd[:rate]).must_equal(0.18139)
      end

      describe "#parse" do
        it "skips a page whose text cannot be extracted" do
          good = Struct.new(:text).new(<<~TEXT)
                                               WISSELKOERSNOTERINGEN IN SRD
                       18 JUNI 2025 VASTGESTELD OMSTREEKS 15:00U EN GELDIG TOT NADER ORDER
            U.S. DOLLAR (USD)                                        37,500             37,700         37,300       37,400
          TEXT
          bad = Object.new
          def bad.text = raise(PDF::Reader::MalformedPDFError, "current font is invalid")
          reader = Struct.new(:pages).new([bad, good])

          fixings = PDF::Reader.stub(:new, reader) { adapter.parse("%PDF-1.7") }

          _(fixings.map { |f| f[:time] }).must_equal(["15:00"])
        end
      end

      describe "#parse_page" do
        it "reads the legacy layout without a fixing time" do
          text = <<~TEXT
                                     C E N T R A L E B A N K V A N S U R I N A M E
                                               WISSELKOERSNOTERINGEN IN SRD
                                           01 SEPTEMBER 2009 EN TOT NADER ORDER
            GELDSOORT                                       AANKOOP*           VERKOOP*      AANKOOP*        VERKOOP*
            U.S. DOLLAR (USD)                                     2,710             2,780         2,710           2,780
            GUYANA DOLLAR (PER 100 GYD)                           1,290             1,370         1,290           1,370
          TEXT

          fixing = adapter.parse_page(text)

          _(fixing[:date]).must_equal(Date.new(2009, 9, 1))
          _(fixing[:time]).must_be_nil
          _(fixing[:records]).must_equal([
            { date: Date.new(2009, 9, 1), base: "USD", quote: "SRD", rate: 2.745, bid: 2.71, ask: 2.78, mid: nil },
            { date: Date.new(2009, 9, 1), base: "GYD", quote: "SRD", rate: 0.0133, bid: 0.0129, ask: 0.0137, mid: nil },
          ])
        end

        it "reads the 2013 layout with a dot as decimal separator" do
          text = <<~TEXT
                                                WISSELKOERSNOTERINGEN IN SRD
                                               02 JANUARI 2013 EN TOT NADER ORDER
            U.S. DOLLAR (USD)                                      3.250              3.350          3.250           3.350
          TEXT

          _(adapter.parse_page(text)[:records].first[:rate]).must_equal(3.3)
        end

        it "reads the fixing time, tolerating the 12.30:00U typo" do
          text = <<~TEXT
                                             WISSELKOERSNOTERINGEN IN SRD
                   20 NOVEMBER 2023 VASTGESTELD OMSTREEKS 12.30:00U EN GELDIG TOT NADER ORDER
            U.S. DOLLAR (USD)                                    38,019           38,072        37,369        37,700
          TEXT

          fixing = adapter.parse_page(text)

          _(fixing[:date]).must_equal(Date.new(2023, 11, 20))
          _(fixing[:time]).must_equal("12:30")
        end

        it "reads current labels, including a lost closing parenthesis" do
          text = <<~TEXT
                                               WISSELKOERSNOTERINGEN IN SRD
                       08 SEPTEMBER 2026 VASTGESTELD OMSTREEKS 15:00U EN GELDIG TOT NADER ORDER
            CARIBISCHE GULDEN (XCG)                                  20,771             21,178         20,517       20,923
            EASTERN CARIBBEAN DOLLAR (XCD                            14,001             14,276         13,830       14,104
            GUYANA DOLLAR (GYD PER 100 )                             17,963             18,315         17,743       18,094
            CHINESE YUAN RENMINBI (PER CNY)                           5,633              5,744          5,564        5,675
          TEXT

          fixing = adapter.parse_page(text)

          _(fixing[:time]).must_equal("15:00")
          _(fixing[:records].map { |r| [r[:base], r[:rate]] })
            .must_equal([["XCG", 20.9745], ["XCD", 14.1385], ["GYD", 0.18139], ["CNY", 5.6885]])
        end

        it "keeps the main table when the notice appends quotes at the maximum selling rate" do
          text = <<~TEXT
                                          WISSELKOERSNOTERINGEN IN SRD
                                           02 MAART 2021 EN TOT NADER ORDER
            U.S. DOLLAR (USD)                               14,018           14,290        14,018        14,290
            EURO (EUR)                                      16,896           17,224        16,890        17,226
            N.B. Bovenstaande koersen zijn de koersnoteringen op basis van de minimumverkoopkoers voor de USD.
            Op basis van de maximumverkoopkoers voor de USD, zijn de USD- en EUR-koersnoteringen als volgt:
            U.S. DOLLAR (USD)                               15,990           16,300        15,990        16,300
            EURO (EUR)                                      19,273           19,646        19,265        19,648
          TEXT

          _(adapter.parse_page(text)[:records].map { |r| [r[:base], r[:rate]] })
            .must_equal([["USD", 14.154], ["EUR", 17.06]])
        end

        it "skips an English rendition of the notice" do
          text = <<~TEXT
                                                       EXCHANGE RATES IN SRD
                      February 10, 2022 determined around 15:00h and valid until further notice
            U.S. DOLLAR (USD)                                     21.421      21.561       20.645        20.687
          TEXT

          _(adapter.parse_page(text)).must_be_nil
        end

        it "skips the sell-only extended overview" do
          text = <<~TEXT
                                 UITGEBREID WISSELKOERSENOVERZICHT
                       VERKOOPKOERSEN SRD VAN KRACHT M.I.V.:  01-03-2022
            U.S. DOLLAR       (PER USD 1)                 SRD       21,57
          TEXT

          _(adapter.parse_page(text)).must_be_nil
        end

        it "skips an internal gold-certificate page that carries a date line" do
          text = <<~TEXT
                                          WAARDE POWISI GOUDCERTIFICATEN
                                        UITSLUITEND VOOR INTERN GEBRUIK CBvS
            CHINESE YUAN RENMINBI (PER CNY)                 0,531            0,547
            DATUM:             02 OKTOBER 2013 EN TOT NADER ORDER
          TEXT

          _(adapter.parse_page(text)).must_be_nil
        end
      end

      describe "#closing" do
        let(:usd) { ->(date, rate) { [{ date:, base: "USD", quote: "SRD", rate: }] } }

        it "keeps the last fixing of each day" do
          fixings = [
            { date: Date.new(2026, 9, 7), time: "10:00", records: usd[Date.new(2026, 9, 7), 1.0] },
            { date: Date.new(2026, 9, 7), time: "15:00", records: usd[Date.new(2026, 9, 7), 3.0] },
            { date: Date.new(2026, 9, 7), time: "12:30", records: usd[Date.new(2026, 9, 7), 2.0] },
          ]

          selected = adapter.closing(fixings, Date.new(2026, 9, 1), Date.new(2026, 9, 8), today: Date.new(2026, 9, 9))

          _(selected.map { |f| f[:time] }).must_equal(["15:00"])
        end

        it "lets a later page win a tie, as with legacy pages without a time" do
          fixings = [
            { date: Date.new(2010, 2, 4), time: nil, records: usd[Date.new(2010, 2, 4), 1.0] },
            { date: Date.new(2010, 2, 4), time: nil, records: usd[Date.new(2010, 2, 4), 2.0] },
          ]

          selected = adapter.closing(fixings, nil, Date.new(2010, 12, 31), today: Date.new(2026, 9, 9))

          _(selected.first[:records].first[:rate]).must_equal(2.0)
        end

        it "holds today back until the closing fixing is out" do
          today = Date.new(2026, 9, 9)
          fixings = [
            { date: today - 1, time: "12:30", records: usd[today - 1, 1.0] },
            { date: today, time: "10:00", records: usd[today, 2.0] },
            { date: today, time: "12:30", records: usd[today, 3.0] },
          ]

          _(adapter.closing(fixings, today - 1, today, today:).map { |f| f[:date] }).must_equal([today - 1])

          fixings << { date: today, time: "15:00", records: usd[today, 4.0] }

          _(adapter.closing(fixings, today - 1, today, today:).map { |f| f[:date] }).must_equal([today - 1, today])
        end

        it "filters by the requested window" do
          fixings = (1..5).map do |d|
            { date: Date.new(2026, 9, d), time: nil, records: usd[Date.new(2026, 9, d), 1.0] }
          end

          selected = adapter.closing(fixings, Date.new(2026, 9, 2), Date.new(2026, 9, 4), today: Date.new(2026, 9, 9))

          _(selected.map { |f| f[:date].day }).must_equal([2, 3, 4])
        end
      end

      describe "#coverage" do
        it "classifies archive links by the span they cover" do
          _(adapter.coverage("/Wisselkoersen/2026/DO260908 15.00 uur.pdf"))
            .must_equal(Date.new(2026, 9, 8)..Date.new(2026, 9, 8))
          _(adapter.coverage("/Wisselkoersen/2026/DO260814 12.30 uu.pdf"))
            .must_equal(Date.new(2026, 8, 14)..Date.new(2026, 8, 14))
          _(adapter.coverage("/Wisselkoersen/2024/Maandoverzichten_2024/WK_FEBRUARI_2024.pdf"))
            .must_equal(Date.new(2024, 2, 1)..Date.new(2024, 2, 29))
          _(adapter.coverage("/Wisselkoersen/2025/Maandoverzichten_2025/NL/WisselkoersnoteringMaarti2025.pdf"))
            .must_equal(Date.new(2025, 3, 1)..Date.new(2025, 3, 31))
          _(adapter.coverage("/Wisselkoersen/2025/Maandoverzichten_2025/NL/WisselkoersnoteringjJuli2025.pdf"))
            .must_equal(Date.new(2025, 7, 1)..Date.new(2025, 7, 31))
          _(adapter.coverage("/Wisselkoersen/2009/jaar-2009sep-dec.pdf"))
            .must_equal(Date.new(2009, 1, 1)..Date.new(2009, 12, 31))
          _(adapter.coverage("/Wisselkoersen/2023/Jaar_2023_WK.pdf"))
            .must_equal(Date.new(2023, 1, 1)..Date.new(2023, 12, 31))
          _(adapter.coverage("/Wisselkoersen/ALL/Jaar_2015.pdf"))
            .must_equal(Date.new(2015, 1, 1)..Date.new(2015, 12, 31))
        end

        it "ignores other PDFs under the Wisselkoersen path" do
          _(adapter.coverage("/pdf/Richtlijnen/Circulaire_dagelijkse_vaststelling_van_de_wisselkoersen.pdf"))
            .must_be_nil
        end
      end
    end
  end
end
