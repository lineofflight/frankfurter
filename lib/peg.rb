# frozen_string_literal: true

require "json"

Peg = Data.define(:quote, :base, :rate, :since, :authority, :source) do
  class << self
    def all
      @all ||= begin
        dir = File.expand_path("../db/seeds/pegs", __dir__)
        Dir["#{dir}/*.json"].sort.map do |f|
          h = JSON.parse(File.read(f))
          new(
            quote: h["quote"],
            base: h["base"],
            rate: h.fetch("rate", 1.0),
            since: Date.parse(h["since"]),
            authority: h["authority"],
            source: h["source"],
          )
        end.freeze
      end
    end

    def find(quote)
      all.find { |p| p.quote == quote }
    end
  end
end
