# frozen_string_literal: true

module Scheduler
  class Daemon
    attr_reader :pid

    class << self
      def start
        new.start
      end
    end

    def initialize
      @parent_pid = Process.pid
    end

    def start
      return if pid

      @pid = fork do
        monitor_parent
        run
      end
      monitor_child
    end

    private

    def run
      Sequel::DATABASES.each(&:disconnect)
      load("bin/schedule")
    end

    def monitor_child
      return if @child_monitor

      @child_monitor = Thread.new do
        loop do
          sleep(5)
          unless alive?(pid)
            @pid = nil
            start
          end
        end
      end
    end

    def monitor_parent
      Thread.new do
        loop do
          exit unless alive?(@parent_pid)
          sleep(1)
        end
      end
    end

    def alive?(pid)
      Process.getpgid(pid)
      true
    rescue Errno::ESRCH
      false
    end
  end
end
