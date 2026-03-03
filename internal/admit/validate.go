package admit

import (
	"strings"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
)

// MaxEstimateMinutes is the upper bound for task estimates.
// Tasks exceeding this must be broken down into smaller units.
// Kept deliberately tight to prevent agent timeout / iteration exhaustion.
const MaxEstimateMinutes = 15

// MinDescriptionLen is the minimum length for a task description.
const MinDescriptionLen = 50

// ValidationResult reports whether a task passed structural validation.
type ValidationResult struct {
	Pass   bool
	Reason string
}

// ValidateStructure checks that a task is well-formed for autonomous execution.
// Rules are applied in order; the first failure short-circuits.
func ValidateStructure(t dag.Task) ValidationResult {
	// 1. Epics are containers, not executable
	if strings.EqualFold(t.Type, "epic") {
		return ValidationResult{Reason: "epics are containers, not executable tasks"}
	}

	// 2. Description must be substantive
	desc := strings.TrimSpace(t.Description)
	if len(desc) < MinDescriptionLen {
		return ValidationResult{Reason: "description is too short (minimum 50 characters)"}
	}

	// 3. Description must not just restate the title
	normDesc := normalize(desc)
	normTitle := normalize(t.Title)
	if normTitle != "" && (normDesc == normTitle || strings.HasPrefix(normDesc, normTitle+".") || strings.HasPrefix(normDesc, normTitle+" ")) {
		// Allow if desc is significantly longer than title (has real detail after it)
		if len(normDesc) < len(normTitle)+20 {
			return ValidationResult{Reason: "description must provide detail beyond the title"}
		}
	}

	// 4. Acceptance criteria must be present
	if strings.TrimSpace(t.Acceptance) == "" {
		return ValidationResult{Reason: "acceptance criteria are required"}
	}

	// 5. Estimate must be set and within bounds
	if t.EstimateMinutes <= 0 {
		return ValidationResult{Reason: "estimate_minutes must be set"}
	}
	if t.EstimateMinutes > MaxEstimateMinutes {
		return ValidationResult{Reason: "tasks over 15 minutes must be broken down"}
	}

	return ValidationResult{Pass: true}
}

// normalize lowercases and strips punctuation for fuzzy comparison.
func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == ' ' {
			return r
		}
		return -1
	}, s)
	return strings.Join(strings.Fields(s), " ")
}
