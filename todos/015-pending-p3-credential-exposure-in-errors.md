---
status: pending
priority: p3
issue_id: "015"
tags: [code-review, security, logging]
dependencies: []
---

# Credentials and Secrets May Leak via Error Messages

## Problem Statement

`runCommand` in `review.go:701-709` includes raw command output in error messages. LLM agent output (up to 500 chars) is included in errors at `activities.go:151`. These propagate through Temporal workflow history and logs. Agent output could contain secrets read from the filesystem. GitHub API responses may contain auth-bearing URLs.

## Proposed Solutions

Sanitize command output before including in error messages. Strip known credential patterns (tokens, keys, passwords). Limit exposed output to safe metadata.

**Effort:** Medium | **Risk:** Low

## Acceptance Criteria

- [ ] Error messages do not contain raw command output verbatim
- [ ] Known credential patterns stripped from error strings
- [ ] Agent output in errors sanitized or truncated to non-sensitive portions

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-12 | Identified during security review | Raw gh output and agent output in error messages |
| 2026-03-12 | Skipped during triage (low risk, internal) | Tracker restored after review flagged deletion |
