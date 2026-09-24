package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FlexibleStringSlice unmarshals JSON arrays of strings or single strings into []string.
type FlexibleStringSlice []string

func (f *FlexibleStringSlice) UnmarshalJSON(data []byte) error {
	var slice []string
	if err := json.Unmarshal(data, &slice); err == nil {
		*f = slice
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if s != "" {
			*f = []string{s}
		} else {
			*f = []string{}
		}
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err == nil {
		var res []string
		for k, v := range m {
			res = append(res, fmt.Sprintf("%s: %v", k, v))
		}
		*f = res
		return nil
	}
	return nil
}

// ServerInfo represents detailed architecture and operational metadata for a server.
type ServerInfo struct {
	Description string              `json:"description,omitempty"`
	Services    FlexibleStringSlice `json:"services,omitempty"`
	WhatToCheck string              `json:"what_to_check,omitempty"`
	LogPaths    FlexibleStringSlice `json:"log_paths,omitempty"`
	Notes       string              `json:"notes,omitempty"`
	UpdatedAt   string              `json:"updated_at,omitempty"`
}

// Config represents the kgssh configuration file structure.
type Config struct {
	Entries map[string]Entry `json:"entries"`
	KeysDir string           `json:"keysDir,omitempty"`
}

// Entry represents an individual SSH alias target.
type Entry struct {
	User        string      `json:"user"`
	Host        string      `json:"host"`
	Port        int         `json:"port,omitempty"`
	Password    string      `json:"password,omitempty"`
	Identity    string      `json:"identity,omitempty"`
	ProxyJump   string      `json:"proxyJump,omitempty"`
	Description string      `json:"description,omitempty"`
	ExtraArgs   []string    `json:"extraArgs,omitempty"`
	Info        *ServerInfo `json:"info,omitempty"`
}

func resolveConfigPath() string {
	if path := os.Getenv("KGSSH_CONFIG"); path != "" {
		return expandPath(path)
	}
	if path := os.Getenv("SSH_MCP_CONFIG"); path != "" {
		return expandPath(path)
	}
	home := userHomeDir()
	primary := filepath.Join(home, ".kgssh", "config.json")
	if _, err := os.Stat(primary); err == nil {
		return primary
	}
	legacy := filepath.Join(home, ".config", "ssh-mcp", "servers.json")
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return primary
}

func loadConfig(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	if cfg.Entries == nil {
		cfg.Entries = map[string]Entry{}
	}
	return cfg, nil
}

func saveConfig(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func expandPath(path string) string {
	if path == "~" {
		if home := userHomeDir(); home != "" {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		if home := userHomeDir(); home != "" {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return home
	}
	return os.Getenv("HOME")
}

// DisplayPath formats an absolute path into a portable ~ path if located within the user's home dir.
func DisplayPath(path string) string {
	if path == "" {
		return ""
	}
	home := userHomeDir()
	if home != "" && strings.HasPrefix(path, home) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

// resolveKeyPath resolves an identity path.
// If the path does not exist on disk as given, it checks:
// 1. Cross-platform translation (/home/<user>/.ssh/... -> ~/.ssh/...)
// 2. In KeysDir with the base filename
// 3. In ~/.ssh with the base filename
func resolveKeyPath(path, keysDir string) string {
	if path == "" {
		return ""
	}
	expanded := expandPath(path)
	if _, err := os.Stat(expanded); err == nil {
		return expanded
	}

	home := userHomeDir()

	// Cross-environment translation (e.g. /home/<user>/.ssh/... on macOS /Users/<user>/.ssh/...)
	if strings.HasPrefix(path, "/home/") && home != "" {
		parts := strings.Split(path, "/")
		for i, part := range parts {
			if part == ".ssh" && i+1 < len(parts) {
				rel := strings.Join(parts[i:], "/")
				candidate := filepath.Join(home, rel)
				if _, err := os.Stat(candidate); err == nil {
					return candidate
				}
			}
		}
	}

	base := filepath.Base(path)
	if keysDir != "" {
		kd := expandPath(keysDir)
		if strings.HasPrefix(keysDir, "/home/") && home != "" {
			parts := strings.Split(keysDir, "/")
			for i, part := range parts {
				if part == ".ssh" {
					rel := strings.Join(parts[i:], "/")
					kd = filepath.Join(home, rel)
					break
				}
			}
		}
		candidate := filepath.Join(kd, base)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	if home != "" {
		candidate := filepath.Join(home, ".ssh", base)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	return expanded
}

// ResolvedIdentity returns the resolved identity key path for an Entry.
func (e Entry) ResolvedIdentity(keysDir string) string {
	if e.Identity != "" {
		return resolveKeyPath(e.Identity, keysDir)
	}
	for i := 0; i < len(e.ExtraArgs); i++ {
		if (e.ExtraArgs[i] == "-i" || e.ExtraArgs[i] == "--identity-file") && i+1 < len(e.ExtraArgs) {
			return resolveKeyPath(e.ExtraArgs[i+1], keysDir)
		}
	}
	return ""
}

// GetDescription returns the alias description, falling back to ServerInfo description if available.
func (e Entry) GetDescription() string {
	if e.Description != "" {
		return e.Description
	}
	if e.Info != nil && e.Info.Description != "" {
		return e.Info.Description
	}
	return ""
}
