#!/usr/bin/env bash
# Commit only the given paths as lineoffligbot, retrying while another agent holds the index lock.
#
#   go/scripts/commit.sh "Port BCV adapter" go/internal/adapters/bcv
#
# Paths are relative to the repository root. Exits 0 when the paths have nothing to commit.
set -uo pipefail

if [ "$#" -lt 2 ]; then
  echo "usage: $0 <subject> <path>..." >&2
  exit 2
fi

msg="$1"
shift

git=/usr/bin/git
root="$($git rev-parse --show-toplevel)"
cd "$root" || exit 1
lock="$($git rev-parse --absolute-git-dir)/index.lock"

export GIT_AUTHOR_NAME=lineoffligbot
export GIT_AUTHOR_EMAIL=4628864+lineoffligbot@users.noreply.github.com
export GIT_COMMITTER_NAME=lineoffligbot
export GIT_COMMITTER_EMAIL=4628864+lineoffligbot@users.noreply.github.com

trailer="Claude-Session: https://claude.ai/code/session_01V4HPWwkhWB64JUHUqE1mLu"

for _ in $(seq 1 60); do
  if [ -z "$($git status --porcelain -- "$@")" ]; then
    exit 0
  fi

  if $git add -A -- "$@" &&
    $git -c gpg.format=ssh \
      -c user.signingkey=/Users/hakanensari/.ssh/id_ed25519_github_bot.pub \
      -c commit.gpgsign=true \
      commit -q -m "$msg" -m "$trailer" -- "$@"; then
    $git log -1 --format='%h %s'
    exit 0
  fi

  if [ -e "$lock" ]; then
    sleep 1
    continue
  fi

  echo "commit failed for reasons other than the index lock" >&2
  exit 1
done

echo "gave up waiting for $lock" >&2
exit 1
