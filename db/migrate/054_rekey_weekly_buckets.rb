# frozen_string_literal: true

# Weekly rollups used 1 January plus 7 * strftime('%W'), which shifted the bucket weekday every year, split New Year
# weeks, and pushed week 53 of Monday-start years past 1 January of the next. Rekey weekly_rates to ISO Mondays and
# clear blended_weekly_rates so the startup job repopulates it on the new keys.
pooled = {
  "BCT.INTERBANK" => "BCT",
  "CBIC.IMPORT" => "CBIC",
  "DAB.TRANSFER" => "DAB",
  "NBP.A" => "NBP",
  "NBP.B" => "NBP",
  "TCMB.FOREX" => "TCMB",
}

rebuild = lambda do |db, bucket|
  dataset_label = Sequel.case({ is_default: :provider_key }, :key)
  from_datasets = db.from(:datasets).where(key: Sequel[:rates][:dataset_key]).select(dataset_label)
  label = Sequel.function(:coalesce, from_datasets, Sequel.case(pooled, :dataset_key, :dataset_key))
  latest = { partition: [bucket, label, :base, :quote], order: [Sequel.desc(:date), Sequel.desc(:dataset_key)] }
  source = db.from(:rates).select(
    bucket.as(:bucket_date), label.as(:label), :base, :quote, :rate,
    Sequel.function(:first_value, :dataset_key).over(**latest).as(:owner),
  ).from_self
    .select(:bucket_date, :owner, :base, :quote, Sequel.function(:avg, :rate))
    .group(:bucket_date, :label, :base, :quote, :owner)

  db.transaction do
    next if db.from(:rates).empty? && db.from(:weekly_rates).empty? && db.from(:blended_weekly_rates).empty?

    db.from(:weekly_rates).delete
    db.from(:weekly_rates).insert([:bucket_date, :dataset_key, :base, :quote, :rate], source)
    db.from(:blended_weekly_rates).delete
  end
end

Sequel.migration do
  up do
    rebuild.call(self, Sequel.function(:date, :date, "-6 days", "weekday 1"))
  end

  down do
    week_num = Sequel.cast(Sequel.function(:strftime, "%W", :date), Integer)
    day_offset = Sequel.join(["+", week_num * 7, " days"])
    year_start = Sequel.function(:strftime, "%Y-01-01", :date)
    bucket = Sequel.function(:date, Sequel.function(:strftime, "%Y-%m-%d", year_start, day_offset))
    rebuild.call(self, bucket)
  end
end
