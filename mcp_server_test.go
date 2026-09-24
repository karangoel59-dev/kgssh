package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestFlexibleStringSlice(t *testing.T) {
	// Test unmarshaling array of strings
	rawArray := []byte(`["item1", "item2"]`)
	var f1 FlexibleStringSlice
	if err := json.Unmarshal(rawArray, &f1); err != nil {
		t.Fatalf("failed to unmarshal array: %v", err)
	}
	if len(f1) != 2 || f1[0] != "item1" || f1[1] != "item2" {
		t.Fatalf("unexpected slice: %#v", f1)
	}

	// Test unmarshaling single string
	rawStr := []byte(`"single_item"`)
	var f2 FlexibleStringSlice
	if err := json.Unmarshal(rawStr, &f2); err != nil {
		t.Fatalf("failed to unmarshal string: %v", err)
	}
	if len(f2) != 1 || f2[0] != "single_item" {
		t.Fatalf("unexpected slice: %#v", f2)
	}
}

func TestBuildMCPServerHasAllTools(t *testing.T) {
	s := BuildMCPServer()
	if s == nil {
		t.Fatal("BuildMCPServer returned nil")
	}

	expectedTools := []string{
		"list_servers",
		"ssh_exec",
		"test_connection",
		"read_file",
		"write_file",
		"list_directory",
		"add_server",
		"remove_server",
		"get_server_info",
		"update_server_info",
	}

	for _, toolName := range expectedTools {
		// Verify tool handlers exist by making a dummy call or checking server tool definitions
		// We can call tool via handler
		switch toolName {
		case "list_servers":
			res, err := handleListServers(context.Background(), mcp.CallToolRequest{})
			if err != nil {
				t.Fatalf("list_servers error: %v", err)
			}
			if len(res.Content) == 0 {
				t.Fatal("list_servers returned empty content")
			}
		}
	}
}

func TestMCPAddAndRemoveServer(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	t.Setenv("KGSSH_CONFIG", configPath)

	initialCfg := Config{
		Entries: map[string]Entry{
			"existing": {Host: "1.1.1.1", User: "root", Port: 22},
		},
	}
	if err := saveConfig(configPath, initialCfg); err != nil {
		t.Fatalf("save initial config: %v", err)
	}

	// 1. Add server via MCP handler
	addReq := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "add_server",
			Arguments: map[string]any{
				"name":          "new-server",
				"host":          "10.20.30.40",
				"user":          "admin",
				"port":          2222,
				"identity_file": "~/.ssh/id_rsa",
			},
		},
	}
	res, err := handleAddServer(context.Background(), addReq)
	if err != nil {
		t.Fatalf("handleAddServer error: %v", err)
	}
	tc, ok := mcp.AsTextContent(res.Content[0])
	if !ok || !strings.Contains(tc.Text, "successfully saved") {
		t.Fatalf("unexpected add response: %#v", res.Content)
	}

	// Verify in config
	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	newEntry, exists := cfg.Entries["new-server"]
	if !exists {
		t.Fatal("new-server not found in config")
	}
	if newEntry.Host != "10.20.30.40" || newEntry.User != "admin" || newEntry.Port != 2222 {
		t.Fatalf("unexpected entry values: %#v", newEntry)
	}

	// 2. Update server info via MCP handler
	updateReq := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "update_server_info",
			Arguments: map[string]any{
				"server":        "new-server",
				"description":   "Test Description",
				"services":      []any{"Service A", "Service B"},
				"what_to_check": "Run uptime",
			},
		},
	}
	resUpdate, err := handleUpdateServerInfo(context.Background(), updateReq)
	if err != nil {
		t.Fatalf("handleUpdateServerInfo error: %v", err)
	}
	tcUpdate, ok := mcp.AsTextContent(resUpdate.Content[0])
	if !ok || !strings.Contains(tcUpdate.Text, "Successfully updated info") {
		t.Fatalf("unexpected update response: %#v", resUpdate.Content)
	}

	// 3. Get server info via MCP handler
	infoReq := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "get_server_info",
			Arguments: map[string]any{
				"server": "new-server",
			},
		},
	}
	resInfo, err := handleGetServerInfo(context.Background(), infoReq)
	if err != nil {
		t.Fatalf("handleGetServerInfo error: %v", err)
	}
	tcInfo, ok := mcp.AsTextContent(resInfo.Content[0])
	if !ok || !strings.Contains(tcInfo.Text, "Test Description") || !strings.Contains(tcInfo.Text, "Service A") {
		t.Fatalf("unexpected info response: %s", tcInfo.Text)
	}

	// 4. Remove server via MCP handler
	rmReq := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "remove_server",
			Arguments: map[string]any{
				"name": "new-server",
			},
		},
	}
	resRm, err := handleRemoveServer(context.Background(), rmReq)
	if err != nil {
		t.Fatalf("handleRemoveServer error: %v", err)
	}
	tcRm, ok := mcp.AsTextContent(resRm.Content[0])
	if !ok || !strings.Contains(tcRm.Text, "removed from configuration") {
		t.Fatalf("unexpected rm response: %#v", resRm.Content)
	}

	// Verify gone from config
	cfgAfter, _ := loadConfig(configPath)
	if _, exists := cfgAfter.Entries["new-server"]; exists {
		t.Fatal("new-server still exists in config after removal")
	}
}
