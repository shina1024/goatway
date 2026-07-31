package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"goatway/internal/config"
	"goatway/internal/header"
	"goatway/internal/router"
)

func Test_externalTokenSources_load_and_authenticate(t *testing.T) {
	// Given
	configDir := testConfigDir(t, "http://127.0.0.1:1")
	externalDir := t.TempDir()
	externalPath := filepath.Join(externalDir, "tokens.yml")
	require.NoError(t, os.WriteFile(externalPath, []byte("public:\n  - file-token\n"), 0o600))
	tests := []struct {
		name    string
		options config.LoadOptions
		token   string
	}{
		{
			name: "external file",
			options: config.LoadOptions{
				APITokensPath: externalPath,
			},
			token: "file-token",
		},
		{
			name: "external YAML",
			options: config.LoadOptions{ //nolint:gosec // test fixture intentionally contains a non-secret API token
				APITokensYAML: "public:\n  - yaml-token\n",
			},
			token: "yaml-token",
		},
		{
			name: "YAML takes precedence over file",
			options: config.LoadOptions{ //nolint:gosec // test fixture intentionally contains a non-secret API token
				APITokensPath: externalPath,
				APITokensYAML: "public:\n  - yaml-token\n",
			},
			token: "yaml-token",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// When
			configuration, err := config.LoadWithOptions(configDir, test.options)
			require.NoError(t, err)
			gatewayRouter, err := router.New(*configuration)
			require.NoError(t, err)

			request := httptest.NewRequest(http.MethodGet, "/items/42", nil)
			request.RemoteAddr = "127.0.0.1:12345"
			request.Header.Set(header.APIToken, test.token)
			match, err := gatewayRouter.Route(request)

			// Then
			require.NoError(t, err)
			require.Equal(t, router.ClientType("public"), match.ClientType)
			defaultRequest := httptest.NewRequest(http.MethodGet, "/items/42", nil)
			defaultRequest.RemoteAddr = "127.0.0.1:12345"
			defaultRequest.Header.Set(header.APIToken, "token")
			_, err = gatewayRouter.Route(defaultRequest)
			require.ErrorIs(t, err, router.ErrUnknownToken)
		})
	}
}

func Test_loadOptionsFromEnv_wires_token_sources_into_production_loader(t *testing.T) {
	// Given
	configDir := testConfigDir(t, "http://127.0.0.1:1")
	externalDir := t.TempDir()
	externalPath := filepath.Join(externalDir, "tokens.yml")
	require.NoError(t, os.WriteFile(externalPath, []byte("public:\n  - file-token\n"), 0o600))
	t.Setenv("GOATWAY_API_CLIENT_TOKENS_FILE", externalPath)
	t.Setenv("GOATWAY_API_CLIENT_TOKENS_YAML", "public:\n  - yaml-token\n")

	// When
	dependencies := productionDependenciesWithOptions(loadOptionsFromEnv())
	configuration, err := dependencies.loadConfig(configDir)

	// Then
	require.NoError(t, err)
	require.Equal(t, []string{"yaml-token"}, configuration.APIClientTokens[config.ClientType("public")])
}
