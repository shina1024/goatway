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
		{"mixed target weights", func(files map[string]string) {
			files["target_groups.yml"] = strings.Replace(files["target_groups.yml"], "host: catalog-b\n      port: 8080\n      weight: 1", "host: catalog-b\n      port: 8080\n      weight: 0", 1)
		}, "mixed weighted and nonweighted", false},
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
		{"negative deployment weight", func(files map[string]string) {
			files["deployment.yml"] = strings.Replace(files["deployment.yml"], "primary_weight: 90", "primary_weight: -1", 1)
		}, "negative weight", false},
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
