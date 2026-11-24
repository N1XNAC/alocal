// Package local manages Alocal's on-disk models, settings, and MCP config.
package local

import (
	"os"
	"path/filepath"
)

// DataDir is where downloaded GGUFs and the runtime binary live.
func DataDir() string {
	if v := os.Getenv("ALOCAL_DATA_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "alocal")
	}
	return filepath.Join(home, ".local", "share", "alocal")
}

// ConfigDir is where settings.json and mcp.json live.
func ConfigDir() string {
	if v := os.Getenv("ALOCAL_CONFIG_DIR"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "alocal")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "alocal-config")
	}
	return filepath.Join(home, ".config", "alocal")
}

func ModelsDir() string  { return filepath.Join(DataDir(), "models") }
func RuntimeDir() string { return filepath.Join(DataDir(), "runtime") }
func ModelsFile() string { return filepath.Join(DataDir(), "models.json") }
func SettingsFile() string {
	return filepath.Join(ConfigDir(), "settings.json")
}
func MCPFile() string { return filepath.Join(ConfigDir(), "mcp.json") }

func ensureDir(path string) error { return os.MkdirAll(filepath.Dir(path), 0o755) }
