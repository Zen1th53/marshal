package scheduler

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
)

type Scheduler struct {
	mu     sync.RWMutex
	leases map[string]string
}

func NewScheduler() *Scheduler {
	return &Scheduler{
		leases: make(map[string]string),
	}
}

type scoredCandidate struct {
	candidate Candidate
	score     float64
	reasons   []string
}

func (s *Scheduler) Next(ctx context.Context, task Task, candidates []Candidate) (*Assignment, error) {
	if task.TaskID == "" {
		return nil, ErrTaskNotReady
	}
	if len(candidates) == 0 {
		return nil, ErrNoEligibleAgent
	}

	var eligible []scoredCandidate
	for _, c := range candidates {
		if c.AgentID == "" {
			continue
		}
		if !hasAllCapabilities(c.Capabilities, task.RequiredCapabilities) {
			continue
		}

		score, reasons := scoreCandidate(task, c)
		eligible = append(eligible, scoredCandidate{
			candidate: c,
			score:     score,
			reasons:   reasons,
		})
	}

	if len(eligible) == 0 {
		return nil, ErrNoEligibleAgent
	}

	// Deterministic sorting: highest score first; tie-break on AgentID lexicographical order
	sort.Slice(eligible, func(i, j int) bool {
		if math.Abs(eligible[i].score-eligible[j].score) > 1e-6 {
			return eligible[i].score > eligible[j].score
		}
		return eligible[i].candidate.AgentID < eligible[j].candidate.AgentID
	})

	best := eligible[0]
	leaseID := fmt.Sprintf("lease-%s-%s", task.TaskID, best.candidate.AgentID)

	s.mu.Lock()
	s.leases[leaseID] = best.candidate.AgentID
	s.mu.Unlock()

	return &Assignment{
		TaskID:  task.TaskID,
		AgentID: best.candidate.AgentID,
		LeaseID: leaseID,
		Score:   math.Round(best.score*1000) / 1000,
		Reasons: best.reasons,
	}, nil
}

func hasAllCapabilities(candidateCaps, requiredCaps []string) bool {
	if len(requiredCaps) == 0 {
		return true
	}
	capMap := make(map[string]bool, len(candidateCaps))
	for _, cap := range candidateCaps {
		capMap[strings.TrimSpace(cap)] = true
	}
	for _, req := range requiredCaps {
		if !capMap[strings.TrimSpace(req)] {
			return false
		}
	}
	return true
}

func scoreCandidate(_ Task, c Candidate) (float64, []string) {
	// 1. Success rate factor (weight: 0.40)
	successRate := c.SuccessRate
	if successRate < 0 {
		successRate = 0
	} else if successRate > 1.0 {
		successRate = 1.0
	}
	successFactor := successRate * 0.40

	// 2. Load factor (weight: 0.30) - lower load is better
	load := c.Load
	if load < 0 {
		load = 0
	} else if load > 1.0 {
		load = 1.0
	}
	loadFactor := (1.0 - load) * 0.30

	// 3. Context utilization factor (weight: 0.20) - lower utilization is better
	ctxUtil := c.ContextUtilization
	if ctxUtil < 0 {
		ctxUtil = 0
	} else if ctxUtil > 1.0 {
		ctxUtil = 1.0
	}
	contextFactor := (1.0 - ctxUtil) * 0.20

	// 4. Cost factor (weight: 0.10) - lower cost is better
	cost := c.EstimatedCost
	if cost < 0 {
		cost = 0
	}
	costFactor := (1.0 / (1.0 + cost)) * 0.10

	totalScore := successFactor + loadFactor + contextFactor + costFactor

	reasons := []string{
		fmt.Sprintf("success_rate=%.2f (contrib=%.3f)", successRate, successFactor),
		fmt.Sprintf("load=%.2f (contrib=%.3f)", load, loadFactor),
		fmt.Sprintf("context_utilization=%.2f (contrib=%.3f)", ctxUtil, contextFactor),
		fmt.Sprintf("estimated_cost=%.2f (contrib=%.3f)", cost, costFactor),
	}

	return totalScore, reasons
}

func (s *Scheduler) Renew(ctx context.Context, leaseID string) error {
	if leaseID == "" {
		return ErrStaleWorker
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, exists := s.leases[leaseID]; !exists {
		return ErrStaleWorker
	}
	return nil
}

func (s *Scheduler) Release(ctx context.Context, leaseID, outcome string) error {
	if leaseID == "" {
		return ErrStaleWorker
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.leases[leaseID]; !exists {
		return ErrStaleWorker
	}
	delete(s.leases, leaseID)
	return nil
}
