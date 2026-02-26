# PR Plan: Reviewer as Actor, CHUM as Arbiter

## Goal
Introduce a deterministic code-review stage where:
- The reviewer model provides review intent and feedback in print mode only.
- CHUM owns all GitHub side effects (submit review, merge PR, close task).
- GitHub discrete state is the source of truth for task transitions.

This keeps decision and actuation separated, avoids dangerous reviewer exec permissions, and preserves auditable state transitions.

## Access Model

| Actor | Mode | Can modify files | Can use `gh` directly | Responsibility |
|---|---|---|---|---|
| Execute agent | `RunCLIExec` | Yes | Not required | Implement code changes |
| Reviewer agent | `RunCLI` (print) | No | No | Produce review signal + rationale |
| CHUM activities | Go activities | N/A | Yes | Submit review, read PR state, merge |

## End-to-End Flow
1. `SetupWorktree`
2. `Execute` (`RunCLIExec`)
3. `DoD`
4. `CommitAll` + `Push`
5. `CreatePR` -> capture `prNumber`, `headSHA`
6. Review loop (`maxRounds = 2`)
- `RunReviewActivity`
- `SubmitReviewActivity`
- `CheckPRStateActivity` (scoped by reviewer login + head SHA + round tag)
- If approved -> `MergePRActivity`
- If changes requested -> `ReadReviewFeedbackActivity` -> re-execute -> re-DoD -> push
- If no activity/failure -> terminal `needs_review`
7. `NotifyActivity` (Matrix)
8. `CloseTaskActivity` with structured detail
9. `CleanupWorktree`

## Key Design Rules

### 1. Reviewer runs in print mode
Reviewer must not execute commands or modify files. `RunReviewActivity` composes input and calls `RunCLI` only.

### 2. Diff is precomputed by CHUM
Because reviewer has no CLI access, `RunReviewActivity` computes review input first:
- primary: `gh pr diff <pr>`
- fallback: local git diff for PR range
- large diff safeguards: cap bytes/files, chunk when needed, prepend deterministic summary

### 3. Strict review signal contract
First non-empty output line from reviewer must be exactly one of:
- `APPROVE`
- `REQUEST_CHANGES`

Parser behavior:
- normalize with trim + uppercase
- unknown/missing signal defaults to `REQUEST_CHANGES`
- set subreason `invalid_review_signal`

### 4. CHUM submits reviews with round tags
`SubmitReviewActivity` performs `gh pr review` and always appends:
- `<!-- chum-round:N -->`

Tagging applies to both approve and request-changes submissions.

### 5. State checks are scoped and deterministic
`CheckPRStateActivity` reads reviews and only considers entries matching all:
- reviewer GitHub login (`gh api user --jq .login`)
- `commit_id == headSHA`
- body contains `chum-round:N`
- state not dismissed

Decision uses latest matching review only.

### 6. Post-submit consistency polling
After review submission, CHUM polls state briefly before classifying `no_review_activity`:
- e.g. 3 attempts with small backoff

### 7. Mergeability mapping is explicit
`MergePRActivity` maps `mergeStateStatus` without fallthrough:
- `CLEAN` -> merge (`gh pr merge --squash --delete-branch`)
- `BLOCKED`, `BEHIND`, `DIRTY`, `DRAFT`, `UNKNOWN`, `UNSTABLE`, `HAS_HOOKS` -> terminal `needs_review` with specific subreason

For pending checks under blocked state:
- optional short wait/poll window
- on timeout -> `checks_pending_timeout`

### 8. Idempotent reruns
If a matching round-tagged review already exists for current reviewer/head SHA/round, CHUM skips re-submission and continues state evaluation.

### 9. Safety guard on workspace cleanliness
Run `git status --porcelain` before/after reviewer phase; non-empty indicates unexpected modifications and closes task as `needs_review` with `reviewer_modified_code`.

## Types to Add (`types.go`)

