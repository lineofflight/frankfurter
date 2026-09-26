# frozen_string_literal: true

# Coverage is a property of a query, not the publication dates of a currency or provider. Boundaries are stored
# observation dates at which a snapshot returns at least one requested record. They do not promise an uninterrupted
# series, extend history by carry-forward days, or confuse a carried quote's date with the arrival of its base bridge.
module RateCoverage
  PARAMS = ["base", "quotes", "providers"].freeze
  PAGE_SIZE = 64

  def coverage
    stored = blended_table?
    dataset = stored ? BlendedRate.dataset : raw_dataset
    acquire_slot! unless stored
    dates = coverage_dates(dataset, stored:)
    first = coverage_boundary(dates, stored:, reverse: false)
    last = coverage_boundary(dates.where { date >= first }, stored:, reverse: true) if first
    { base:, quotes:, providers:, start_date: first&.to_s, end_date: last&.to_s }
  ensure
    release_slot
  end

  private

  # EXISTS only prunes impossible snapshots; the actual rate query still decides eligibility. In particular, merely
  # overlapping MIN/MAX currency dates says nothing about gaps, USD connectivity, or a single provider's native cross.
  def coverage_dates(dataset, stored:)
    # Raw anchors keep live/stored bounds identical when a publication yields only carried-forward output.
    dates = raw_dataset.from(Sequel.as(:rates, :coverage_dates)).select(:date).distinct
    return dates.where(false) if provider_peg_base?

    if stored
      dates = dates.where(coverage_presence(dataset)) if base == self.class::PIVOT
      dates = dates.where(coverage_presence(dataset.where(quote: base))) unless base == self.class::PIVOT
      if quotes && !quotes.intersect?([base, self.class::PIVOT])
        dates = dates.where(coverage_presence(dataset.where(quote: quotes)))
      end
    else
      dates = dates.where(coverage_presence(coverage_currency_rows(dataset, [base]))) unless !providers && base_peg
      if quotes && (providers || quotes.none? { |code| Peg.find(code) })
        dates = dates.where(coverage_presence(coverage_currency_rows(dataset, quotes)))
      end
    end
    dates
  end

  def coverage_currency_rows(dataset, codes)
    dataset.where(Sequel.|({ base: codes }, { quote: codes }))
  end

  def coverage_presence(dataset)
    anchor = Sequel[:coverage_dates][:date]
    date = Sequel[dataset.first_source_alias][:date]
    check_deadline!
    first = dataset.min(:date)
    return false unless first

    check_deadline!
    last = dataset.max(:date)
    check_deadline!
    Sequel.&(
      anchor >= first,
      anchor <= Sequel.function(:date, last, "+#{lookback} days"),
      dataset.where(date <= anchor)
        .where(date >= Sequel.function(:date, anchor, "-#{lookback} days")).exists,
    )
  end

  # Keyset pages bound memory and release the DB connection before snapshots run. Searching from both ends avoids
  # computing a century of interior history just to describe its extent; each snapshot shares the rate deadline.
  def coverage_boundary(dates, stored:, reverse:)
    order = reverse ? Sequel.desc(:date) : :date
    loop do
      check_deadline!
      page = dates.order(order).limit(PAGE_SIZE).naked.all
      page.each do |row|
        check_deadline!
        date = row[:date]
        date = Date.parse(date) unless date.is_a?(Date)
        found = false
        each_snapshot(date, stored:) { found = true }
        return date if found
      end
      return if page.size < PAGE_SIZE

      cursor = page.last[:date]
      dates = dates.where(reverse ? Sequel[:date] < cursor : Sequel[:date] > cursor)
    end
  end
end
