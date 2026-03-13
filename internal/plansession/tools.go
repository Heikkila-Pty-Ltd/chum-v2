package plansession

import (
	"fmt"
	"os"
)

// writeToolsScript generates a shell script with functions that call the dashboard API.
func writeToolsScript(path, apiPort, token string) error {
	script := fmt.Sprintf(`#!/bin/bash
# Planning session tools — auto-generated, do not edit.
# These functions call the CHUM dashboard API.

CHUM_API="http://localhost:%s"
CHUM_TOKEN="%s"

_chum_curl() {
  curl -sf -H "Authorization: Bearer $CHUM_TOKEN" "$@"
}

chum-tasks() {
  local project="${1:-}"
  local status="${2:-}"
  local url="$CHUM_API/api/dashboard/tasks/${project}"
  if [ -n "$status" ]; then
    url="${url}?status=${status}"
  fi
  _chum_curl "$url"
}

chum-task() {
  local id="${1:?task ID required}"
  _chum_curl "$CHUM_API/api/dashboard/task/${id}"
}

chum-plan() {
  local id="${1:?plan ID required}"
  _chum_curl "$CHUM_API/api/dashboard/plan/${id}"
}

chum-plans() {
  local project="${1:?project required}"
  _chum_curl "$CHUM_API/api/dashboard/plans/${project}"
}

chum-context() {
  local project="${1:?project required}"
  local query="${2:-}"
  _chum_curl "$CHUM_API/api/dashboard/context/build?project=${project}&query=${query}"
}

chum-lessons() {
  local query="${1:?query required}"
  _chum_curl "$CHUM_API/api/dashboard/lessons/search?q=${query}"
}
`, apiPort, token)

	return os.WriteFile(path, []byte(script), 0700)
}

// writeSessionCLAUDEMD generates a CLAUDE.md for the planning session.
func writeSessionCLAUDEMD(path, planID string) error {
	content := fmt.Sprintf(`# Planning Session

You are a planning assistant for the CHUM project management system.
You are interviewing a human to refine plan %s into well-specified tasks.

## Available Tools

Use the shell functions to interact with the project:

`+"```"+`bash
chum-tasks myproject          # List tasks for a project
chum-tasks myproject done     # List done tasks
chum-task TASK-123            # Get task details
chum-plan PLAN-456            # Get current plan
chum-plans myproject          # List plans for a project
chum-context myproject "auth" # Get codebase context
chum-lessons "transport"      # Search past solutions
`+"```"+`

## Planning Methodology

- Ask ONE focused question at a time
- Build on previous answers
- Use chum-context to ground your understanding in actual code
- Use chum-lessons to check for past solutions
- Validate assumptions against the codebase before recommending
- Apply YAGNI — prefer simpler approaches

## File Access

You may read any file in the project. You may write ONLY to the docs/ directory.

## Structured Output

When asked to extract a plan, produce a JSON object with:
- problem_statement, desired_outcome, summary
- constraints, assumptions, non_goals, risks
- open_questions, validation_strategy
- working_markdown (full plan document in markdown)
`, planID)

	return os.WriteFile(path, []byte(content), 0644)
}
