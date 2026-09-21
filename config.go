package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config represents the kgssh configuration file structure.
type Config struct {
	Entries map[string]Entry `json:"entries"`
	KeysDir string           `json:"keysDir,omitempty"`
}

// Entry represents an individual SSH alias target.
type Entry struct {
	User        string   `json:"user"`
	Host        string   `json:"host"`
	Port        int      `json:"port,omitempty"`
	Password    string   `json:"password,omitempty"`
	Identity    string   `json:"identity,omitempty"`
	ProxyJump   string   `json:"proxyJump,omitempty"`
	Description string   `json:"description,omitempty"`
	ExtraArgs   []string `json:"extraArgs,omitempty"`
}

func resolveConfigPath() string {
	if path := os.Getenv("KGSSH_CONFIG"); path != "" {
		return path
	}
	home := userHomeDir()
	return filepath.Join(home, ".kgssh", "config.json")
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
// 1. In KeysDir with the base filename
// 2. In ~/.ssh with the base filename
// This resolves cross-environment paths like /home/user/.ssh/... on macOS /Users/user/.ssh/...
func resolveKeyPath(path, keysDir string) string {
	if path == "" {
		return ""
	}
	expanded := expandPath(path)
	if _, err := os.Stat(expanded); err == nil {
		return expanded
	}

	base := filepath.Base(path)
	if keysDir != "" {
		candidate := filepath.Join(expandPath(keysDir), base)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	if home := userHomeDir(); home != "" {
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
