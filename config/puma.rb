# frozen_string_literal: true

workers Integer(ENV.fetch("WORKER_PROCESSES", 4))
threads_count = Integer(ENV.fetch("MAX_THREADS", 5))
threads threads_count, threads_count

port Integer(ENV.fetch("PORT", 8080))
worker_timeout 10
preload_app!

before_fork do
  Sequel::DATABASES.each(&:disconnect)
end
