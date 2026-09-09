# frozen_string_literal: true

require "bucket"

# Seven more series in the shape of 027: a source labels a currency's whole history with its current ISO code without
# restating the values across a redenomination, so the old rows carry the predecessor's magnitudes under the successor
# code (issue #623). The adapters now emit the predecessor before each source's own switch date; this relabels what is
# already stored, in place, since the values are right and only the code is wrong.
#
#   LB    TMT -> TMM before 2009-01-01  (10000 "TMT" = 1.7354 LTL on 2008-12-31, 10 TMT = 8.677 on 2009-01-01)
#   SARB  ZMW -> ZMK before 2013-01-01  (612.34 per ZAR on 2012-12-31, 0.6188 on 2013-01-02)
#   RB    RUB -> RUR before 1998-01-01  (0.0013 SEK on 1997-12-30, 1.326 on 1998-01-02)
#   CBU   RUB -> RUR before 1998-01-06  (1000 "RUB" = 13.46 UZS on 1997-12-30, 1 RUB = 13.48 on 1998-01-06)
#   BCCH  BRL -> BRR before 1994-07-01  (0.16 CLP on 1994-06-30, 418.34 on 1994-07-01)
#   CBR   TJS -> TJR before 2000-10-30  (1000 "TJS" = 13.54 RUB on 2000-10-01, 1 TJS = 12.65 on 2000-11-01)
#   NBU   TJS -> TJR before 2000-10-30  (1000 "TJS" = 2.78 UAH on 2000-09-01, 1 TJS = 1.81 on 2002-12-02)
#
# Rollups and currency summaries follow 027. The blend does not take 027's sole-contributor shortcut: most of these
# codes had other contributors across the span (BDI, NBU and NBT quote TMM from 1999, BDI, NBP and RBM quote ZMK, LB and
# CBA carry restated RUB and TJS), and the TMT peg anchors every stored TMT row to 3.5 regardless of who quoted it, so a
# relabelled TMT row would carry the peg value into TMM. The blend is recomputed instead over one window from the
# earliest relabelled row to the latest plus the carry-forward lookback, 1993 to January 2013, which takes about a
# minute against prod.
Sequel.migration do
  up do
    # Required here, not at file top: BlendedRate loads the Rate model, whose table does not exist yet when the migrator
    # loads every file on a fresh database.
    require "blended_rate"
    require "carry_forward"

    series = [
      { provider: "LB", code: "TMT", predecessor: "TMM", cutover: "2009-01-01" },
      { provider: "SARB", code: "ZMW", predecessor: "ZMK", cutover: "2013-01-01" },
      { provider: "RB", code: "RUB", predecessor: "RUR", cutover: "1998-01-01" },
      { provider: "CBU", code: "RUB", predecessor: "RUR", cutover: "1998-01-06" },
      { provider: "BCCH", code: "BRL", predecessor: "BRR", cutover: "1994-07-01" },
      { provider: "CBR", code: "TJS", predecessor: "TJR", cutover: "2000-10-30" },
      { provider: "NBU", code: "TJS", predecessor: "TJR", cutover: "2000-10-30" },
    ]

    either_side = ->(codes) { Sequel.|({ base: codes }, { quote: codes }) }

    first = nil
    last = nil
    series.each do |s|
      scope = from(:rates).where(provider: s[:provider]).where(either_side[s[:code]]).where { date < s[:cutover] }
      bounds = scope.select { [min(date).as(:first), max(date).as(:last)] }.first # rubocop:disable Performance/Detect
      next unless bounds[:first]

      first = [first, bounds[:first].to_s].compact.min
      last = [last, bounds[:last].to_s].compact.max
      scope.where(base: s[:code]).update(base: s[:predecessor])
      scope.where(quote: s[:code]).update(quote: s[:predecessor])
    end

    series.group_by { |s| s[:provider] }.each do |provider, group|
      codes = group.flat_map { |s| [s[:code], s[:predecessor]] }.uniq
      { weekly_rates: Bucket.week, monthly_rates: Bucket.month }.each do |table, bucket|
        buckets = from(:rates).where(provider:).where(either_side[codes]).select_map(bucket).uniq
        from(table).where(provider:).where(either_side[codes]).delete
        from(table).insert(
          [:bucket_date, :provider, :base, :quote, :rate],
          from(:rates)
            .where(provider:)
            .where(either_side[codes])
            .where(bucket => buckets)
            .select(bucket, :provider, :base, :quote, Sequel.function(:avg, :rate))
            .group(:provider, :base, :quote, bucket),
        )
      end
    end

    series.flat_map { |s| [s[:code], s[:predecessor]] }.uniq.each do |code|
      from(:currency_coverages).where(iso_code: code).delete
      from(:rates)
        .where(either_side[code])
        .group(:provider)
        .select(:provider, Sequel.function(:min, :date).as(:start_date), Sequel.function(:max, :date).as(:end_date))
        .each do |row|
          from(:currency_coverages).insert(
            provider_key: row[:provider], iso_code: code, start_date: row[:start_date], end_date: row[:end_date],
          )
        end

      from(:currencies).where(iso_code: code).delete
      global = from(:currency_coverages)
        .where(iso_code: code)
        .select(Sequel.function(:min, :start_date).as(:start_date), Sequel.function(:max, :end_date).as(:end_date))
        .first
      next unless global&.[](:start_date)

      from(:currencies).insert(iso_code: code, start_date: global[:start_date], end_date: global[:end_date])
    end

    BlendedRate.refresh(Date.parse(first)..(Date.parse(last) + CarryForward::LOOKBACK_DAYS)) if first
  end

  down do
    # Irreversible by design: the adapters no longer produce the mislabelled rows.
  end
end
