# frozen_string_literal: true

require "db"

class Source < Sequel::Model
  one_to_many :currencies, key: :source_code, primary_key: :code

  def validate
    super
    validates_presence([:code, :name])
    validates_unique(:code)
    validates_format(/^[A-Z]{2,10}$/, :code)
  end
end
