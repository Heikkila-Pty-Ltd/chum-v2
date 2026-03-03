package engine

import (
	"strings"
	"testing"
)

func TestClassifyFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		failures string
		wantCat  FailureCategory
		wantSub  string // substring of summary
	}{
		// Infrastructure (highest priority)
		{"infra_parallel_lint", "parallel golangci-lint is running", CategoryInfraFailure, "parallel lock"},
		{"infra_disk_full", "no space left on device", CategoryInfraFailure, "disk full"},
		{"infra_command_not_found", "command not found: semgrep", CategoryInfraFailure, "not found in PATH"},

		// Activity timeouts
		{"activity_heartbeat", "activity heartbeat timeout exceeded", CategoryActivityTimeout, "heartbeat timeout"},
		{"activity_starttoclose", "StartToClose timeout fired", CategoryActivityTimeout, "StartToClose"},

		// Compile errors
		{"compile_undefined", "undefined: someFunc", CategoryCompileError, "undefined: someFunc"},
		{"compile_syntax", "syntax error: unexpected }", CategoryCompileError, "syntax error"},
		{"compile_build_failed", "build failed: exit 2", CategoryCompileError, "build failed"},
		{"compile_triple_dod", "go test failed; go vet failed; golangci-lint exit 1", CategoryCompileError, "Triple DoD"},

		// Test failures
		{"test_fail", "--- FAIL: TestFoo (0.1s)", CategoryTestFailure, "FAIL: TestFoo"},
		{"test_fail_generic", "test failed with errors", CategoryTestFailure, "test failed"},

		// Lint config error — golangci-lint exit 3 is also an infra pattern so infra wins
		{"lint_config_exit3_is_infra", "golangci-lint exit 3: config error", CategoryInfraFailure, "config/runtime error"},

		// Lint errors
		{"lint_golangci", "golangci-lint found 3 issues", CategoryLintError, "golangci-lint"},
		{"lint_eslint", "eslint: 2 errors", CategoryLintError, "eslint"},
		{"lint_generic", "lint error: unused variable", CategoryLintError, "lint error"},

		// Timeouts
		{"timeout_generic", "execution timeout exceeded", CategoryTimeout, "timeout"},
		{"timeout_time_exceeded", "exceeded time limit", CategoryTimeout, "exceeded time"},

		// Merge conflicts
		{"merge_conflict", "merge conflict in main.go", CategoryMergeConflict, "merge conflict"},
		{"conflict_generic", "CONFLICT (content): Merge conflict in foo.go", CategoryMergeConflict, "CONFLICT"},

		// Scope drift
		{"scope_drift", "drift detected in worktree", CategoryScopeDrift, "drift"},
		{"scope_out_of", "out-of-scope file modified", CategoryScopeDrift, "out-of-scope"},

		// Execution error
		{"exec_error", "execute error: agent crashed", CategoryExecutionError, "execute error"},
		{"exec_activity", "ExecuteActivity failed", CategoryExecutionError, "ExecuteActivity"},

		// Catch-all
		{"generic_failure", "something strange happened", CategoryDoDCheckFailed, "something strange"},

		// Empty
		{"empty_string", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cat, summary := ClassifyFailure(tt.failures)
			if cat != tt.wantCat {
				t.Errorf("ClassifyFailure(%q) category = %q, want %q", tt.failures, cat, tt.wantCat)
			}
			if tt.wantSub != "" && !strings.Contains(strings.ToLower(summary), strings.ToLower(tt.wantSub)) {
				t.Errorf("ClassifyFailure(%q) summary = %q, want substring %q", tt.failures, summary, tt.wantSub)
			}
		})
	}
}

func TestIsInfrastructureFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		failures string
		want     bool
	}{
		{"parallel_lint", "parallel golangci-lint is running", true},
		{"lint_exit3", "golangci-lint exit 3", true},
		{"lint_exit_neg1", "golangci-lint exit -1", true},
		{"semgrep_exit7", "semgrep exit 7", true},
		{"signal_kill", "process received exit -1", true},
		{"vcs_status", "error obtaining vcs status", true},
		{"cmd_not_found", "command not found", true},
		{"no_such_file", "no such file or directory", true},
		{"perm_denied", "permission denied", true},
		{"disk_full", "no space left on device", true},
		{"disk_quota", "disk quota exceeded", true},
		{"git_lock", "fatal: git lock on index", true},
		{"index_lock", "fatal: Unable to create index.lock", true},
		{"unable_create", "unable to create '/tmp/foo'", true},
		{"fatal_access", "fatal: unable to access 'https://github.com'", true},
		{"overloaded", "API server overloaded", true},
		{"normal_test_fail", "--- FAIL: TestFoo (0.1s)", false},
		{"normal_lint_fail", "golangci-lint exit 1: found issues", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsInfrastructureFailure(tt.failures); got != tt.want {
				t.Errorf("IsInfrastructureFailure(%q) = %v, want %v", tt.failures, got, tt.want)
			}
		})
	}
}

func TestIsTransientInfraFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		failures string
		want     bool
	}{
		// Transient — worth retrying
		{"parallel_lint", "parallel golangci-lint is running", true},
		{"git_lock", "git lock contention", true},
		{"index_lock", "index.lock exists", true},
		{"unable_create", "unable to create temp file", true},

		// Persistent — don't retry
		{"disk_full", "no space left on device", false},
		{"cmd_not_found", "command not found: semgrep", false},
		{"perm_denied", "permission denied", false},
		{"overloaded", "server overloaded", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsTransientInfraFailure(tt.failures); got != tt.want {
				t.Errorf("IsTransientInfraFailure(%q) = %v, want %v", tt.failures, got, tt.want)
			}
		})
	}
}

func TestExtractInfraReason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		failures string
		wantSub  string
	}{
		{"parallel_lint", "parallel golangci-lint is running", "parallel lock"},
		{"lint_exit3", "golangci-lint exit 3", "config/runtime error"},
		{"semgrep_exit7", "semgrep exit 7", "config/download error"},
		{"cmd_not_found", "command not found", "not found in PATH"},
		{"disk_full", "no space left on device", "disk full"},
		{"disk_quota", "disk quota exceeded", "disk full"},
		{"git_lock", "git lock contention", "git lock"},
		{"index_lock", "index.lock exists", "git lock"},
		{"overloaded", "API overloaded", "overloaded"},
		{"generic_infra", "fatal: unable to access repo", "infrastructure"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ExtractInfraReason(tt.failures)
			if !strings.Contains(strings.ToLower(got), strings.ToLower(tt.wantSub)) {
				t.Errorf("ExtractInfraReason(%q) = %q, want substring %q", tt.failures, got, tt.wantSub)
			}
		})
	}
}

func TestExtractFirstLine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		s    string
		want string
	}{
		{"single_line", "hello world", "hello world"},
		{"multi_line", "first\nsecond\nthird", "first"},
		{"leading_empty", "\n\nhello", "hello"},
		{"all_whitespace", "  \n  \n  ", ""},
		{"empty", "", ""},
		{"long_line", string(make([]byte, 300)), string(make([]byte, 200)) + "..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ExtractFirstLine(tt.s)
			if got != tt.want {
				t.Errorf("ExtractFirstLine(%q) = %q, want %q", tt.s, got, tt.want)
			}
		})
	}
}
