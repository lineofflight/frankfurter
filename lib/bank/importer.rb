# frozen_string_literal: true

require "bank/provider"
require "bank/providers/ecb"

module Bank
  class Importer
    class << self
      attr_writer :providers

      def providers
        @providers ||= [Bank::Providers::ECB.new].freeze
      end
    end

    def initialize(providers: self.class.providers)
      @providers = providers
    end

    def current
      import(:current)
    end

    def ninety_days
      import(:ninety_days)
    end

    def historical
      import(:historical)
    end

    def saved_data
      import(:saved_data)
    end

    private

    attr_reader :providers

    def import(dataset)
      providers.each_with_object({}) do |provider, result|
        provider.public_send(dataset).each do |day|
          result[day[:date]] ||= {}
          day[:rates].each do |iso_code, rate|
            result[day[:date]][iso_code] ||= rate
          end
        end
      end.sort.map do |date, rates|
        { date: date, rates: rates }
      end
    end
  end
end
