# frozen_string_literal: true

require "providers/base"

Dir[File.expand_path("providers/*.rb", __dir__)].each do |file|
  require file unless file.end_with?("base.rb")
end