```go
type ReviewOutcome string

const (
    ReviewApproved         ReviewOutcome = "approved"
    ReviewChangesRequested ReviewOutcome = "changes_requested"
    ReviewNoActivity       ReviewOutcome = "no_review_activity"
    ReviewerFailed         ReviewOutcome = "reviewer_failed"
)

type ReviewResult struct {
    Outcome   ReviewOutcome
    Reason    string
    ReviewURL string
    Comments  string
}

type CloseReason string

const (
    CloseCompleted   CloseReason = "completed"
    CloseDoDFailed   CloseReason = "dod_failed"
    CloseNeedsReview CloseReason = "needs_review"
)

type CloseDetail struct {
    Reason    CloseReason
    SubReason string
    ReviewURL string
    PRNumber  int
}
```

## Subreason Vocabulary
Use stable machine-readable subreasons in `CloseDetail.SubReason`:
- `max_rounds_reached`
- `reviewer_error`
- `review_submit_failed`
- `invalid_review_signal`
- `no_reviewer_activity`
- `reviewer_modified_code`
- `merge_blocked`
- `checks_pending_timeout`
- `merge_failed`

## Files and Changes

### New: `internal/engine/review.go`
Add:
- `RunReviewActivity(workDir string, prNumber int, round int, execAgent string) (signal string, body string, err error)`
- `SubmitReviewActivity(workDir string, prNumber int, round int, signal string, body string) error`
- `CheckPRStateActivity(workDir string, prNumber int, round int, reviewerLogin string, headSHA string) (ReviewResult, error)`
- `ReadReviewFeedbackActivity(workDir string, prNumber int, reviewID int64) (string, error)`
- `MergePRActivity(workDir string, prNumber int) error`
- `GuardReviewerCleanActivity(workDir string) error`
- `DefaultReviewer(agent string) string`
- `ResolveReviewerLoginActivity(workDir string) (string, error)`

### New: `internal/engine/notify.go`
Add:
- `NotifyActivity(message string) error` (Matrix webhook/client-server delivery)

### Modify: `internal/engine/agent.go`
- Insert review loop after PR creation.
- Enforce max two rounds.
- Re-execute only on `changes_requested`.
- Merge only through `MergePRActivity` after approved state.
- Close with `CloseDetail` on every terminal path.

### Modify: `internal/engine/worker.go`
- Register all review/merge/notify activities.

### Modify: `internal/engine/types.go`
- Add `ReviewOutcome`, `ReviewResult`, `CloseReason`, `CloseDetail`.

### Modify: `chum.toml`
- Add reviewer provider mapping.
- Add Matrix notification config fields.

## Test Plan

### Unit tests (`internal/engine/review_test.go`)
- `TestDefaultReviewer`
- `TestRunReviewActivity_DiffInjectedWithoutExec`
- `TestParseReviewSignal_ExactApprove`
- `TestParseReviewSignal_ExactRequestChanges`
- `TestParseReviewSignal_InvalidDefaultsToRequestChanges`
- `TestSubmitReviewActivity_AppendsRoundTag`
- `TestCheckPRState_ScopedByLoginSHAAndRound`
- `TestCheckPRState_DismissedIgnored`
- `TestCheckPRState_EventualConsistencyRetry`
- `TestGuardClean_StatusPorcelain`
- `TestMergePRActivity_StateMappingAllKnownStatuses`
- `TestMergePRActivity_ChecksPendingTimeout`
- `TestReviewRound_IdempotentWhenTagAlreadyExists`

### Workflow tests (`internal/engine/agent_test.go`)
- `TestWorkflow_ReviewApproved`
- `TestWorkflow_RejectedThenApproved`
- `TestWorkflow_MaxRoundsReached`
- `TestWorkflow_ReviewerError`
- `TestWorkflow_ReviewSubmitFailed`
- `TestWorkflow_InvalidReviewSignal`
- `TestWorkflow_ReviewerDirtyWorktree`
- `TestWorkflow_ApprovedMergeBlocked`
- `TestWorkflow_ApprovedMergeFails`
- `TestWorkflow_NoReviewActivity`
- `TestWorkflow_ChecksPendingTimeout`

## Rollout Notes
- Land activities and types first with tests.
- Gate workflow wiring behind a feature flag if needed.
- Enable notification config as optional non-blocking behavior.
- Keep telemetry/audit logs for every review/merge decision.
