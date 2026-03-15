# frozen_string_literal: true

require "providers/ecb"

module Bank
  class << self
    def fetch_all!
      ecb.historical.import
    end

    def fetch_current!
      ecb.current.import
    end

    def replace_all!
      Currency.dataset.delete
      ecb.historical.import
    end

    def seed_with_saved_data!
      xml = File.read(File.join(__dir__, "bank", "eurofxref-hist.xml"))
      Currency.dataset.delete
      Providers::ECB.new(dataset: ecb.parse(xml)).import
    end

    private

    def ecb
      Providers::ECB.new
    end
  end
end
