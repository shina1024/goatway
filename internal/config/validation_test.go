package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_Config_Load_rejects_invalid_configuration_with_typed_errors(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(map[string]string)
		rule       string
		decodeFail bool
	}{
		{"unknown YAML field", func(files map[string]string) { files["routes.yml"] += "  unexpected: true\n" }, "", true},
		{"negative target weight", func(files map[string]string) {
			files["target_groups.yml"] = strings.Replace(files["target_groups.yml"], "weight: 1", "weight: -1", 1)
		}, "negative weight", false},
		{"empty target list", func(files map[string]string) {
			files["target_groups.yml"] = "catalog:\n  targets: []\n"
		}, "empty target list", false},
		{"empty host", func(files map[string]string) {
			files["target_groups.yml"] = strings.Replace(files["target_groups.yml"], "host: catalog-a", "host: \"\"", 1)
		}, "empty host", false},
		{"non-positive port", func(files map[string]string) {
			files["target_groups.yml"] = strings.Replace(files["target_groups.yml"], "port: 8080", "port: 0", 1)
		}, "non-positive port", false},
		{"mixed target weights", func(files map[string]string) {
			files["target_groups.yml"] = strings.Replace(files["target_groups.yml"], "host: catalog-b\n      port: 8080\n      weight: 1", "host: catalog-b\n      port: 8080\n      weight: 0", 1)
		}, "mixed weighted and nonweighted", false},
		{"empty destination list", func(files map[string]string) {
			files["routes.yml"] = "- from:\n    path: ^/sample/(.+)$\n  to:\n    destinations: []\n"
		}, "empty destination list", false},
		{"mixed destination weights", func(files map[string]string) {
			files["routes.yml"] = strings.Replace(files["routes.yml"], "      - target_group: secondary\n        path: /secondary/$1\n        weight: 1", "      - target_group: secondary\n        path: /secondary/$1\n        weight: 0", 1)
		}, "mixed weighted and nonweighted", false},
		{"unsupported retry case", func(files map[string]string) {
			files["target_groups.yml"] = strings.Replace(files["target_groups.yml"], "- timeout", "- unauthorized", 1)
		}, "invalid retry case", false},
		{"invalid route regexp", func(files map[string]string) {
			files["routes.yml"] = strings.Replace(files["routes.yml"], "path: ^/sample/(.+)$", "path: '['", 1)
		}, "invalid route regexp", false},
		{"unknown route target group", func(files map[string]string) {
			files["routes.yml"] = strings.Replace(files["routes.yml"], "target_group: secondary", "target_group: absent", 1)
		}, "unknown target group", false},
		{"duplicate route target group", func(files map[string]string) {
			files["routes.yml"] = strings.Replace(files["routes.yml"], "target_group: secondary", "target_group: catalog", 1)
		}, "duplicate target group", false},
		{"missing retry target group", func(files map[string]string) {
			files["target_groups.yml"] = strings.Replace(files["target_groups.yml"], "retry_to_target_group_id: secondary", "retry_to_target_group_id: absent", 1)
		}, "unknown retry target group", false},
		{"retry target without route rewrite", func(files map[string]string) {
			files["routes.yml"] = strings.Replace(files["routes.yml"], "\n      - target_group: secondary\n        path: /secondary/$1\n        weight: 1", "", 1)
		}, "retry target group missing route destination", false},
		{"unknown per-target retry address", func(files map[string]string) {
			files["target_groups.yml"] = strings.Replace(files["target_groups.yml"], "retry_to: catalog-b:8080", "retry_to: catalog-c:8080", 1)
		}, "unknown retry target", false},
		{"invalid CIDR", func(files map[string]string) {
			files["ip_range_groups.yml"] = strings.Replace(files["ip_range_groups.yml"], "10.0.0.0/24", "not-a-cidr", 1)
		}, "invalid CIDR", false},
		{"duplicate token across client types", func(files map[string]string) {
			files["api_client_tokens.yml"] = strings.Replace(files["api_client_tokens.yml"], "token-b", "token-a", 1)
		}, "duplicate token", false},
		{"negative max try count", func(files map[string]string) {
			files["target_groups.yml"] = strings.Replace(files["target_groups.yml"], "max_try_count: 2", "max_try_count: -1", 1)
		}, "negative max try count", false},
		{"negative timeout", func(files map[string]string) {
			files["target_groups.yml"] = strings.Replace(files["target_groups.yml"], "connect_timeout: 100", "connect_timeout: -1", 1)
		}, "negative timeout", false},
		{"negative retry interval", func(files map[string]string) {
			files["target_groups.yml"] = strings.Replace(files["target_groups.yml"], "retry_base_interval: 75", "retry_base_interval: -1", 1)
		}, "negative retry interval", false},
		{"invalid target group scheme", func(files map[string]string) {
			files["target_groups.yml"] = strings.Replace(files["target_groups.yml"], "max_try_count: 2", "scheme: ftp\n  max_try_count: 2", 1)
		}, "invalid scheme", false},
		{"invalid target scheme", func(files map[string]string) {
			files["target_groups.yml"] = strings.Replace(files["target_groups.yml"], "host: catalog-a\n      port: 8080", "host: catalog-a\n      scheme: ftp\n      port: 8080", 1)
		}, "invalid scheme", false},
		{"negative deployment weight", func(files map[string]string) {
			files["deployment.yml"] = strings.Replace(files["deployment.yml"], "primary_weight: 90", "primary_weight: -1", 1)
		}, "negative weight", false},
		{"negative deployment pod count", func(files map[string]string) {
			files["deployment.yml"] = strings.Replace(files["deployment.yml"], "primary_pods: 3", "primary_pods: -1", 1)
		}, "negative pod count", false},
		{"invalid deployment traffic weight total", func(files map[string]string) {
			files["deployment.yml"] = strings.Replace(files["deployment.yml"], "canary_weight: 10", "canary_weight: 20", 1)
		}, "traffic weights must total 100 or 0", false},
		{"negative max concurrent requests", func(files map[string]string) {
			files["max_concurrent_requests.yml"] = strings.Replace(files["max_concurrent_requests.yml"], "public: 100", "public: -1", 1)
		}, "negative max concurrent requests", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			files := validConfigFiles()
			test.mutate(files)

			// When
			_, err := Load(writeConfigFiles(t, files))

			// Then
			require.Error(t, err)
			if test.decodeFail {
				var decodeErr *DecodeError
				require.ErrorAs(t, err, &decodeErr)
				return
			}
			var validationErr *ValidationError
			require.ErrorAs(t, err, &validationErr)
			require.Equal(t, test.rule, validationErr.Rule)
		})
	}
}

