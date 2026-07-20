package proxy

import (
	"net"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"goatway/internal/config"
	"goatway/internal/router"
	"goatway/internal/targetgroup"
)

func newRetryRegistry(t *testing.T, configs map[config.TargetGroupID]config.TargetGroupConfig) *targetgroup.Registry {
	t.Helper()
	registry, err := targetgroup.NewRegistry(configs)
	require.NoError(t, err)
	return registry
}

func retryTarget(t *testing.T, rawURL string, readTimeout time.Duration) config.TargetConfig {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	require.NoError(t, err)
	host, portText, err := net.SplitHostPort(parsed.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	return config.TargetConfig{
		Host:        host,
		Port:        port,
		Weight:      1,
		ReadTimeout: config.Milliseconds(readTimeout / time.Millisecond),
	}
}

func retryInput(group *targetgroup.TargetGroup, paths map[string]string) RetryInput {
	return RetryInput{
		Group: group,
		Match: router.Match{
			TargetGroupID: string(group.ID()),
			RoutedPathMap: paths,
		},
	}
}
