package main

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func seedExistingEntry(t *testing.T) string {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("KGSSH_CONFIG", configPath)
	t.Setenv("HOME", t.TempDir()) // keep alias sync out of the real ~/.kgssh
	err := saveConfig(configPath, Config{Entries: map[string]Entry{
		"prod": {
			User:      "ubuntu",
			Host:      "old.example.com",
			Port:      2200,
			ExtraArgs: []string{"-o", "ServerAliveInterval=30"},
			Info:      &ServerInfo{Description: "API node", Services: FlexibleStringSlice{"nginx"}},
		},
	}})
	if err != nil {
		t.Fatalf("save config: %v", err)
	}
	return configPath
}

func assertPreserved(t *testing.T, got Entry, wantHost string) {
	t.Helper()
	if got.Host != wantHost {
		t.Errorf("host = %q, want %q", got.Host, wantHost)
	}
	if got.User != "ubuntu" {
		t.Errorf("user = %q, want preserved %q", got.User, "ubuntu")
	}
	if got.Port != 2200 {
		t.Errorf("port = %d, want preserved 2200", got.Port)
	}
	if !reflect.DeepEqual(got.ExtraArgs, []string{"-o", "ServerAliveInterval=30"}) {
		t.Errorf("extra args = %#v, want preserved", got.ExtraArgs)
	}
	if got.Info == nil || got.Info.Description != "API node" {
		t.Errorf("info = %#v, want preserved runbook", got.Info)
	}
}

func TestCLIAddUpdatePreservesUnspecifiedFields(t *testing.T) {
	configPath := seedExistingEntry(t)

	cmd := newAddCommand()
	cmd.SetArgs([]string{"prod", "new.example.com", "--no-sync"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	assertPreserved(t, cfg.Entries["prod"], "new.example.com")
}

func TestMCPAddServerUpdatePreservesUnspecifiedFields(t *testing.T) {
	configPath := seedExistingEntry(t)

	_, err := handleAddServer(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "add_server",
			Arguments: map[string]any{"name": "prod", "host": "new.example.com"},
		},
	})
	if err != nil {
		t.Fatalf("handleAddServer: %v", err)
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	assertPreserved(t, cfg.Entries["prod"], "new.example.com")
}

func TestMCPAddServerNewEntryDefaults(t *testing.T) {
	configPath := seedExistingEntry(t)

	_, err := handleAddServer(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "add_server",
			Arguments: map[string]any{"name": "fresh", "host": "10.0.0.9"},
		},
	})
	if err != nil {
		t.Fatalf("handleAddServer: %v", err)
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if e := cfg.Entries["fresh"]; e.User != "root" || e.Port != 22 {
		t.Fatalf("new entry defaults: user=%q port=%d, want root/22", e.User, e.Port)
	}
}
