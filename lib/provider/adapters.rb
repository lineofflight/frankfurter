# frozen_string_literal: true

require "provider/adapters/adapter"

Dir[File.expand_path("adapters/*.rb", __dir__)].each do |file|
  require file unless file.end_with?("adapter.rb")
end
