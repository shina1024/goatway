package scheduler

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateWeights_returnsErrorsForInvalidWeights(t *testing.T) {
	tests := []struct {
		name    string
		weights []int
		wantErr string
	}{
		{
			name:    "rejects negative weight",
			weights: []int{1, -1},
			wantErr: "invalid weight",
		},
		{
			name:    "rejects mixed weighted and nonweighted targets",
			weights: []int{1, 0},
			wantErr: "mixed weighted and nonweighted targets",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// When
			err := validateWeights(test.weights)

			// Then
			require.EqualError(t, err, test.wantErr)
		})
	}
}

func TestNewScheduler_returnsErrorForEmptyWeights(t *testing.T) {
	// When
	_, err := NewScheduler(nil)

	// Then
	require.Error(t, err)
}

func TestNewScheduler_usesRoundRobinForEqualWeights(t *testing.T) {
	tests := []struct {
		name    string
		weights []int
		want    []int
	}{
		{
			name:    "uses round robin for unset weights",
			weights: []int{0, 0},
			want:    []int{0, 1, 0, 1},
		},
		{
			name:    "uses round robin for equal weighted targets",
			weights: []int{3, 3},
			want:    []int{0, 1, 0, 1},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			scheduler, err := NewScheduler(test.weights)
			require.NoError(t, err)
			require.IsType(t, &roundRobinScheduler{}, scheduler)

			// When
			got := make([]int, len(test.want))
			for i := range got {
				got[i] = scheduler.Fetch()
			}

			// Then
			require.Equal(t, test.want, got)
		})
	}
}

func TestNewScheduler_fetchesArticleWeightedOrder(t *testing.T) {
	tests := []struct {
		name    string
		weights []int
		want    []int
	}{
		{
			name:    "alternates proportional one to two weights",
			weights: []int{1, 2},
			want:    []int{1, 0, 1},
		},
		{
			name:    "follows three five one article sequence",
			weights: []int{3, 5, 1},
			want:    []int{1, 1, 0, 1, 0, 1, 0, 1, 2},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			scheduler, err := NewScheduler(test.weights)
			require.NoError(t, err)
			require.IsType(t, &weightedRoundRobinScheduler{}, scheduler)

			// When
			got := make([]int, len(test.want))
			for i := range got {
				got[i] = scheduler.Fetch()
			}

			// Then
			require.Equal(t, test.want, got)
		})
	}
}

func TestNewScheduler_preservesFourToOneArticleCadence(t *testing.T) {
	// Given
	scheduler, err := NewScheduler([]int{4, 1})
	require.NoError(t, err)

	// When
	got := make([]int, 10)
	for i := range got {
		got[i] = scheduler.Fetch()
	}

	// Then
	require.Equal(t, []int{0, 0, 0, 0, 1, 0, 0, 0, 0, 1}, got)
	indexZeroCount := 0
	for _, index := range got {
		if index == 0 {
			indexZeroCount++
		}
	}
	require.Equal(t, 8, indexZeroCount)
	require.Equal(t, 2, len(got)-indexZeroCount)
}

func TestNewScheduler_normalizesWeightsByGreatestCommonDivisor(t *testing.T) {
	// Given
	scheduler, err := NewScheduler([]int{2, 4})
	require.NoError(t, err)

	// When
	got := []int{scheduler.Fetch(), scheduler.Fetch(), scheduler.Fetch()}

	// Then
	require.Equal(t, []int{1, 0, 1}, got)
}

func TestScheduler_Fetch_isSafeForConcurrentCallers(t *testing.T) {
	// Given
	scheduler, err := NewScheduler([]int{1, 2})
	require.NoError(t, err)

	const workerCount = 32
	const fetchesPerWorker = 64
	results := make(chan int, workerCount*fetchesPerWorker)
	var workers sync.WaitGroup
	workers.Add(workerCount)

	// When
	for range workerCount {
		go func() {
			defer workers.Done()
			for range fetchesPerWorker {
				results <- scheduler.Fetch()
			}
		}()
	}
	workers.Wait()
	close(results)

	// Then
	require.Len(t, results, workerCount*fetchesPerWorker)
	for index := range results {
		require.GreaterOrEqual(t, index, 0)
		require.Less(t, index, 2)
	}
}
