# frozen_string_literal: true

require "date"

require "bucket"

# The Bank of Lithuania labels its old-manat series as AZN without restating the values: 1 "AZN" = 0.00063 LTL on
# 2005-12-30 is old manat, 4593 to the dollar. LB kept quoting old manat for the first week of 2006 and switched to new
# manat on 2006-01-09. The adapter now emits AZM for rows before that date; this relabels what is already stored.
#
# The values are right and only the code is wrong, so the rows are updated in place rather than deleted and refetched.
# Three things derived from them need the same relabel:
#
# - Rollups. Weekly and monthly buckets for LB's AZN and AZM pairs are rebuilt from the relabelled rates, the same way
#   Provider#refresh_rollup does it, since the January 2006 monthly bucket straddles the cutover.
# - Currency summaries. currencies and currency_coverages only ever widen on insert, so AZN's start date has to be
#   recomputed, and AZM's inserted.
# - Blend. LB was the only provider quoting AZN before 2006-01-02, so every stored AZN blend up to then was computed
#   from exactly these rows and is unchanged under the new code: relabelling those blend rows is equivalent to
#   recomputing them. From 2006-01-02 BDI, NBT and NBKR join with new manat while LB still quotes old, so that window
#   is recomputed, with the carry-forward lookback past LB's last old-manat row.
Sequel.migration do
  up do
    # Loaded here, not at the top: the migrator loads every file before running any, and the models bind to tables that
    # do not exist yet on a fresh database.
    require "blended_rate"

    cutover = Date.new(2006, 1, 9)
    codes = ["AZN", "AZM"]

    from(:rates).where(provider: "LB", base: "AZN").where { date < cutover }.update(base: "AZM")

    { weekly_rates: Bucket.week, monthly_rates: Bucket.month }.each do |table, bucket|
      buckets = from(:rates).where(provider: "LB", base: codes).select_map(bucket).uniq
      from(table).where(provider: "LB", base: codes).delete
      from(table).insert(
        [:bucket_date, :provider, :base, :quote, :rate],
        from(:rates)
          .where(provider: "LB", base: codes)
          .where(bucket => buckets)
          .select(bucket, :provider, :base, :quote, Sequel.function(:avg, :rate))
          .group(:provider, :base, :quote, bucket),
      )
    end

    codes.each do |code|
      from(:currency_coverages).where(iso_code: code).delete
      from(:rates)
        .where(Sequel.|({ base: code }, { quote: code }))
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

    existing_azm = from(:blended_rates).where(quote: "AZM").select_map(:date)
    from(:blended_rates)
      .where(quote: "AZN")
      .where { date < Date.new(2006, 1, 2) }
      .exclude(date: existing_azm)
      .update(quote: "AZM")
    from(:blended_rates).where(quote: "AZN").where { date < Date.new(2006, 1, 2) }.delete
    BlendedRate.refresh(Date.new(2006, 1, 2)..Date.new(2006, 1, 31))
  end

  down do
    # Irreversible by design: the adapter no longer produces the mislabelled rows.
  end
end
