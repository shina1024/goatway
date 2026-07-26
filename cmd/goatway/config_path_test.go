package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_resolveConfigDir_applies_precedence(t *testing.T) {
	tests := []struct {
		name         string
		envDir       string
		flagDir      string
		flagProvided bool
		executable   string
		userConfig   string
		existingDirs []string
		want         string
	}{
		{
			name:         "environment wins over every candidate",
			envDir:       "/env/config",
			flagDir:      "/flag/config",
			flagProvided: true,
			executable:   "/app/goatway",
			userConfig:   "/user/config",
			existingDirs: []string{"./config", filepath.Join("/app", "config"), filepath.Join("/user/config", "goatway")},
			want:         "/env/config",
		},
		{
			name:         "explicit config flag wins over working directory",
			flagDir:      "/flag/config",
			flagProvided: true,
			executable:   "/app/goatway",
			userConfig:   "/user/config",
			existingDirs: []string{"./config", filepath.Join("/app", "config"), filepath.Join("/user/config", "goatway")},
			want:         "/flag/config",
		},
		{
			name:         "working directory config is first discovered directory",
			executable:   "/app/goatway",
			userConfig:   "/user/config",
			existingDirs: []string{"./config", filepath.Join("/app", "config"), filepath.Join("/user/config", "goatway")},
			want:         "./config",
		},
		{
			name:         "executable relative config follows working directory",
			executable:   "/app/goatway",
			userConfig:   "/user/config",
			existingDirs: []string{filepath.Join("/app", "config"), filepath.Join("/user/config", "goatway")},
			want:         filepath.Join("/app", "config"),
		},
		{
			name:         "user config follows executable relative config",
			executable:   "/app/goatway",
			userConfig:   "/user/config",
			existingDirs: []string{filepath.Join("/user/config", "goatway")},
			want:         filepath.Join("/user/config", "goatway"),
		},
		{
			name:       "working directory is the ultimate fallback",
			executable: "/app/goatway",
			userConfig: "/user/config",
			want:       "./config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			existing := make(map[string]bool, len(tt.existingDirs))
			for _, directory := range tt.existingDirs {
				existing[directory] = true
			}

			// When
			got := resolveConfigDir(configDirInputs{
				envDir:       tt.envDir,
				flagDir:      tt.flagDir,
				flagProvided: tt.flagProvided,
				executablePath: func() (string, error) {
					return tt.executable, nil
				},
				userConfigDir: func() (string, error) {
					return tt.userConfig, nil
				},
				dirExists: func(path string) bool {
					return existing[path]
				},
			})

			// Then
			require.Equal(t, tt.want, got)
		})
	}
}
