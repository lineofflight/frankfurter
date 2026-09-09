---
name: tech-lead
description: Use when orchestrating multiple agents across worktrees or Herdr tabs, managing PR pipelines, handling Copilot review threads, resolving merge conflicts across shared files, and migrating databases safely.
---

# Tech Lead: Swarm Orchestration & Delivery

Techniques and patterns for directing parallel AI agent swarms, managing git worktree mechanics, resolving cascading PR conflicts, interacting with GitHub review bots, and safely migrating unique-constraint databases.

## 1. Multi-Agent Swarm Orchestration in Herdr

When dispatching parallel tasks (e.g. implementing multiple providers concurrently):

### Lifecycle
1. **Isolated Worktrees**: Every agent gets its own git worktree branched from `origin/main`. Never let agents share the primary checkout.
2. **Dedicated Herdr Tab**: Launch each agent in its own tab with `HERDR_ENV=1` and an assigned name (e.g. `herdr tab create --name "386-bm"`).
3. **Continuous Monitoring**: Track agent state with `herdr agent list` and `herdr agent read <name> --source recent-unwrapped --lines 30`.
4. **Immediate Cleanup**: Once a PR is squash-merged, immediately prune the workspace:
   ```bash
   git worktree unlock .claude/worktrees/<name>
   git worktree remove --force .claude/worktrees/<name>
   git branch -D worktree-<name>
   herdr tab close <workspace_id>:<tab_id>
   ```

---

## 2. Git Worktree & Push Refspec Mechanics

### The Refspec Mismatch Pitfall
Herdr and Claude worktrees commonly create local branches with a `worktree-` prefix (e.g. `worktree-cbo`), while the remote branch is named without it (e.g. `cbo-provider`).
Running a bare `git push --force-with-lease` fails silently with git exit code 128:
```
fatal: The upstream branch of your current branch does not match the name of your current branch.
```

**Rule**: Never bare-push from a worktree. Always specify the explicit refspec:
```bash
git -C .claude/worktrees/<name> push origin HEAD:<remote_branch> --force-with-lease
```

---

## 3. Resolving Cascading PR Conflicts

When multiple PRs merge in sequence, shared files (`CHANGELOG.md`, seed files) inevitably conflict.

### CHANGELOG.md Resolution Pattern
Parallel provider PRs all insert entries under `## [Unreleased] -> ### Added`. Sequential merges produce trivial conflict markers:
```markdown
<<<<<<< HEAD
- Added Centrale Bank van Suriname (CBvS) provider. (#426)
=======
- Added Banco Central de Venezuela (BCV) provider. (#435)
>>>>>>> branch
```
Resolve by keeping both entries, rebasing onto `origin/main`, running the test suite, and force-pushing with lease:
```bash
git -C .claude/worktrees/<name> rebase origin/main
# On conflict: edit CHANGELOG.md to preserve all additions
git -C .claude/worktrees/<name> add CHANGELOG.md
git -C .claude/worktrees/<name> rebase --continue
git -C .claude/worktrees/<name> push origin HEAD:<remote_branch> --force-with-lease
```

### Stacked PR Retargeting
When PR B is stacked on branch A, and PR A merges to `main`:
1. Retarget PR B's base branch to `main`:
   ```bash
   gh pr edit <PR_B_NUMBER> --base main
   ```
2. Rebase or cherry-pick PR B's commits onto `origin/main`.
3. Force-push to PR B's branch. If PR B was closed due to an empty diff during rebasing, reopen it:
   ```bash
   gh pr reopen <PR_B_NUMBER>
   ```

---

## 4. Code Review Automation & Copilot Nuances

### Bot Review Status Pitfall
GitHub Copilot does **not** flip its review decision from `CHANGES_REQUESTED` to `APPROVED` automatically after review comments are addressed or resolved. A naive merge script waiting for `reviewDecision == "APPROVED"` will deadlock.

### Resolution Protocol
1. **Query Unresolved Threads**: Inspect review threads via GraphQL rather than overall review decision:
   ```graphql
   query($owner: String!, $repo: String!, $pr: Int!) {
     repository(owner: $owner, name: $repo) {
       pullRequest(number: $pr) {
         reviewThreads(first: 20) {
           nodes {
             id
             isResolved
             comments(first: 5) {
               nodes { author { login } body path line }
             }
           }
         }
       }
     }
   }
   ```
2. **Reply & Resolve**: If a comment requires changes, make the commit and resolve the thread. If a comment is informational or already satisfied, post an explanatory reply and resolve the thread:
   ```graphql
   mutation($threadId: ID!, $body: String!) {
     addPullRequestReviewThreadReply(input: {pullRequestReviewThreadId: $threadId, body: $body}) {
       comment { id }
     }
     resolveReviewThread(input: {threadId: $threadId}) {
       thread { isResolved }
     }
   }
   ```
3. **Merge Gating**: A PR is safe to squash-merge when:
   - All `reviewThreads` have `isResolved: true`.
   - CI `test` check reports `SUCCESS`.
   - `mergeable` reports `MERGEABLE`.

---

## 5. Production Database Migrations & Materialized Tables

### Materialized Table Unique Constraints
Tables like `blended_rates` have a unique primary key `(quote, date)`.
When relabelling a currency (e.g. updating old-currency rows to a new ISO code):
- Never assume a single provider was the sole contributor historically. Another provider may have already quoted the target currency, meaning `(new_code, date)` already exists in `blended_rates`.
- An unconditional `UPDATE blended_rates SET quote = 'NEW' WHERE quote = 'OLD'` will fail with `SQLite3::ConstraintException: UNIQUE constraint failed`.
- **Safe pattern**: Exclude existing rows, update non-conflicting rows, delete redundant old rows, and recompute the blend:
  ```ruby
  existing = from(:blended_rates).where(quote: new_code).select_map(:date)
  from(:blended_rates)
    .where(quote: old_code)
    .where { date < cutover }
    .exclude(date: existing)
    .update(quote: new_code)
  from(:blended_rates).where(quote: old_code).where { date < cutover }.delete
  BlendedRate.refresh(cutover..window_end)
  ```

### Verification Before Release
Before merging migrations that modify rates or blend history:
1. Run migrations against a local copy or test database to confirm zero errors and correct schema state:
   ```bash
   APP_ENV=test bundle exec rake db:migrate
   ```
2. Verify table integrity, unique constraint adherence, and parity:
   ```bash
   APP_ENV=test bundle exec rake blend:parity
   ```

