package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{"entries":{"prod":{"user":"ubuntu","host":"prod.example.com","port":2222,"extraArgs":["-i","/tmp/id_rsa"]}}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig returned error: %v", err)
	}

	if got := cfg.Entries["prod"].User; got != "ubuntu" {
		t.Fatalf("user mismatch: got %q", got)
	}
	if got := cfg.Entries["prod"].Port; got != 2222 {
		t.Fatalf("port mismatch: got %d", got)
	}
	if !reflect.DeepEqual(cfg.Entries["prod"].ExtraArgs, []string{"-i", "/tmp/id_rsa"}) {
		t.Fatalf("extra args mismatch: %#v", cfg.Entries["prod"].ExtraArgs)
	}
}

func TestBuildSSHClientConfig(t *testing.T) {
	entry := entry{User: "deploy", Host: "server.internal", Port: 2222, Password: "secret"}
	cfg, err := buildSSHClientConfig(entry)
	if err != nil {
		t.Fatalf("buildSSHClientConfig returned error: %v", err)
	}
	if cfg.User != "deploy" {
		t.Fatalf("user mismatch: got %q", cfg.User)
	}
	if len(cfg.Auth) != 1 {
		t.Fatalf("expected one auth method, got %d", len(cfg.Auth))
	}
	if cfg.HostKeyCallback == nil {
		t.Fatalf("expected host key callback to be set")
	}
}
