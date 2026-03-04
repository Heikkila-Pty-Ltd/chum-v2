package beadsbridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
)

// GateDecision is the deterministic result of evaluating an issue for admission.
type GateDecision struct {
	Pass   bool
	Reason string
}

// EvaluateGate applies deterministic admission predicates.
func EvaluateGate(issue beads.Issue, canaryLabel string) GateDecision {
	if issue.ID == "" {
		return GateDecision{Pass: false, Reason: "missing_issue_id"}
	}
	if strings.TrimSpace(issue.Title) == "" {
		return GateDecision{Pass: false, Reason: "missing_title"}
	}
	if strings.TrimSpace(canaryLabel) != "" && !hasLabel(issue.Labels, canaryLabel) {
		return GateDecision{Pass: false, Reason: "no_canary_label"}
	}
	if issue.Status == "closed" || issue.Status == "done" || issue.Status == "completed" {
		return GateDecision{Pass: false, Reason: "terminal_issue_status"}
	}
	return GateDecision{Pass: true, Reason: "gate_pass"}
}

// FingerprintIssue computes a deterministic revision key for idempotency.
func FingerprintIssue(issue beads.Issue) string {
	deps := make([]string, 0, len(issue.Dependencies))
	for _, dep := range issue.Dependencies {
		if dep.DependsOnID == "" {
			continue
		}
		deps = append(deps, dep.DependsOnID)
	}
	sort.Strings(deps)
	labels := append([]string(nil), issue.Labels...)
	sort.Strings(labels)
	payload := map[string]any{
		"id":                  issue.ID,
		"title":               issue.Title,
		"description":         issue.Description,
		"status":              issue.Status,
		"priority":            issue.Priority,
		"issue_type":          issue.IssueType,
		"acceptance_criteria": issue.AcceptanceCriteria,
		"design":              issue.Design,
		"estimated_minutes":   issue.EstimatedMinutes,
		"labels":              labels,
		"dependencies":        deps,
	}
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hasLabel(labels []string, label string) bool {
	for _, l := range labels {
		if strings.EqualFold(strings.TrimSpace(l), strings.TrimSpace(label)) {
			return true
		}
	}
	return false
}
