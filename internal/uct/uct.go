// Package uct implements UCT (Upper Confidence bounds applied to Trees) selection.
// Ported from cortex/internal/planner/primitives.go — pure functions, no dependencies.
package uct

import (
	"math"
	"time"
)

// Arm is one candidate branch in UCT selection.
type Arm struct {
	Key         string
	Visits      int
	TotalReward float64
	Prior       float64
	LastSeen    time.Time
}

// Selection is the selected branch returned by Select.
type Selection struct {
	Index int
	Key   string
	Score float64
}

// Select picks the highest-scoring branch using UCT.
func Select(arms []Arm, exploration float64) (Selection, bool) {
	if len(arms) == 0 {
		return Selection{}, false
	}
	if exploration < 0 {
		exploration = 0
	}

	totalVisits := 0
	for i := range arms {
		if arms[i].Visits > 0 {
			totalVisits += arms[i].Visits
		}
	}

	best := Selection{Index: -1, Score: math.Inf(-1)}
	bestPrior := math.Inf(-1)
	bestKey := ""
	for i := range arms {
		score := score(arms[i], totalVisits, exploration)
		prior := arms[i].Prior
		key := arms[i].Key
		if score > best.Score ||
			(score == best.Score && (prior > bestPrior || (prior == bestPrior && (best.Index < 0 || key < bestKey)))) {
			best = Selection{
				Index: i,
				Key:   key,
				Score: score,
			}
			bestPrior = prior
			bestKey = key
		}
	}
	return best, true
}

func score(arm Arm, totalVisits int, exploration float64) float64 {
	visits := arm.Visits
	if visits <= 0 {
		return math.Inf(1)
	}
	if totalVisits < visits {
		totalVisits = visits
	}

	exploit := arm.TotalReward / float64(visits)
	explore := exploration * math.Sqrt(math.Log(float64(totalVisits+1))/float64(visits))
	return exploit + explore + arm.Prior
}

// DecayByAge applies exponential half-life decay for stale values.
func DecayByAge(value float64, age, halfLife time.Duration) float64 {
	if value == 0 || age <= 0 || halfLife <= 0 {
		return value
	}
	factor := math.Pow(0.5, float64(age)/float64(halfLife))
	return value * factor
}

// PruneStaleArms drops old, low-signal branches while keeping unvisited branches.
func PruneStaleArms(arms []Arm, now time.Time, minVisits int, staleAfter time.Duration) []Arm {
	if len(arms) == 0 {
		return nil
	}
	if minVisits < 0 {
		minVisits = 0
	}
	if staleAfter <= 0 {
		out := make([]Arm, len(arms))
		copy(out, arms)
		return out
	}

	out := make([]Arm, 0, len(arms))
	for i := range arms {
		arm := arms[i]
		visits := arm.Visits
		if visits < 0 {
			visits = 0
		}
		if visits == 0 {
			out = append(out, arm)
			continue
		}
		if visits >= minVisits {
			out = append(out, arm)
			continue
		}
		if arm.LastSeen.IsZero() || now.Sub(arm.LastSeen) <= staleAfter {
			out = append(out, arm)
			continue
		}
	}

	return out
}
