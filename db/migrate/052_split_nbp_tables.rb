# frozen_string_literal: true

require "bucket"

# NBP publishes average rates against the zloty in two tables: Table A every business day for about 30 currencies, and
# Table B on Wednesdays for about 120 others. Both were stored under NBP. They now become two datasets of the provider,
# NBP.A for Table A and the daily gold price, NBP.B for Table B. Both are default, so NBP still selects both, and their
# rows report NBP.
#
# Stored rows do not record their table, so membership comes from NBP's API (tables/A, 2002-01-02 to 2026-11-06, read
# through the adapter's labels). Every Table A currency below sat in the table for one unbroken run of table dates: an
# open run lasts to date. Checked against every stored row, the runs place each in the table that published it, and the
# 149 rows Table B repeated before 2004 already hold Table A's rate. Gold is NBP.A's too. A currency outside its run, or
# never in Table A, is NBP.B's.
#
# Only keys change. Spikes and coverage move with their rows. A rollup moves with the latest observation in its bucket:
# five buckets span a currency's move between tables (ILS and INR in July 2011, CLP in August 2011, RUB in March 2022
# and its week of 2022-03-07), and they keep their stored average under the table the currency moved to. Blend tables
# are untouched, since every row still reports NBP.
Sequel.migration do
  table_a = {
    "ATS" => ["2002-01-02", "2002-02-28"],
    "AUD" => ["2002-01-02", nil],
    "BEF" => ["2002-01-02", "2002-02-28"],
    "BGN" => ["2007-04-04", "2025-12-31"],
    "BRL" => ["2008-01-02", nil],
    "CAD" => ["2002-01-02", nil],
    "CHF" => ["2002-01-02", nil],
    "CLP" => ["2011-08-31", nil],
    "CNY" => ["2008-01-02", nil],
    "CYP" => ["2003-01-07", "2007-12-31"],
    "CZK" => ["2002-01-02", nil],
    "DKK" => ["2002-01-02", nil],
    "EEK" => ["2002-01-02", "2010-12-31"],
    "ESP" => ["2002-01-02", "2002-02-28"],
    "EUR" => ["2002-01-02", nil],
    "FIM" => ["2002-01-02", "2002-02-28"],
    "FRF" => ["2002-01-02", "2002-02-15"],
    "GBP" => ["2002-01-02", nil],
    "GRD" => ["2002-01-02", "2002-02-28"],
    "HKD" => ["2003-01-07", nil],
    "HRK" => ["2008-01-02", "2022-12-30"],
    "HUF" => ["2002-01-02", nil],
    "IDR" => ["2008-01-02", nil],
    "IEP" => ["2002-01-02", "2002-02-08"],
    "ILS" => ["2011-07-27", nil],
    "INR" => ["2011-07-27", nil],
    "ISK" => ["2008-01-02", nil],
    "ITL" => ["2002-01-02", "2002-02-28"],
    "JPY" => ["2002-01-02", nil],
    "KRW" => ["2008-01-02", nil],
    "LTL" => ["2003-01-07", "2014-12-31"],
    "LUF" => ["2002-01-02", "2002-02-28"],
    "LVL" => ["2003-01-07", "2013-12-31"],
    "MTL" => ["2003-01-07", "2007-12-31"],
    "MXN" => ["2008-01-02", nil],
    "MYR" => ["2008-01-02", nil],
    "NLG" => ["2002-01-02", "2002-01-25"],
    "NOK" => ["2002-01-02", nil],
    "NZD" => ["2008-01-02", nil],
    "PHP" => ["2008-01-02", nil],
    "PTE" => ["2002-01-02", "2002-02-28"],
    "RON" => ["2007-04-04", nil],
    "RUB" => ["2003-01-07", "2022-03-08"],
    "SEK" => ["2002-01-02", nil],
    "SGD" => ["2008-01-02", nil],
    "SIT" => ["2003-01-07", "2006-12-29"],
    "SKK" => ["2003-01-07", "2008-12-31"],
    "THB" => ["2008-01-02", nil],
    "TRY" => ["2008-01-02", nil],
    "UAH" => ["2003-01-07", nil],
    "USD" => ["2002-01-02", nil],
    "XDR" => ["2002-01-02", nil],
    "ZAR" => ["2003-01-07", nil],
  }

  up do
    require "rate_scopes"

    datasets = ["NBP.A", "NBP.B"]
    # Every NBP row prices its currency as the base, in zlotys.
    split = lambda do |date|
      in_table_a = table_a.map do |code, (first, last)|
        Sequel.&({ base: code }, last ? { date => first..last } : Sequel[date] >= first)
      end
      Sequel.case({ Sequel.|({ base: "XAU" }, *in_table_a) => "NBP.A" }, "NBP.B")
    end

    transaction do
      next if from(:rates).where(dataset_key: "NBP").empty?

      [:rates, :rate_spikes].each do |table|
        from(table).where(dataset_key: "NBP").update(dataset_key: split.call(:date))
      end

      { weekly_rates: Bucket.week, monthly_rates: Bucket.month }.each do |table, bucket|
        newest = Sequel.function(:row_number).over(partition: [bucket, :base, :quote], order: Sequel.desc(:date))
        latest = from(:rates).where(dataset_key: datasets)
          .select(bucket.as(:bucket_date), :base, :quote, :dataset_key, newest.as(:n))
        # Typed like the rollup tables, so the lookup below can use the key.
        create_table(:nbp_buckets, temp: true) do
          Date :bucket_date
          String :base
          String :quote
          String :dataset_key
          primary_key [:bucket_date, :base, :quote]
        end
        columns = [:bucket_date, :base, :quote, :dataset_key]
        from(:nbp_buckets).insert(columns, from(latest).where(n: 1).select(*columns))
        rollup = Sequel[table]
        owner = from(:nbp_buckets).where(bucket_date: rollup[:bucket_date], base: rollup[:base], quote: rollup[:quote])
          .select(:dataset_key)
        # A bucket without daily rows, should one exist, follows its own date.
        from(table).where(dataset_key: "NBP")
          .update(dataset_key: Sequel.function(:coalesce, owner, split.call(:bucket_date)))
        drop_table(:nbp_buckets)
      end

      { currency_coverages: true, currency_exclusions: false }.each do |table, named|
        codes = from(table).where(dataset_key: "NBP").select_map(:iso_code)
        from(table).where(dataset_key: "NBP").delete
        codes.each do |code|
          rows = from(:rates).where(dataset_key: datasets).where(Sequel.|({ base: code }, { quote: code }))
          rows = RateScopes.current_currencies(RateScopes.named_currencies(rows)) if named
          dates = [Sequel.function(:min, :date).as(:start_date), Sequel.function(:max, :date).as(:end_date)]
          rows.group(:dataset_key).select(:dataset_key, *dates).each do |row|
            from(table).insert(row.merge(iso_code: code))
          end
        end
      end
    end
  end

  down do
    # Irreversible by design: undoing it would merge two datasets the seeds no longer describe.
  end
end
