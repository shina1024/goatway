package scheduler

import (
	"errors"
	"sync"
)

var (
	errEmptyWeights                = errors.New("weights must not be empty")
	errInvalidWeight               = errors.New("invalid weight")
	errMixedWeightedAndNonweighted = errors.New("mixed weighted and nonweighted targets")
)

// Scheduler selects the next target index.
type Scheduler interface {
	Fetch() int
}

// NewScheduler creates a round-robin scheduler for equal weights and a
// weighted round-robin scheduler otherwise.
func NewScheduler(weights []int) (Scheduler, error) {
	if err := validateWeights(weights); err != nil {
		return nil, err
	}
	if len(weights) == 0 {
		return nil, errEmptyWeights
	}

	allWeightsEqual := true
	for _, weight := range weights[1:] {
		if weight != weights[0] {
			allWeightsEqual = false
			break
		}
	}
	if allWeightsEqual {
		return newRoundRobinScheduler(len(weights)), nil
	}
	return newWeightedRoundRobinScheduler(weights), nil
}

func validateWeights(weights []int) error {
	nonweightedCount := 0
	for _, weight := range weights {
		if weight < 0 {
			return errInvalidWeight
		}
		if weight == 0 {
			nonweightedCount++
		}
	}
	if nonweightedCount > 0 && nonweightedCount < len(weights) {
		return errMixedWeightedAndNonweighted
	}
	return nil
}

type roundRobinScheduler struct {
	mutex   *sync.Mutex
	current int
	count   int
}

func newRoundRobinScheduler(count int) *roundRobinScheduler {
	return &roundRobinScheduler{
		mutex:   new(sync.Mutex),
		current: 0,
		count:   count,
	}
}

func (s *roundRobinScheduler) Fetch() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	i := s.current
	s.current = (i + 1) % s.count
	return i
}

type weightedRoundRobinScheduler struct {
	mutex         *sync.Mutex
	weights       []int
	maxWeight     int
	currentIndex  int
	currentWeight int
}

func newWeightedRoundRobinScheduler(weights []int) *weightedRoundRobinScheduler {
	normalizedWeights := normalizeWeights(weights)
	maxWeight := 0
	for _, weight := range normalizedWeights {
		if weight > maxWeight {
			maxWeight = weight
		}
	}

	return &weightedRoundRobinScheduler{
		mutex:         new(sync.Mutex),
		weights:       normalizedWeights,
		maxWeight:     maxWeight,
		currentIndex:  -1,
		currentWeight: 0,
	}
}

func (s *weightedRoundRobinScheduler) Fetch() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	for {
		s.currentIndex = (s.currentIndex + 1) % len(s.weights)
		if s.currentIndex == 0 {
			s.currentWeight--
			if s.currentWeight <= 0 {
				s.currentWeight = s.maxWeight
			}
		}
		if s.weights[s.currentIndex] >= s.currentWeight {
			return s.currentIndex
		}
	}
}

func normalizeWeights(weights []int) []int {
	divisor := weights[0]
	for _, weight := range weights[1:] {
		divisor = gcd(divisor, weight)
	}

	normalizedWeights := make([]int, len(weights))
	for i, weight := range weights {
		normalizedWeights[i] = weight / divisor
	}
	return normalizedWeights
}
