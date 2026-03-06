# Contributing

## Required Workflow

All changes must be made from a dedicated linked git worktree on a topic branch.

Do not:
- work from the primary checkout
- make code changes on `main` or `master`
- review or patch a PR from an unrelated checkout

Recommended start:

```bash
bash scripts/new_worktree.sh chore/example-change
cd ../_worktrees/<repo>-chore-example-change
bash scripts/hygiene_check.sh
```

## Hygiene Gate

Run this before edits and again before pushing:

```bash
bash scripts/hygiene_check.sh
```

The check fails when:
- you are on `main` or `master`
- you are working from the primary checkout instead of a linked worktree

## Validation

Before pushing:
- run the narrowest relevant test/build command for the change
- run `bash scripts/hygiene_check.sh`
- inspect `git diff --stat`
- confirm the branch matches the task you are actually doing

## Pull Requests

Every PR should include:
- what changed
- why it changed
- how it was validated
- logic risks that were checked explicitly

## Review Guard

Run these before pushing:

```bash
bash scripts/review_guard.sh
bash scripts/validate_repo.sh
```

`review_guard.sh` fails high-risk code changes that do not come with matching test changes.
