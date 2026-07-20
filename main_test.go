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

func TestBuildSSHArgs(t *testing.T) {
	entry := entry{User: "deploy", Host: "server.internal", Port: 2222, ExtraArgs: []string{"-i", "/tmp/id_rsa"}}
	args := buildSSHArgs(entry)
	want := []string{"-o", "BatchMode=yes", "-p", "2222", "-i", "/tmp/id_rsa", "deploy@server.internal"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("unexpected args: %#v", args)
	}
}
