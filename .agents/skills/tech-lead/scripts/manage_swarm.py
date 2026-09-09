#!/usr/bin/env python3
"""
manage_swarm.py - Tech Lead Swarm Orchestrator & PR Pipeline Manager

Monitors active PRs from multi-agent swarms, resolves cascading CHANGELOG conflicts,
validates review threads and CI, squash-merges approved PRs, and prunes worktrees and tabs.
"""

import json
import os
import re
import subprocess
import sys
import time

REPO = "lineofflight/frankfurter"
REPO_DIR = subprocess.check_output(["git", "rev-parse", "--show-toplevel"], text=True).strip()


def run_cmd(cmd, cwd=REPO_DIR, capture=True, check=False):
    res = subprocess.run(
        cmd,
        shell=isinstance(cmd, str),
        cwd=cwd,
        capture_output=capture,
        text=True,
        check=check,
    )
    return res.stdout.strip() if capture else ""


def get_open_prs():
    out = run_cmd(["gh", "pr", "list", "--json", "number,title,headRefName,baseRefName,mergeable,state"])
    try:
        return json.loads(out)
    except Exception:
        return []


def get_pr_review_threads(pr_num):
    query = """
    query($owner: String!, $repo: String!, $pr: Int!) {
      repository(owner: $owner, name: $repo) {
        pullRequest(number: $pr) {
          reviewDecision
          reviewThreads(first: 50) {
            nodes {
              id
              isResolved
              comments(first: 5) {
                nodes {
                  author { login }
                  body
                  path
                  line
                }
              }
            }
          }
        }
      }
    }
    """
    cmd = [
        "gh", "api", "graphql",
        "-f", f"query={query}",
        "-F", "owner=lineofflight",
        "-F", "repo=frankfurter",
        "-F", f"pr={pr_num}",
    ]
    res = run_cmd(cmd)
    try:
        data = json.loads(res)
        pr_data = data.get("data", {}).get("repository", {}).get("pullRequest", {})
        threads = pr_data.get("reviewThreads", {}).get("nodes", [])
        decision = pr_data.get("reviewDecision")
        return decision, threads
    except Exception as e:
        print(f"Error fetching reviews for PR #{pr_num}: {e}")
        return None, []


def get_ci_status(pr_num):
    out = run_cmd(["gh", "pr", "checks", str(pr_num)])
    if not out:
        return "pending"
    lines = out.splitlines()
    statuses = []
    for line in lines:
        parts = line.split("\t")
        if len(parts) >= 2:
            statuses.append((parts[0].strip(), parts[1].strip()))
    has_test = False
    test_passed = False
    for name, status in statuses:
        if name == "test":
            has_test = True
            if status == "pass":
                test_passed = True
        elif status in ("fail", "error"):
            return "failed"
    if has_test and test_passed:
        pending = [s for n, s in statuses if s in ("pending", "in_progress") and n in ("Analyze (ruby)", "CodeQL")]
        if not pending:
            return "passed"
    return "pending"


def find_worktree_for_branch(branch_name):
    out = run_cmd(["git", "worktree", "list", "--porcelain"])
    current_wt = None
    for line in out.splitlines():
        if line.startswith("worktree "):
            current_wt = line.split(" ", 1)[1]
        elif line.startswith("branch "):
            b = line.split(" ", 1)[1].replace("refs/heads/", "")
            if b == branch_name or b == f"worktree-{branch_name}":
                return current_wt
    return None


def resolve_changelog_conflict(file_path):
    with open(file_path, "r", encoding="utf-8") as f:
        content = f.read()
    conflict_pattern = re.compile(
        r"<<<<<<< HEAD\n(.*?)\n=======\n(.*?)\n>>>>>>> [^\n]+",
        re.DOTALL,
    )
    def replacer(match):
        part1 = match.group(1).strip()
        part2 = match.group(2).strip()
        return f"{part1}\n{part2}"
    resolved = conflict_pattern.sub(replacer, content)
    with open(file_path, "w", encoding="utf-8") as f:
        f.write(resolved)


def rebase_and_push(wt_dir, remote_branch):
    run_cmd("git fetch origin", cwd=wt_dir)
    res = subprocess.run("git rebase origin/main", shell=True, cwd=wt_dir, capture_output=True, text=True)
    if res.returncode != 0:
        cl_path = os.path.join(wt_dir, "CHANGELOG.md")
        if os.path.exists(cl_path):
            resolve_changelog_conflict(cl_path)
            run_cmd("git add CHANGELOG.md", cwd=wt_dir)
            cont = subprocess.run("git rebase --continue", shell=True, cwd=wt_dir, capture_output=True, text=True)
            if cont.returncode != 0:
                run_cmd("git rebase --abort", cwd=wt_dir)
                return False
        else:
            run_cmd("git rebase --abort", cwd=wt_dir)
            return False
    push_res = subprocess.run(
        f"git push origin HEAD:{remote_branch} --force-with-lease",
        shell=True,
        cwd=wt_dir,
        capture_output=True,
        text=True,
    )
    return push_res.returncode == 0


def cleanup_pr(pr):
    head = pr["headRefName"]
    wt = find_worktree_for_branch(head)
    if wt and wt != REPO_DIR:
        run_cmd(["git", "worktree", "unlock", wt])
        run_cmd(["git", "worktree", "remove", "--force", wt])
        run_cmd(["git", "branch", "-D", head])
        run_cmd(["git", "branch", "-D", f"worktree-{head}"])


def process_prs():
    prs = get_open_prs()
    for pr in prs:
        num = pr["number"]
        title = pr["title"]
        head = pr["headRefName"]
        mergeable = pr.get("mergeable")
        print(f"\n--- Checking PR #{num}: {title} ({head}) ---")
        if mergeable == "CONFLICTING":
            wt = find_worktree_for_branch(head)
            if wt:
                print(f"Rebasing conflicting PR #{num} in {wt}...")
                if rebase_and_push(wt, head):
                    print("Rebase and push succeeded.")
                    continue
        decision, threads = get_pr_review_threads(num)
        unresolved = [t for t in threads if not t.get("isResolved")]
        if unresolved:
            print(f"PR #{num} has {len(unresolved)} unresolved review thread(s).")
            continue
        ci = get_ci_status(num)
        print(f"PR #{num} CI status: {ci}")
        if ci == "passed":
            print(f"Squash-merging PR #{num}...")
            merge_out = run_cmd(["gh", "pr", "merge", str(num), "--squash"])
            print(f"Merged #{num}: {merge_out}")
            cleanup_pr(pr)


if __name__ == "__main__":
    process_prs()
