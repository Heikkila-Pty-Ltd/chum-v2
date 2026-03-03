package engine

import "strings"

// FailureCategory is a machine-readable classification of a DoD failure.
// Used for structured logging, telemetry, and retry/escalation decisions.
type FailureCategory string

const (
	CategoryTestFailure     FailureCategory = "test_failure"
	CategoryCompileError    FailureCategory = "compile_error"
	CategoryLintError       FailureCategory = "lint_error"
	CategoryLintConfigError FailureCategory = "lint_config_error"
	CategoryTimeout         FailureCategory = "timeout"
	CategoryActivityTimeout FailureCategory = "activity_timeout"
	CategoryMergeConflict   FailureCategory = "merge_conflict"
	CategoryScopeDrift      FailureCategory = "scope_drift"
	CategoryExecutionError  FailureCategory = "execution_error"
	CategoryInfraFailure    FailureCategory = "infrastructure_failure"
	CategoryDoDCheckFailed  FailureCategory = "dod_check_failed"
)

// ClassifyFailure categorizes a DoD failure string into a machine-readable
// category and a one-line summary.
//
// Categories (in priority order):
//   - infrastructure_failure — environmental, not the agent's fault
//   - activity_timeout       — Temporal activity timeout
//   - compile_error          — build doesn't compile
//   - test_failure           — test suite failures
//   - lint_config_error      — golangci-lint config/runtime error (exit 3)
//   - lint_error             — linter or static analysis failure
//   - timeout                — generic execution timeout
//   - merge_conflict         — git merge/rebase conflict
//   - scope_drift            — out-of-scope changes detected
//   - execution_error        — agent CLI crashed or was killed
//   - dod_check_failed       — generic catch-all
func ClassifyFailure(failures string) (FailureCategory, string) {
	lower := strings.ToLower(failures)

	// Infrastructure failures first — NOT the agent's fault.
	if IsInfrastructureFailure(failures) {
		return CategoryInfraFailure, ExtractInfraReason(failures)
	}

	switch {
	// Temporal activity timeouts — must be before generic timeout check.
	case strings.Contains(lower, "heartbeat timeout") ||
		strings.Contains(lower, "starttoclose timeout") ||
		strings.Contains(lower, "activity heartbeat timeout") ||
		strings.Contains(lower, "activity starttoclose timeout"):
		return CategoryActivityTimeout, ExtractFirstLine(failures)

	// Compile errors — code doesn't build.
	case strings.Contains(lower, "undefined:") ||
		strings.Contains(lower, "cannot use") ||
		strings.Contains(lower, "syntax error") ||
		strings.Contains(lower, "does not compile") ||
		strings.Contains(lower, "build failed") ||
		strings.Contains(lower, "compilation failed"):
		return CategoryCompileError, ExtractFirstLine(failures)

	// Triple DoD fail (test+vet+lint) = compile error.
	case strings.Contains(lower, "go test") &&
		strings.Contains(lower, "go vet") &&
		strings.Contains(lower, "golangci-lint"):
		return CategoryCompileError, "Triple DoD fail (test+vet+lint) — code does not compile"

	// Test failures.
	case strings.Contains(lower, "fail") &&
		(strings.Contains(lower, "test") || strings.Contains(lower, "--- fail")):
		return CategoryTestFailure, ExtractFirstLine(failures)

	// golangci-lint exit 3 = config/runtime error (not lint failure which is exit 1).
	case strings.Contains(lower, "golangci-lint") && strings.Contains(lower, "exit 3"):
		return CategoryLintConfigError, "golangci-lint exit 3: config/runtime error (not lint failure)"

	// Lint errors — explicit parentheses to avoid operator-precedence confusion.
	case strings.Contains(lower, "golangci-lint") ||
		strings.Contains(lower, "eslint") ||
		(strings.Contains(lower, "lint") && strings.Contains(lower, "error")):
		return CategoryLintError, ExtractFirstLine(failures)

	// Timeouts.
	case strings.Contains(lower, "timeout") ||
		(strings.Contains(lower, "exceeded") && strings.Contains(lower, "time")):
		return CategoryTimeout, ExtractFirstLine(failures)

	// Merge conflicts.
	case strings.Contains(lower, "merge conflict") ||
		strings.Contains(lower, "conflict"):
		return CategoryMergeConflict, ExtractFirstLine(failures)

	// Scope drift.
	case strings.Contains(lower, "scope") ||
		strings.Contains(lower, "out-of-scope") ||
		strings.Contains(lower, "drift"):
		return CategoryScopeDrift, ExtractFirstLine(failures)

	// Execute error (agent CLI crashed or was killed).
	case strings.Contains(lower, "execute error") ||
		strings.Contains(lower, "executeactivity"):
		return CategoryExecutionError, ExtractFirstLine(failures)

	default:
		if failures != "" {
			return CategoryDoDCheckFailed, ExtractFirstLine(failures)
		}
		return "", ""
	}
}

