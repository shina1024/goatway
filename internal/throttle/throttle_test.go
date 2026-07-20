package throttle

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_DetectDepType_returns_canary_when_hostname_contains_canary(t *testing.T) {
	// Given
	hostname := "api-gateway-canary-x"

	// When
	got := DetectDepType(hostname)

	// Then
	require.Equal(t, "canary", got)
}

func Test_FileFetcher_reads_current_deployment_file_when_fetching(t *testing.T) {
	// Given
	path := writeTestFile(t, "deployment.yml", "primary_pods: 20\ncanary_pods: 1\nprimary_weight: 90\ncanary_weight: 10\n")
	fetcher := NewFileFetcher(path)

	// When
	counts, countsErr := fetcher.FetchInstanceCounts(context.Background())
	weights, weightsErr := fetcher.FetchTrafficWeight(context.Background())

	// Then
	require.NoError(t, countsErr)
	require.NoError(t, weightsErr)
	require.Equal(t, InstanceCounts{Primary: 20, Canary: 1}, counts)
	require.Equal(t, TrafficWeight{Primary: 90, Canary: 10}, weights)

	// Given
	require.NoError(t, os.WriteFile(path, []byte("primary_pods: 21\ncanary_pods: 2\nprimary_weight: 80\ncanary_weight: 20\n"), 0o600))

	// When
	updatedCounts, err := fetcher.FetchInstanceCounts(context.Background())

	// Then
	require.NoError(t, err)
	require.Equal(t, InstanceCounts{Primary: 21, Canary: 2}, updatedCounts)
}

func Test_FileFetcher_returns_fetch_error_when_deployment_file_is_missing(t *testing.T) {
	// Given
	fetcher := NewFileFetcher(filepath.Join(t.TempDir(), "missing.yml"))

	// When
	_, err := fetcher.FetchInstanceCounts(context.Background())

	// Then
	require.Error(t, err)
	var fetchErr *FetchError
	require.ErrorAs(t, err, &fetchErr)
}

func writeTestFile(t *testing.T, name string, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}
