# frozen_string_literal: true

require "etc"
require "fileutils"

desc "Run test suite in parallel"
task :spec do
  ENV["APP_ENV"] = "test"
  workers = Integer(ENV.fetch("PARALLEL_WORKERS") { [Etc.nprocessors, 8].min })
  test_db = "db/frankfurter_test.sqlite3"

  # Ensure base test database is clean and WAL checkpointed
  if File.exist?(test_db)
    system("sqlite3", test_db, "PRAGMA wal_checkpoint(TRUNCATE);", out: File::NULL)
  end

  # Clone database for workers 2..N
  (2..workers).each do |i|
    FileUtils.cp(test_db, "db/frankfurter_test_#{i}.sqlite3")
  end

  begin
    cmd = ["bundle", "exec", "parallel_test", "spec/", "-n", workers.to_s, "--type", "test", "--serialize-stdout"]
    system({ "APP_ENV" => "test" }, *cmd) || abort("Tests failed")
  ensure
    (2..workers).each do |i|
      FileUtils.rm_f(Dir["db/frankfurter_test_#{i}.sqlite3*"])
    end
  end
end