// infraPatterns are substrings that indicate environmental/infrastructure
// failures that are NOT caused by the agent's code changes.
var infraPatterns = []string{
	"parallel golangci-lint is running",
	"golangci-lint exit 3",  // config error
	"golangci-lint exit -1", // signal kill (OOM)
	"semgrep exit 7",        // config/download error
	"exit -1",               // any tool killed by signal (OOM, SIGKILL)
	"error obtaining vcs status",
	"command not found",
	"no such file or directory",
	"permission denied",
	"no space left on device",
	"disk quota exceeded",
	"git lock",
	"index.lock",
	"unable to create",
	"fatal: unable to access",
	"overloaded", // LLM provider overloaded
}

// IsInfrastructureFailure returns true if the failure is environmental,
// not caused by the agent's code changes. These failures should NOT burn
// a retry attempt or be fed back as agent guidance.
func IsInfrastructureFailure(failures string) bool {
	lower := strings.ToLower(failures)
	for _, p := range infraPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// transientPatterns are infra failures that are likely temporary and worth
// one retry (e.g., lock contention). Persistent issues (disk full, missing
// tools) need human intervention and should not be retried.
var transientPatterns = []string{
	"parallel golangci-lint is running",
	"git lock",
	"index.lock",
	"unable to create",
}

// IsTransientInfraFailure returns true if the infra failure is likely
// temporary and worth one retry. Returns false for persistent issues
// (disk full, missing tools) that need human intervention.
func IsTransientInfraFailure(failures string) bool {
	lower := strings.ToLower(failures)
	for _, p := range transientPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// ExtractInfraReason returns a human-readable reason for the infrastructure failure.
func ExtractInfraReason(failures string) string {
	lower := strings.ToLower(failures)
	switch {
	case strings.Contains(lower, "parallel golangci-lint"):
		return "golangci-lint parallel lock (another instance running)"
	case strings.Contains(lower, "golangci-lint") && strings.Contains(lower, "exit 3"):
		return "golangci-lint config/runtime error (exit 3)"
	case strings.Contains(lower, "semgrep") && strings.Contains(lower, "exit 7"):
		return "semgrep config/download error (exit 7)"
	case strings.Contains(lower, "command not found"):
		return "required tool not found in PATH"
	case strings.Contains(lower, "no space left") || strings.Contains(lower, "disk quota"):
		return "disk full"
	case strings.Contains(lower, "git lock") || strings.Contains(lower, "index.lock"):
		return "git lock contention"
	case strings.Contains(lower, "overloaded"):
		return "LLM provider overloaded"
	default:
		return "infrastructure/environment error"
	}
}

// ExtractFirstLine returns the first non-empty line of text, truncated to 200 chars.
func ExtractFirstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			if len(line) > 200 {
				return line[:200] + "..."
			}
			return line
		}
	}
	return ""
}
