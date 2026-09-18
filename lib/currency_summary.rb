# frozen_string_literal: true

require "rate_scopes"

# Named, valid observations define currency coverage. Unknown codes keep separate publication ranges so provider health
# can report them without rescanning rate history on every request.
module CurrencySummary
  class << self
    def refresh(db, iso_codes, provider: nil)
      require "provider"

      iso_codes.uniq.each do |code|
        rows = db[:rates].where(Sequel.|({ base: code }, { quote: code }))
        coverage = db[:currency_coverages].where(iso_code: code)
        exclusions = db[:currency_exclusions].where(iso_code: code) if db.table_exists?(:currency_exclusions)
        if provider
          rows = rows.where(provider:)
          coverage = coverage.where(provider_key: provider)
          exclusions = exclusions.where(provider_key: provider) if exclusions
        end
        coverage.delete
        exclusions&.delete

        named = Money::Currency.find(code)
        rows = RateScopes.current_currencies(RateScopes.named_currencies(rows)) if named
        target = named ? coverage : exclusions
        if target
          rows.group(:provider).select(
            :provider,
            Sequel.function(:min, :date).as(:start_date),
            Sequel.function(:max, :date).as(:end_date),
          ).each do |row|
            target.insert(
              provider_key: row[:provider], iso_code: code, start_date: row[:start_date], end_date: row[:end_date],
            )
          end
        end

        db[:currencies].where(iso_code: code).delete
        next unless named

        dates = db[:currency_coverages].where(iso_code: code).exclude(provider_key: Provider.non_blending_keys)
          .select(
            Sequel.function(:min, :start_date).as(:start_date), Sequel.function(:max, :end_date).as(:end_date),
          ).first
        next unless dates[:start_date]

        db[:currencies].insert(iso_code: code, **dates)
      end
    end
  end
end
