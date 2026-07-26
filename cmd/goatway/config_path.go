package main

import (
	"os"
	"path/filepath"
)

type configDirInputs struct {
	envDir         string
	flagDir        string
	flagProvided   bool
	executablePath func() (string, error)
	userConfigDir  func() (string, error)
	dirExists      func(string) bool
}

func resolveConfigDir(inputs configDirInputs) string {
	const defaultConfigDir = "./config"

	if inputs.envDir != "" {
		return inputs.envDir
	}
	if inputs.flagProvided {
		return inputs.flagDir
	}
	if inputs.dirExists(defaultConfigDir) {
		return defaultConfigDir
	}

	if executable, err := inputs.executablePath(); err == nil {
		executableConfigDir := filepath.Join(filepath.Dir(executable), "config")
		if inputs.dirExists(executableConfigDir) {
			return executableConfigDir
		}
	}
	if userConfig, err := inputs.userConfigDir(); err == nil {
		userConfigDir := filepath.Join(userConfig, "goatway")
		if inputs.dirExists(userConfigDir) {
			return userConfigDir
		}
	}
	return defaultConfigDir
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
