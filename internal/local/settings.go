package local

import (
	"encoding/json"
	"errors"
	"os"
)

// Settings are persisted chat/runtime preferences.
type Settings struct {
	Context      int     `json:"context"`
	GPULayers    int     `json:"gpu_layers"`
	Temperature  float64 `json:"temperature"`
	SystemPrompt string  `json:"system_prompt"`
	MaxTokens    int     `json:"max_tokens"`
	RuntimePath  string  `json:"runtime_path,omitempty"`
	LastModelID  string  `json:"last_model_id,omitempty"`
}

// DefaultSettings returns sane starting values.
func DefaultSettings() Settings {
	return Settings{
		Context:     8192,
		GPULayers:   -1,
		Temperature: 0.7,
		MaxTokens:   512,
	}
}

// LoadSettings merges stored values over defaults.
func LoadSettings() (Settings, error) {
	s := DefaultSettings()
	data, err := os.ReadFile(SettingsFile())
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, err
	}
	if s.Context <= 0 {
		s.Context = DefaultSettings().Context
	}
	if s.Temperature < 0 || s.Temperature > 2 {
		s.Temperature = DefaultSettings().Temperature
	}
	if s.MaxTokens <= 0 {
		s.MaxTokens = DefaultSettings().MaxTokens
	}
	return s, nil
}

// Save persists settings.
func (s Settings) Save() error {
	if err := ensureDir(SettingsFile()); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(SettingsFile(), append(data, '\n'), 0o644)
}
