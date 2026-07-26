package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_Config_Load_parses_all_six_files_when_configuration_is_valid(t *testing.T) {
	// Given
	dir := writeConfigFiles(t, validConfigFiles())

	// When
	config, err := Load(dir)

	// Then
	require.NoError(t, err)
	require.Len(t, config.TargetGroups, 2)
	require.Len(t, config.Routes, 1)
	require.Equal(t, ClientType("public"), config.Routes[0].From.Clients[0])
	require.Equal(t, TargetGroupID("catalog"), config.Routes[0].To.Destinations[0].TargetGroup)
	require.Equal(t, []string{"token-a"}, config.APIClientTokens[ClientType("public")])
	require.Equal(t, []string{"10.0.0.0/24"}, config.IPRangeGroups["office"])
	require.Equal(t, 100, config.MaxConcurrentRequests[ClientType("public")])
	require.Equal(t, 3, config.Deployment.PrimaryPods)
}

func Test_Config_Load_uses_gateway_default_when_file_is_absent(t *testing.T) {
	// Given
	dir := writeConfigFiles(t, validConfigFiles())

	// When
	config, err := Load(dir)

	// Then
	require.NoError(t, err)
	require.Equal(t, int64(10485760), config.Gateway.Proxy.MaxResponseBodySizeBytes)
}

func Test_Config_Load_reads_gateway_proxy_setting(t *testing.T) {
	// Given
	files := validConfigFiles()
	files["gateway.yml"] = `schema_version: 1
proxy:
  max_response_body_size_bytes: 2097152
`

	// When
	config, err := Load(writeConfigFiles(t, files))

	// Then
	require.NoError(t, err)
	require.Equal(t, int64(2097152), config.Gateway.Proxy.MaxResponseBodySizeBytes)
}

func Test_TargetGroupConfig_resolves_zero_values_to_defaults(t *testing.T) {
	// Given
	group := TargetGroupConfig{Targets: []TargetConfig{{}, {}}}

	// When
	maxTries := group.EffectiveMaxTryCount()
	connectTimeout := group.ConnectTimeoutFor(TargetConfig{})
	readTimeout := group.ReadTimeoutFor(TargetConfig{})
	retryBase := group.EffectiveRetryBaseInterval()
	retryMax := group.EffectiveRetryMaxInterval()

	// Then
	require.Equal(t, 2, maxTries)
	require.Equal(t, time.Second, connectTimeout)
	require.Equal(t, 10*time.Second, readTimeout)
	require.Equal(t, 50*time.Millisecond, retryBase)
	require.Equal(t, 500*time.Millisecond, retryMax)
}

func Test_TargetGroupConfig_SchemeFor_resolves_target_group_and_default(t *testing.T) {
	tests := []struct {
		name         string
		targetScheme string
		groupScheme  string
		want         string
	}{
		{name: "uses target scheme", targetScheme: "http", groupScheme: "https", want: "http"},
		{name: "uses group scheme", groupScheme: "https", want: "https"},
		{name: "uses default scheme", want: "http"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			group := TargetGroupConfig{Scheme: test.groupScheme}
			target := TargetConfig{Scheme: test.targetScheme}

			// When
			got := group.SchemeFor(target)

			// Then
			require.Equal(t, test.want, got)
		})
	}
}

func writeConfigFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, contents := range files {
		err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600)
		require.NoError(t, err)
	}
	return dir
}

func validConfigFiles() map[string]string {
	return map[string]string{
		"target_groups.yml": `catalog:
  targets:
    - host: catalog-a
      port: 8080
      weight: 1
      retry_to: catalog-b:8080
      connect_timeout: 100
      read_timeout: 200
      idle_conn_timeout: 300
    - host: catalog-b
      port: 8080
      weight: 1
  max_try_count: 2
  retry_cases:
    - server_error
    - timeout
  retry_non_idempotent: false
  retry_base_interval: 75
  retry_max_interval: 750
  retry_to_target_group_id: secondary
  connect_timeout: 400
  read_timeout: 500
  idle_conn_timeout: 600
  max_idle_conns_per_host: 10
secondary:
  targets:
    - host: secondary-a
      port: 8081
      weight: 0
`,
		"routes.yml": `- from:
    path: ^/sample/(.+)$
    clients:
      - public
    ip_range_groups:
      - office
  to:
    destinations:
      - target_group: catalog
        path: /$1
        weight: 1
      - target_group: secondary
        path: /secondary/$1
        weight: 1
`,
		"api_client_tokens.yml": `public:
  - token-a
backoffice:
  - token-b
`,
		"ip_range_groups.yml": `office:
  - 10.0.0.0/24
`,
		"max_concurrent_requests.yml": `public: 100
backoffice: 20
`,
		"deployment.yml": `primary_pods: 3
canary_pods: 1
primary_weight: 90
canary_weight: 10
`,
	}
}
