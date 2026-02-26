# PR: Robustness Test Plan for `chum-m1l` (CHUM v2)

## Goal

Add a focused robustness suite around the new Temporal spine so failures are classified correctly, workflows do not deadlock, and unsafe false-positive success states are prevented.

This PR proposes tests first (no behavior changes yet), targeting the highest-risk execution paths.

## Proposed test files

1. `internal/engine/cli_test.go`
2. `internal/engine/parse_test.go`
3. `internal/engine/activities_test.go`
4. `internal/engine/agent_workflow_test.go`
5. `internal/engine/dispatcher_workflow_test.go`
6. `internal/git/git_test.go`

## Test matrix

## 1) CLI robustness (`internal/engine/cli.go`)

1. `TestIsRateLimited_PatternMatching`
- Verifies known rate-limit phrases are detected case-insensitively.

2. `TestRunCLI_PlanMode_UsesPlanCommandShape`
- Asserts plan mode uses the expected command flags per agent family.

3. `TestRunCLIExec_ExecMode_UsesExecCommandShape`
- Asserts execute mode uses file-modifying command shape (no plan-only flags).

4. `TestRunWithPrompt_WritesPromptViaStdinNotArgs`
- Verifies prompt is piped through stdin pathway and not interpolated into argv.

5. `TestRunWithPrompt_ReturnsErrRateLimitedEvenOnNonZeroExit`
- Ensures rate-limit classification is preserved as `ErrRateLimited` with output context.

6. `TestBuildPlanCommand_UnknownAgentFallback`
- Covers unknown agent fallback path.

7. `TestBuildExecCommand_UnknownAgentFallback`
- Covers unknown agent fallback path.

## 2) JSON extraction/repair robustness (`internal/engine/parse.go`)

1. `TestExtractJSON_RawObject`
2. `TestExtractJSON_CodeFence_JSON`
3. `TestExtractJSON_CodeFence_NoLang`
4. `TestExtractJSON_ClaudeEnvelope_TextBlocks`
5. `TestExtractJSON_WithCommentaryPrefixSuffix`
6. `TestExtractJSON_BalancedBracesInsideString`
7. `TestExtractJSON_RepairTrailingComma`
8. `TestExtractJSON_RepairSingleQuotesWhenValid`
9. `TestExtractJSON_NoObject_ReturnsEmpty`

10. `TestNormalizePlan_FillsSummaryFromSteps`
11. `TestNormalizePlan_FillsSummaryFromPromptWhenMissing`
12. `TestNormalizePlan_SynthesizesStepsWhenMissing`
13. `TestNormalizePlan_DeduplicatesFilesAndTrimsWhitespace`

## 3) Activity preflight robustness (`internal/engine/activities.go`)

1. `TestExecuteActivity_PreflightFailsWhenCLIMissing`
- Must return clear preflight error before long execution window.

2. `TestExecuteActivity_PreflightFailsWhenGitMetadataMissing`
- Simulates missing `.git` in worktree.

3. `TestExecuteActivity_PreflightFailsWhenBaselineBuildFails`
- First DoD check failure blocks execution.

4. `TestExecuteActivity_CommitAllNoChanges_DoesNotHardFail`
- No-change path is surfaced but does not crash activity.

5. `TestDoDCheckActivity_ProjectNotFound`
- Correct error classification for missing project config.

6. `TestDoDCheckActivity_DefaultChecksWhenEmpty`
- Falls back to default check set when project checks unset.

## 4) Core workflow robustness (`internal/engine/agent.go`)

Use Temporal test suite and mock activities to cover branch behavior deterministically.

1. `TestAgentWorkflow_PlanFailure_ClosesPlanFailedAndCleans`
2. `TestAgentWorkflow_ExecuteFailure_ClosesExecFailedAndCleans`
3. `TestAgentWorkflow_DoDError_ClosesDodErrorAndCleans`
4. `TestAgentWorkflow_DoDFail_ClosesDodFailedAndCleans`
5. `TestAgentWorkflow_Success_PushPrCloseAndCleanup`
6. `TestAgentWorkflow_WorktreeSetupFailure_FallsBackToSharedWorkspace`

## 5) Dispatcher workflow robustness (`internal/engine/dispatcher.go`)

1. `TestDispatcherWorkflow_NoCandidates_NoDispatch`
2. `TestDispatcherWorkflow_MarkTaskRunningFailure_SkipsCandidate`
3. `TestDispatcherWorkflow_StartChildFailure_ContinuesNextCandidate`
4. `TestDispatcherWorkflow_WaitsForChildStartBeforeCompleting`
5. `TestDispatcherWorkflow_MapsCandidateToTaskRequestCorrectly`

## 6) Git robustness (`internal/git/git.go`)

1. `TestRunDoDChecks_FailsWhenGitDirMissing`
2. `TestRunDoDChecks_FailsWhenNoChanges`
3. `TestRunDoDChecks_FailsWhenNpmCheckAndPackageJSONMissing`
4. `TestRunDoDChecks_CollectsPerCheckExitCodeAndFailureList`
5. `TestHasChanges_TrueOnUncommittedDiff`
6. `TestHasChanges_TrueOnCommittedDiffAgainstFallbackBase`
7. `TestHasChanges_FalseWhenNoDiff`
8. `TestSetupWorktree_UsesHooksBypassAndConfiguresHooksPath`
9. `TestCommitAll_NoStagedChanges_ReturnsFalseNil`

## 7) Smoke integration (optional, build tag e.g. `integration`)

1. `TestWorkflowE2E_LocalTempGitRepo_HappyPath`
- Creates temporary git repo, simple task, and validates complete workflow progression.

2. `TestWorkflowE2E_DoDFailure_ProducesExpectedTerminalStatus`
- Ensures terminal failure status is deterministic and no orphaned worktree remains.

## Sequencing recommendation

1. Land parser + CLI tests first (lowest mocking overhead).
2. Land git package tests second.
3. Land Temporal workflow tests third.
4. Add optional integration tests last.

## Acceptance criteria for this testing PR

1. New tests cover all critical failure branches in `agent`, `dispatcher`, and DoD preflights.
2. At least one test exists for every major fallback/repair path in `ExtractJSON`.
3. Rate-limit classification is explicitly tested.
4. Workflow tests verify cleanup behavior on every terminal branch.
