package local

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// UpdateSource records where `alocal update` should fetch releases. The
// installers write this file so users do not have to repeat ALOCAL_REPO.
type UpdateSource struct {
	Repo    string `json:"repo,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
}

// UpdateFile is the persisted update source path.
func UpdateFile() string { return filepath.Join(ConfigDir(), "update.json") }

// LoadUpdateSource returns the persisted release source, if any.
func LoadUpdateSource() (UpdateSource, error) {
	data, err := os.ReadFile(UpdateFile())
	if errors.Is(err, os.ErrNotExist) {
		return UpdateSource{}, nil
	}
	if err != nil {
		return UpdateSource{}, err
	}
	var src UpdateSource
	if err := json.Unmarshal(data, &src); err != nil {
		return UpdateSource{}, err
	}
	src.Repo = strings.TrimSpace(src.Repo)
	src.BaseURL = strings.TrimRight(strings.TrimSpace(src.BaseURL), "/")
	return src, nil
}

// Save persists the release source.
func (s UpdateSource) Save() error {
	if err := ensureDir(UpdateFile()); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(UpdateFile(), append(data, '\n'), 0o644)
}

// Valid reports whether a source has enough information to fetch an update.
func (s UpdateSource) Valid() bool { return s.Repo != "" || s.BaseURL != "" }
