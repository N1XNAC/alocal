package local

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Model is a downloaded GGUF tracked by Alocal.
type Model struct {
	ID        string    `json:"id"` // owner/repo/filename.gguf
	Repo      string    `json:"repo"`
	File      string    `json:"file"`
	Path      string    `json:"path"`
	SizeBytes int64     `json:"size_bytes"`
	Quant     string    `json:"quant,omitempty"`
	Family    string    `json:"family,omitempty"`
	AddedAt   time.Time `json:"added_at"`
}

// ModelStore is the contents of models.json.
type ModelStore struct {
	Version int     `json:"version"`
	Models  []Model `json:"models"`
}

// LoadModels returns the local registry, treating a missing file as empty.
func LoadModels() (*ModelStore, error) {
	path := ModelsFile()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &ModelStore{Version: 1}, nil
	}
	if err != nil {
		return nil, err
	}
	var store ModelStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, err
	}
	if store.Version == 0 {
		store.Version = 1
	}
	return &store, nil
}

// SaveModels writes the registry atomically.
func (s *ModelStore) Save() error {
	if err := ensureDir(ModelsFile()); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := ModelsFile() + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, ModelsFile())
}

// Add inserts or replaces a model and persists the registry.
func (s *ModelStore) Add(m Model) error {
	if m.AddedAt.IsZero() {
		m.AddedAt = time.Now()
	}
	for i := range s.Models {
		if s.Models[i].ID == m.ID {
			s.Models[i] = m
			return s.Save()
		}
	}
	s.Models = append(s.Models, m)
	return s.Save()
}

// Remove deletes a model from the registry (it does not delete the file).
func (s *ModelStore) Remove(id string) (bool, error) {
	for i, m := range s.Models {
		if m.ID == id {
			s.Models = append(s.Models[:i], s.Models[i+1:]...)
			return true, s.Save()
		}
	}
	return false, nil
}

// Find resolves an exact ID, a unique ID suffix, or a file path.
func (s *ModelStore) Find(id string) (Model, bool) {
	for _, m := range s.Models {
		if m.ID == id || m.Path == id {
			return m, true
		}
	}
	var match Model
	found := false
	for _, m := range s.Models {
		if strings.HasSuffix(m.ID, id) || strings.EqualFold(filepath.Base(m.Path), id) {
			if found {
				return Model{}, false // ambiguous
			}
			match, found = m, true
		}
	}
	return match, found
}
