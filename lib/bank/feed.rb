# frozen_string_literal: true

require "bank/providers/ecb"

module Bank
  class Feed
    class << self
      def current
        provider.current
      end

      def ninety_days
        provider.ninety_days
      end

      def historical
        provider.historical
      end

      def saved_data
        provider.saved_data
      end

      private

      def provider
        @provider ||= Bank::Providers::ECB.new
      end
    end
  end
end
