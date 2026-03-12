---
status: pending
priority: p1
issue_id: "001"
tags: [code-review, security, review-pipeline]
dependencies: []
---

# Review Signal Parsing Bypass — APPROVE Matched from Echoed Prompt

## Problem Statement

`parseReviewSignal()` in `review.go:477` scans ALL lines of reviewer output for "APPROVE" or "REQUEST_CHANGES" and returns the FIRST match. The review prompt itself (`buildReviewPrompt`, line 462) contains the literal text "APPROVE" as an instruction. When the reviewer LLM echoes the prompt (which codex does), the parser matches "APPROVE" from the instructions before finding the actual "REQUEST_CHANGES" verdict.

**Confirmed in production:** PR #71 for task `chum-ues.1.2` — reviewer said REQUEST_CHANGES but was parsed as APPROVE. Combined with the self-review COMMENT fallback and admin merge, this created a complete trust chain bypass.

## Findings

- `parseReviewSignal` iterates lines 0..N and returns on first signal match (`review.go:480-500`)
- `buildReviewPrompt` contains literal "APPROVE" and "REQUEST_CHANGES" in instructions (`review.go:462-464`)
- Codex reviewer echoed the full prompt + its analysis, causing "APPROVE" to match on the instruction line
- The review was submitted as COMMENT with approve fallback marker
- `reviewStateWithBodyToOutcome` saw the approve marker → `ReviewApproved`
- Merge attempted with `--admin` → blocked by branch protection → `needs_review` with `merge_blocked`

## Proposed Solutions

### Option A: First non-blank line only (Recommended)
Only check the very first non-blank, non-whitespace line for the signal. This is what the prompt asks for ("Line 1 must be exactly one of").

**Pros:** Simple, matches prompt contract, eliminates false positives from echoed content
**Cons:** If an LLM outputs a preamble before the signal, it will be treated as invalid (falls back to REQUEST_CHANGES, which is fail-safe)
**Effort:** Small
**Risk:** Low

### Option B: Structured signal envelope
Change the prompt to require `SIGNAL:APPROVE` or `SIGNAL:REQUEST_CHANGES` and only match that prefix. Don't include bare keywords in instructions.

**Pros:** Unambiguous, no false positives possible
**Cons:** Requires updating prompt + parser + tests; existing workflow replays need version gate
**Effort:** Medium
**Risk:** Medium (version gate needed for Temporal replay)

### Option C: Scan from end instead of beginning
Reverse the scan order — last match wins, since the actual verdict typically appears at the bottom after any echoed instructions.

**Pros:** Works even if LLM echoes prompt
**Cons:** Fragile — LLM could also echo at the end; less principled than Option A
**Effort:** Small
**Risk:** Medium

## Recommended Action

Option A — anchor to first non-blank line only.

## Technical Details

**Affected files:**
- `internal/engine/review.go` — `parseReviewSignal()` (lines 477-508), `buildReviewPrompt()` (lines 455-475)
- `internal/engine/review_test.go` — test coverage for echoed prompt scenarios

## Acceptance Criteria

- [ ] `parseReviewSignal` only checks the first non-blank line for signal
- [ ] Test case: output with echoed prompt containing "APPROVE" before actual "REQUEST_CHANGES" → returns REQUEST_CHANGES
- [ ] Test case: output with "APPROVE" on first line → returns APPROVE
- [ ] Test case: output with preamble before signal → falls back to REQUEST_CHANGES (fail-safe)
- [ ] Existing review_test.go tests still pass

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-12 | Discovered via PR #71 investigation | Codex echoes full prompt; first-match scan is fundamentally broken |

## Resources

- PR #71: https://github.com/Heikkila-Pty-Ltd/chum-v2/pull/71
- `review.go:477-508` — parseReviewSignal
- `review.go:455-475` — buildReviewPrompt
