# frozen_string_literal: true

require "json"

Peg = Data.define(:quote, :base, :rate, :since, :authority, :source) do
  def self.all
    @all ||= JSON.parse(File.read(File.expand_path("../db/seeds/pegs.json", __dir__))).map { |h|
      new(
        quote: h["quote"],
        base: h["base"],
        rate: h.fetch("rate", 1.0),
        since: Date.parse(h["since"]),
        authority: h["authority"],
        source: h["source"],
      )
    }.freeze
  end

  def self.find(quote)
    all.find { |p| p.quote == quote }
  end
end
