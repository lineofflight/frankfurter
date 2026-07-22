# frozen_string_literal: true

desc "Purge the CDN cache"
task "cache:purge" do
  require "cache"

  Cache.purge
end
