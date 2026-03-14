# frozen_string_literal: true

module Bank
  class Provider
    def current
      raise NotImplementedError
    end

    def ninety_days
      raise NotImplementedError
    end

    def historical
      raise NotImplementedError
    end

    def saved_data
      raise NotImplementedError
    end

    def supported_currencies
      raise NotImplementedError
    end
  end
end