func Test_Config_Load_rejects_invalid_gateway_configuration_with_typed_errors(t *testing.T) {
	tests := []struct {
		name       string
		gateway    string
		wantFile   string
		wantField  string
		wantRule   string
		decodeFail bool
	}{
		{
			name:       "unknown YAML field",
			gateway:    "schema_version: 1\nproxy:\n  unexpected: true\n",
			decodeFail: true,
		},
		{
			name:      "missing schema version",
			gateway:   "proxy: {}\n",
			wantFile:  "gateway.yml",
			wantField: "schema_version",
			wantRule:  "schema version must equal 1",
		},
		{
			name:      "empty gateway file",
			gateway:   "",
			wantFile:  "gateway.yml",
			wantField: "schema_version",
			wantRule:  "schema version must equal 1",
		},
		{
			name:      "unsupported schema version",
			gateway:   "schema_version: 2\nproxy: {}\n",
			wantFile:  "gateway.yml",
			wantField: "schema_version",
			wantRule:  "schema version must equal 1",
		},
		{
			name:      "negative max response body size",
			gateway:   "schema_version: 1\nproxy:\n  max_response_body_size_bytes: -1\n",
			wantFile:  "gateway.yml",
			wantField: "proxy.max_response_body_size_bytes",
			wantRule:  "positive max response body size",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			files := validConfigFiles()
			files["gateway.yml"] = test.gateway

			// When
			_, err := Load(writeConfigFiles(t, files))

			// Then
			require.Error(t, err)
			if test.decodeFail {
				var decodeErr *DecodeError
				require.ErrorAs(t, err, &decodeErr)
				return
			}
			var validationErr *ValidationError
			require.ErrorAs(t, err, &validationErr)
			require.Equal(t, test.wantFile, validationErr.File)
			require.Equal(t, test.wantField, validationErr.Field)
			require.Equal(t, test.wantRule, validationErr.Rule)
		})
	}
}
