package local

import (
	"encoding/json"
	"errors"
	"os"
)

// MCPServer describes a local MCP server Alocal can launch.
type MCPServer struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled bool              `json:"enabled"`
}

// MCPConfig is the contents of mcp.json.
type MCPConfig struct {
	Version int         `json:"version"`
	Servers []MCPServer `json:"servers"`
}

// LoadMCP returns the MCP config, treating a missing file as empty.
func LoadMCP() (*MCPConfig, error) {
	data, err := os.ReadFile(MCPFile())
	if errors.Is(err, os.ErrNotExist) {
		return &MCPConfig{Version: 1}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg MCPConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	return &cfg, nil
}

// Save persists the MCP config.
func (c *MCPConfig) Save() error {
	if err := ensureDir(MCPFile()); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(MCPFile(), append(data, '\n'), 0o644)
}

// AddServer inserts or replaces a server by name.
func (c *MCPConfig) AddServer(s MCPServer) error {
	for i := range c.Servers {
		if c.Servers[i].Name == s.Name {
			c.Servers[i] = s
			return c.Save()
		}
	}
	c.Servers = append(c.Servers, s)
	return c.Save()
}

// RemoveServer deletes a server by name.
func (c *MCPConfig) RemoveServer(name string) (bool, error) {
	for i, s := range c.Servers {
		if s.Name == name {
			c.Servers = append(c.Servers[:i], c.Servers[i+1:]...)
			return true, c.Save()
		}
	}
	return false, nil
}
