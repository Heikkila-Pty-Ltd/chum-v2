---
status: complete
priority: p3
issue_id: "013"
tags: [code-review, quality, simplification]
dependencies: []
---

# Reviewer Resolution Has 7 Fallback Stages — Over-Engineered

## Problem Statement

`resolveReviewerWithStage` in `review.go:332-408` implements a 7-stage reviewer resolution cascade (77 lines) for a system with 2-3 providers. A simpler 3-stage approach (explicit config → cross-provider fallback → self-review) would cover all real scenarios.

## Acceptance Criteria

- [ ] Reviewer resolution simplified to 3 stages max
- [ ] All existing review_test.go tests still pass
- [ ] Debug `stage` logging retained for observability
