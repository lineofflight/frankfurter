#!/usr/bin/env bash
# Live-data smoke parity: serve a copy of a real database from the Ruby app and the Go binary side by side, send both
# the requests in smoke_corpus.txt, and compare the answers (go/scripts/smokeparity). Go's v2 answers are also
# validated against lib/public/v2/openapi.json.
#
#   go/scripts/smoke_parity.sh [snapshot.sqlite3]
#
# The snapshot (default db/frankfurter.sqlite3) is only read: the servers run against a copy in SCRATCH (default a new
# temporary directory), which `rake db:setup` first brings to the latest migration and seeds, as a deploy would. The
# copy is deleted on exit. Both servers run with TZ=UTC, as production does: Ruby evaluates publish schedules in the
# process's local time zone.
set -euo pipefail

go_dir=$(cd "$(dirname "$0")/.." && pwd)
root=$(cd "$go_dir/.." && pwd)
snapshot=${1:-$root/db/frankfurter.sqlite3}
scratch=${SCRATCH:-$(mktemp -d)}
ruby_port=${RUBY_PORT:-9301}
go_port=${GO_PORT:-9302}
gobin=${GO:-go}
copy=$scratch/smoke_parity.sqlite3

pids=()
cleanup() {
	for pid in "${pids[@]}"; do kill "$pid" 2>/dev/null || true; done
	wait 2>/dev/null || true
	rm -f "$copy" "$copy-wal" "$copy-shm" "$scratch/frankfurter-smoke"
}
trap cleanup EXIT

echo "copying $snapshot"
cp -c "$snapshot" "$copy" 2>/dev/null || cp "$snapshot" "$copy"

export DATABASE_URL=sqlite://$copy TZ=UTC REQUEST_TIMEOUT_SECONDS=240 MAX_THREADS=5
(cd "$root" && APP_ENV=test mise exec -- bundle exec rake db:setup)

(cd "$go_dir" && "$gobin" build -o "$scratch/frankfurter-smoke" ./cmd/frankfurter)

(cd "$root" && APP_ENV=test WORKER_PROCESSES=0 PORT=$ruby_port \
	exec mise exec -- bundle exec puma -C config/puma.rb config.ru) >"$scratch/smoke_ruby.log" 2>&1 &
pids+=($!)
PORT=$go_port "$scratch/frankfurter-smoke" serve >"$scratch/smoke_go.log" 2>&1 &
pids+=($!)

for port in "$ruby_port" "$go_port"; do
	curl -s -o /dev/null --retry 60 --retry-delay 1 --retry-connrefused "http://localhost:$port/"
done

cd "$go_dir"
"$gobin" run ./scripts/smokeparity -ruby "http://localhost:$ruby_port" -go "http://localhost:$go_port" \
	-corpus scripts/smoke_corpus.txt -openapi "$root/lib/public/v2/openapi.json"
