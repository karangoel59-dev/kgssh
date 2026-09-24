package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Helper getters for robust argument extraction from CallToolRequest
func argString(r mcp.CallToolRequest, key, def string) string {
	args := r.GetArguments()
	if v, ok := args[key]; ok && v != nil {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
	return def
}

func argInt(r mcp.CallToolRequest, key string, def int) int {
	args := r.GetArguments()
	if v, ok := args[key]; ok && v != nil {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case int64:
			return int(n)
		case string:
			if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
				return i
			}
		}
	}
	return def
}

func argBool(r mcp.CallToolRequest, key string, def bool) bool {
	args := r.GetArguments()
	if v, ok := args[key]; ok && v != nil {
		if b, ok := v.(bool); ok {
			return b
		}
		if s, ok := v.(string); ok {
			return strings.ToLower(strings.TrimSpace(s)) == "true"
		}
	}
	return def
}

func argStringSlice(r mcp.CallToolRequest, key string) []string {
	args := r.GetArguments()
	if v, ok := args[key]; ok && v != nil {
		if sl, ok := v.([]any); ok {
			var res []string
			for _, item := range sl {
				if item != nil {
					res = append(res, fmt.Sprintf("%v", item))
				}
			}
			return res
		}
		if sl, ok := v.([]string); ok {
			return sl
		}
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return []string{strings.TrimSpace(s)}
		}
	}
	return nil
}

// BuildMCPServer constructs the MCP server with all 10 registered tools.
func BuildMCPServer() *server.MCPServer {
	s := server.NewMCPServer(
		"kgssh",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	// 1. list_servers
	s.AddTool(mcp.NewTool("list_servers",
		mcp.WithDescription("List all configured SSH servers, their hosts, ports, users, and authentication modes."),
	), handleListServers)

	// 2. ssh_exec
	s.AddTool(mcp.NewTool("ssh_exec",
		mcp.WithDescription("Execute a shell command on a configured remote SSH server.\n\nArgs:\n    server: The name of the configured server (e.g. 'agentic-prod', 'prod1', 'backend-stage')\n    command: The shell command to execute (e.g. 'docker ps', 'df -h', 'uptime')\n    timeout: Execution timeout in seconds (default: 60)\n    working_dir: Optional remote directory to execute the command in\n    pty: Request a pseudo-terminal (useful for interactive tools or commands needing a TTY)\n"),
		mcp.WithString("server", mcp.Required(), mcp.Description("The name of the configured server")),
		mcp.WithString("command", mcp.Required(), mcp.Description("The shell command to execute")),
		mcp.WithInteger("timeout", mcp.Description("Execution timeout in seconds (default: 60)")),
		mcp.WithString("working_dir", mcp.Description("Optional remote directory to execute the command in")),
		mcp.WithBoolean("pty", mcp.Description("Request a pseudo-terminal (useful for interactive tools or commands needing a TTY)")),
	), handleSSHExec)

	// 3. test_connection
	s.AddTool(mcp.NewTool("test_connection",
		mcp.WithDescription("Test SSH connectivity and measure latency to one or all configured servers.\n\nArgs:\n    server: Name of a specific server to test, or 'all' to test all configured servers (default: 'all')\n    timeout: Connection timeout in seconds per server (default: 5)\n"),
		mcp.WithString("server", mcp.Description("Name of a specific server to test, or 'all' to test all configured servers (default: 'all')")),
		mcp.WithInteger("timeout", mcp.Description("Connection timeout in seconds per server (default: 5)")),
	), handleTestConnection)

	// 4. read_file
	s.AddTool(mcp.NewTool("read_file",
		mcp.WithDescription("Read the contents of a remote file over SFTP.\n\nArgs:\n    server: Name of configured server\n    remote_path: Path of file on remote server (e.g. '/etc/hosts' or 'app.log')\n    max_bytes: Maximum number of bytes to return (default: 50,000 to prevent context blowout)\n"),
		mcp.WithString("server", mcp.Required(), mcp.Description("Name of configured server")),
		mcp.WithString("remote_path", mcp.Required(), mcp.Description("Path of file on remote server")),
		mcp.WithInteger("max_bytes", mcp.Description("Maximum number of bytes to return (default: 50,000 to prevent context blowout)")),
	), handleReadFile)

	// 5. write_file
	s.AddTool(mcp.NewTool("write_file",
		mcp.WithDescription("Write content to a file on a remote server over SFTP.\n\nArgs:\n    server: Name of configured server\n    remote_path: Target path on remote server\n    content: Text content to write into the file\n"),
		mcp.WithString("server", mcp.Required(), mcp.Description("Name of configured server")),
		mcp.WithString("remote_path", mcp.Required(), mcp.Description("Target path on remote server")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Text content to write into the file")),
	), handleWriteFile)

	// 6. list_directory
	s.AddTool(mcp.NewTool("list_directory",
		mcp.WithDescription("List contents of a remote directory with file details over SFTP.\n\nArgs:\n    server: Name of configured server\n    remote_path: Directory path on remote server (default: '.')\n"),
		mcp.WithString("server", mcp.Required(), mcp.Description("Name of configured server")),
		mcp.WithString("remote_path", mcp.Description("Directory path on remote server (default: '.')")),
	), handleListDirectory)

	// 7. add_server
	s.AddTool(mcp.NewTool("add_server",
		mcp.WithDescription("Add or update an SSH server configuration profile.\n\nArgs:\n    name: Unique alias identifier for the server (e.g. 'staging-api')\n    host: IP address or hostname\n    user: SSH user (default: 'root')\n    port: SSH port (default: 22)\n    identity_file: Optional path to private key (e.g. '~/.ssh/id_rsa')\n    password: Optional SSH password\n    extra_args: Optional list of additional SSH arguments (e.g. ['-o', 'StrictHostKeyChecking=no'])\n"),
		mcp.WithString("name", mcp.Required(), mcp.Description("Unique alias identifier for the server")),
		mcp.WithString("host", mcp.Required(), mcp.Description("IP address or hostname")),
		mcp.WithString("user", mcp.Description("SSH user (default: 'root')")),
		mcp.WithInteger("port", mcp.Description("SSH port (default: 22)")),
		mcp.WithString("identity_file", mcp.Description("Optional path to private key (e.g. '~/.ssh/id_rsa')")),
		mcp.WithString("password", mcp.Description("Optional SSH password")),
		mcp.WithArray("extra_args", mcp.Description("Optional list of additional SSH arguments"), mcp.WithStringItems()),
	), handleAddServer)

	// 8. remove_server
	s.AddTool(mcp.NewTool("remove_server",
		mcp.WithDescription("Remove an SSH server from configuration.\n\nArgs:\n    name: Alias of server to remove\n"),
		mcp.WithString("name", mcp.Required(), mcp.Description("Alias of server to remove")),
	), handleRemoveServer)

	// 9. get_server_info
	s.AddTool(mcp.NewTool("get_server_info",
		mcp.WithDescription("Retrieve operational context, architecture description, running services,\nkey log paths, and troubleshooting checklists for a configured SSH server (or all servers).\n\nArgs:\n    server: Name of configured server (e.g. 'agentic-stage'). If 'all' or empty, returns an overview summary of all configured servers.\n"),
		mcp.WithString("server", mcp.Description("Name of configured server (e.g. 'agentic-stage'). If 'all' or empty, returns an overview summary of all configured servers.")),
	), handleGetServerInfo)

	// 10. update_server_info
	s.AddTool(mcp.NewTool("update_server_info",
		mcp.WithDescription("Update documentation, architecture info, key services, log paths, and troubleshooting checklists for a configured server.\nPreserves connection details and only modifies fields that are explicitly provided.\n\nArgs:\n    server: Name of the configured server to update (e.g. 'agentic-stage')\n    description: High-level overview of the server's role and architecture\n    services: List of key services running on this server (e.g. ['Gunicorn (Django API)', 'Celery (8 workers)', 'Qdrant'])\n    what_to_check: Checklist or instructions for diagnosing issues (commands, metrics, health checks)\n    log_paths: List of important log file paths and directories to monitor\n    notes: Important operational caveats, port mappings, credentials, or environment details\n"),
		mcp.WithString("server", mcp.Required(), mcp.Description("Name of the configured server to update (e.g. 'agentic-stage')")),
		mcp.WithString("description", mcp.Description("High-level overview of the server's role and architecture")),
		mcp.WithArray("services", mcp.Description("List of key services running on this server"), mcp.WithStringItems()),
		mcp.WithString("what_to_check", mcp.Description("Checklist or instructions for diagnosing issues")),
		mcp.WithArray("log_paths", mcp.Description("List of important log file paths and directories to monitor"), mcp.WithStringItems()),
		mcp.WithString("notes", mcp.Description("Important operational caveats, port mappings, credentials, or environment details")),
	), handleUpdateServerInfo)

	return s
}

// RunMCPServer starts the MCP server listening on stdio.
func RunMCPServer() error {
	s := BuildMCPServer()
	return server.ServeStdio(s)
}

func handleListServers(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg, err := loadConfig(resolveConfigPath())
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to load config: %v", err)), nil
	}
	if len(cfg.Entries) == 0 {
		return mcp.NewToolResultText("No SSH servers currently configured in settings."), nil
	}

	keysDir := cfg.KeysDir
	if keysDir == "" {
		keysDir = "~/.ssh"
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("### Configured SSH Servers (%d total)", len(cfg.Entries)))
	lines = append(lines, fmt.Sprintf("**Keys Directory:** `%s`", keysDir))
	lines = append(lines, "")
	lines = append(lines, "| Server Name | User@Host:Port | Auth Method | Extra Args |")
	lines = append(lines, "| :--- | :--- | :--- | :--- |")

	names := make([]string, 0, len(cfg.Entries))
	for name := range cfg.Entries {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		entry := cfg.Entries[name]
		user := entry.User
		if user == "" {
			user = "root"
		}
		host := entry.Host
		if host == "" {
			host = "unknown"
		}
		port := entry.Port
		if port <= 0 {
			port = 22
		}

		authDesc := ""
		if entry.Password != "" {
			authDesc = "Password"
		} else {
			keyPath := ""
			if entry.Identity != "" {
				keyPath = entry.Identity
			} else {
				for i := 0; i < len(entry.ExtraArgs); i++ {
					if (entry.ExtraArgs[i] == "-i" || entry.ExtraArgs[i] == "--identity-file") && i+1 < len(entry.ExtraArgs) {
						keyPath = entry.ExtraArgs[i+1]
						break
					}
				}
			}
			if keyPath != "" {
				resolved := resolveKeyPath(keyPath, cfg.KeysDir)
				exists := false
				if _, err := os.Stat(resolved); err == nil {
					exists = true
				}
				status := "missing"
				if exists {
					status = "found"
				}
				authDesc = fmt.Sprintf("Key: `%s` (%s)", filepath.Base(keyPath), status)
			} else {
				authDesc = "Auto-detect key / agent"
			}
		}

		argsStr := "-"
		if len(entry.ExtraArgs) > 0 {
			argsStr = strings.Join(entry.ExtraArgs, " ")
		}

		lines = append(lines, fmt.Sprintf("| **%s** | `%s@%s:%d` | %s | `%s` |", name, user, host, port, authDesc, argsStr))
	}

	return mcp.NewToolResultText(strings.Join(lines, "\n")), nil
}

func handleSSHExec(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	server := argString(request, "server", "")
	command := argString(request, "command", "")
	timeoutSec := argInt(request, "timeout", 60)
	workingDir := argString(request, "working_dir", "")
	pty := argBool(request, "pty", false)

	if server == "" || command == "" {
		return mcp.NewToolResultError("Both 'server' and 'command' parameters are required"), nil
	}

	fullCmd := command
	if workingDir != "" {
		fullCmd = fmt.Sprintf("cd %s && %s", workingDir, command)
	}

	timeout := time.Duration(timeoutSec) * time.Second

	var result *ExecResult
	client, err := globalPool.GetClient(server, 10*time.Second)
	if err == nil {
		result, err = RunRemoteCommand(client, fullCmd, timeout, pty)
	}

	if err != nil {
		// Stale connection or failed session, retry with fresh connection
		globalPool.Close(server)
		client, err2 := globalPool.GetClient(server, 10*time.Second)
		if err2 == nil {
			result, err = RunRemoteCommand(client, fullCmd, timeout, pty)
		}
	}

	if err != nil {
		return mcp.NewToolResultText(fmt.Sprintf("❌ Execution failed on '%s': %v", server, err)), nil
	}

	statusEmoji := "✅"
	if result.ExitCode != 0 {
		statusEmoji = fmt.Sprintf("⚠️ (Exit Code: %d)", result.ExitCode)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### %s Command Execution on `%s`\n", statusEmoji, server))
	sb.WriteString(fmt.Sprintf("- **Command:** `%s`\n", command))
	sb.WriteString(fmt.Sprintf("- **Exit Code:** `%d`\n", result.ExitCode))
	sb.WriteString(fmt.Sprintf("- **Duration:** %.3fs\n", result.Duration))
	if workingDir != "" {
		sb.WriteString(fmt.Sprintf("- **Working Dir:** `%s`\n", workingDir))
	}

	if strings.TrimSpace(result.Stdout) != "" {
		sb.WriteString(fmt.Sprintf("\n#### STDOUT\n```text\n%s\n```", strings.TrimSpace(result.Stdout)))
	} else {
		sb.WriteString("\n#### STDOUT\n*(empty)*")
	}

	if strings.TrimSpace(result.Stderr) != "" {
		sb.WriteString(fmt.Sprintf("\n#### STDERR\n```text\n%s\n```", strings.TrimSpace(result.Stderr)))
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func handleTestConnection(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	server := argString(request, "server", "all")
	timeoutSec := argInt(request, "timeout", 5)
	if timeoutSec <= 0 {
		timeoutSec = 5
	}
	timeout := time.Duration(timeoutSec) * time.Second

	cfg, err := loadConfig(resolveConfigPath())
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to load config: %v", err)), nil
	}
	if len(cfg.Entries) == 0 {
		return mcp.NewToolResultText("No servers configured."), nil
	}

	var targets []string
	if strings.ToLower(server) == "all" || server == "" {
		for name := range cfg.Entries {
			targets = append(targets, name)
		}
		sort.Strings(targets)
	} else {
		targets = []string{server}
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("### SSH Connection Test Results (%d tested)", len(targets)))
	lines = append(lines, "")
	lines = append(lines, "| Server | Target | Status | Latency | Hostname / Info |")
	lines = append(lines, "| :--- | :--- | :--- | :--- | :--- |")

	for _, target := range targets {
		entry, exists := cfg.Entries[target]
		if !exists {
			lines = append(lines, fmt.Sprintf("| **%s** | - | ❌ Unknown | - | Server not found in configuration |", target))
			continue
		}

		user := entry.User
		if user == "" {
			user = "root"
		}
		port := entry.Port
		if port <= 0 {
			port = 22
		}

		startT := time.Now()
		globalPool.Close(target) // Ensure fresh connection
		client, err := globalPool.GetClient(target, timeout)
		if err != nil {
			lat := float64(time.Since(startT).Milliseconds())
			errMsg := strings.ReplaceAll(strings.ReplaceAll(err.Error(), "|", "\\|"), "\n", " ")
			lines = append(lines, fmt.Sprintf("| **%s** | `%s@%s:%d` | ❌ Failed | %.1fms | %s |", target, user, entry.Host, port, lat, errMsg))
			continue
		}

		res, err := RunRemoteCommand(client, "hostname", timeout, false)
		lat := float64(time.Since(startT).Milliseconds())
		if err != nil {
			errMsg := strings.ReplaceAll(strings.ReplaceAll(err.Error(), "|", "\\|"), "\n", " ")
			lines = append(lines, fmt.Sprintf("| **%s** | `%s@%s:%d` | ❌ Failed | %.1fms | %s |", target, user, entry.Host, port, lat, errMsg))
		} else {
			hn := strings.TrimSpace(res.Stdout)
			if hn == "" {
				hn = "ok"
			}
			lines = append(lines, fmt.Sprintf("| **%s** | `%s@%s:%d` | ✅ Connected | %.1fms | `%s` |", target, user, entry.Host, port, lat, hn))
		}
	}

	return mcp.NewToolResultText(strings.Join(lines, "\n")), nil
}

func handleReadFile(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	server := argString(request, "server", "")
	remotePath := argString(request, "remote_path", "")
	maxBytes := argInt(request, "max_bytes", 50000)

	if server == "" || remotePath == "" {
		return mcp.NewToolResultError("Parameters 'server' and 'remote_path' are required"), nil
	}

	client, err := globalPool.GetClient(server, 10*time.Second)
	if err != nil {
		return mcp.NewToolResultText(fmt.Sprintf("❌ Failed to connect to '%s': %v", server, err)), nil
	}

	content, size, perm, truncated, err := ReadFileSFTP(client, remotePath, maxBytes)
	if err != nil {
		return mcp.NewToolResultText(fmt.Sprintf("❌ Failed to read file `%s` on `%s`: %v", remotePath, server, err)), nil
	}

	notice := ""
	if truncated {
		notice = fmt.Sprintf("\n*(Truncated to first %d bytes. Full file size: %d bytes)*", maxBytes, size)
	}

	out := fmt.Sprintf("### File Content: `%s` on `%s`\n- **Size:** %d bytes\n- **Permissions:** %s\n%s\n```text\n%s\n```", remotePath, server, size, perm, notice, content)
	return mcp.NewToolResultText(out), nil
}

func handleWriteFile(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	server := argString(request, "server", "")
	remotePath := argString(request, "remote_path", "")
	content := argString(request, "content", "")

	if server == "" || remotePath == "" {
		return mcp.NewToolResultError("Parameters 'server' and 'remote_path' are required"), nil
	}

	client, err := globalPool.GetClient(server, 10*time.Second)
	if err != nil {
		return mcp.NewToolResultText(fmt.Sprintf("❌ Failed to connect to '%s': %v", server, err)), nil
	}

	n, err := WriteFileSFTP(client, remotePath, content)
	if err != nil {
		return mcp.NewToolResultText(fmt.Sprintf("❌ Failed to write file `%s` on `%s`: %v", remotePath, server, err)), nil
	}

	out := fmt.Sprintf("✅ Successfully wrote to `%s` on `%s`.\n- **Bytes Written:** %d\n", remotePath, server, n)
	return mcp.NewToolResultText(out), nil
}

func handleListDirectory(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	server := argString(request, "server", "")
	remotePath := argString(request, "remote_path", ".")
	if remotePath == "" {
		remotePath = "."
	}

	if server == "" {
		return mcp.NewToolResultError("Parameter 'server' is required"), nil
	}

	client, err := globalPool.GetClient(server, 10*time.Second)
	if err != nil {
		return mcp.NewToolResultText(fmt.Sprintf("❌ Failed to connect to '%s': %v", server, err)), nil
	}

	items, err := ListDirectorySFTP(client, remotePath)
	if err != nil {
		return mcp.NewToolResultText(fmt.Sprintf("❌ Failed to list directory `%s` on `%s`: %v", remotePath, server, err)), nil
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("### Directory Listing: `%s` on `%s` (%d items)", remotePath, server, len(items)))
	lines = append(lines, "")
	lines = append(lines, "| Name | Type | Size | Permissions | Last Modified |")
	lines = append(lines, "| :--- | :--- | :--- | :--- | :--- |")

	for _, it := range items {
		itemType := "📄 File"
		if it.IsDir {
			itemType = "📁 Directory"
		}
		lines = append(lines, fmt.Sprintf("| **%s** | %s | %d B | `%s` | %s |", it.Name, itemType, it.Size, it.Perm, it.ModTime))
	}

	return mcp.NewToolResultText(strings.Join(lines, "\n")), nil
}

func handleAddServer(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := argString(request, "name", "")
	host := argString(request, "host", "")
	user := argString(request, "user", "root")
	if user == "" {
		user = "root"
	}
	port := argInt(request, "port", 22)
	if port <= 0 {
		port = 22
	}
	identityFile := argString(request, "identity_file", "")
	password := argString(request, "password", "")
	extraArgs := argStringSlice(request, "extra_args")

	if name == "" || host == "" {
		return mcp.NewToolResultError("Parameters 'name' and 'host' are required"), nil
	}

	cfgPath := resolveConfigPath()
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		cfg = Config{Entries: make(map[string]Entry)}
	}
	if cfg.Entries == nil {
		cfg.Entries = make(map[string]Entry)
	}

	entry := Entry{
		User:      user,
		Host:      host,
		Port:      port,
		Password:  password,
		Identity:  identityFile,
		ExtraArgs: extraArgs,
	}

	if existing, exists := cfg.Entries[name]; exists {
		if entry.Identity == "" && existing.Identity != "" {
			entry.Identity = existing.Identity
		}
		if entry.Password == "" && existing.Password != "" {
			entry.Password = existing.Password
		}
		if entry.ProxyJump == "" && existing.ProxyJump != "" {
			entry.ProxyJump = existing.ProxyJump
		}
		if entry.Description == "" && existing.Description != "" {
			entry.Description = existing.Description
		}
		if len(entry.ExtraArgs) == 0 && len(existing.ExtraArgs) > 0 {
			entry.ExtraArgs = existing.ExtraArgs
		}
		if existing.Info != nil {
			entry.Info = existing.Info
		}
	}

	if identityFile != "" {
		hasI := false
		for i, arg := range entry.ExtraArgs {
			if arg == "-i" && i+1 < len(entry.ExtraArgs) {
				hasI = true
				break
			}
		}
		if !hasI {
			entry.ExtraArgs = append(entry.ExtraArgs, "-i", identityFile)
		}
	}

	cfg.Entries[name] = entry
	globalPool.Close(name)

	if err := saveConfig(cfgPath, cfg); err != nil {
		return mcp.NewToolResultText(fmt.Sprintf("❌ Failed to save configuration: %v", err)), nil
	}

	// Sync aliases for instant shell availability
	_ = SyncAliasesFile(cfg, "", "function", detectShell())

	return mcp.NewToolResultText(fmt.Sprintf("✅ Server '%s' successfully saved to `%s`.", name, cfgPath)), nil
}

func handleRemoveServer(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := argString(request, "name", "")
	if name == "" {
		return mcp.NewToolResultError("Parameter 'name' is required"), nil
	}

	cfgPath := resolveConfigPath()
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return mcp.NewToolResultText(fmt.Sprintf("❌ Failed to load configuration: %v", err)), nil
	}

	if _, exists := cfg.Entries[name]; !exists {
		return mcp.NewToolResultText(fmt.Sprintf("⚠️ Server '%s' does not exist in configuration.", name)), nil
	}

	delete(cfg.Entries, name)
	globalPool.Close(name)

	if err := saveConfig(cfgPath, cfg); err != nil {
		return mcp.NewToolResultText(fmt.Sprintf("❌ Failed to update configuration: %v", err)), nil
	}

	_ = SyncAliasesFile(cfg, "", "function", detectShell())

	return mcp.NewToolResultText(fmt.Sprintf("✅ Server '%s' removed from configuration.", name)), nil
}

func formatServerInfo(name string, entry Entry, keysDir string) string {
	user := entry.User
	if user == "" {
		user = "root"
	}
	host := entry.Host
	if host == "" {
		host = "unknown"
	}
	port := entry.Port
	if port <= 0 {
		port = 22
	}

	authDesc := ""
	if entry.Password != "" {
		authDesc = "Password"
	} else {
		keyPath := ""
		if entry.Identity != "" {
			keyPath = entry.Identity
		} else {
			for i := 0; i < len(entry.ExtraArgs); i++ {
				if (entry.ExtraArgs[i] == "-i" || entry.ExtraArgs[i] == "--identity-file") && i+1 < len(entry.ExtraArgs) {
					keyPath = entry.ExtraArgs[i+1]
					break
				}
			}
		}
		if keyPath != "" {
			resolved := resolveKeyPath(keyPath, keysDir)
			exists := false
			if _, err := os.Stat(resolved); err == nil {
				exists = true
			}
			status := "missing"
			if exists {
				status = "found"
			}
			authDesc = fmt.Sprintf("Key `%s` (%s)", filepath.Base(keyPath), status)
		} else {
			authDesc = "Auto-detect key / SSH agent"
		}
	}

	desc := entry.GetDescription()
	if desc == "" {
		desc = "*(No description provided)*"
	}

	var services []string
	whatToCheck := ""
	var logPaths []string
	notes := ""
	updatedAt := ""

	if entry.Info != nil {
		services = entry.Info.Services
		whatToCheck = strings.TrimSpace(entry.Info.WhatToCheck)
		logPaths = entry.Info.LogPaths
		notes = strings.TrimSpace(entry.Info.Notes)
		updatedAt = strings.TrimSpace(entry.Info.UpdatedAt)
	}

	if whatToCheck == "" {
		whatToCheck = "*(No checklist provided)*"
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("### 🖥️ Server Profile: `%s`", name))
	lines = append(lines, fmt.Sprintf("- **Connection:** `%s@%s:%d`", user, host, port))
	lines = append(lines, fmt.Sprintf("- **Authentication:** %s", authDesc))

	if len(entry.ExtraArgs) > 0 {
		lines = append(lines, fmt.Sprintf("- **Extra Args:** `%s`", strings.Join(entry.ExtraArgs, " ")))
	}
	if updatedAt != "" {
		lines = append(lines, fmt.Sprintf("- **Last Updated:** `%s`", updatedAt))
	}

	lines = append(lines, "")
	lines = append(lines, "#### 📖 Description")
	lines = append(lines, desc)
	lines = append(lines, "")
	lines = append(lines, "#### ⚙️ Key Services")

	if len(services) > 0 {
		for _, svc := range services {
			lines = append(lines, fmt.Sprintf("- %s", svc))
		}
	} else {
		lines = append(lines, "*(No services documented)*")
	}

	lines = append(lines, "")
	lines = append(lines, "#### 🔍 What to Check / Health Checks")
	lines = append(lines, whatToCheck)
	lines = append(lines, "")
	lines = append(lines, "#### 📋 Key Logs & Paths")

	if len(logPaths) > 0 {
		for _, lp := range logPaths {
			if strings.HasPrefix(lp, "`") || strings.HasPrefix(lp, "-") {
				lines = append(lines, lp)
			} else {
				lines = append(lines, fmt.Sprintf("- `%s`", lp))
			}
		}
	} else {
		lines = append(lines, "*(No log paths documented)*")
	}

	if notes != "" {
		lines = append(lines, "")
		lines = append(lines, "#### 💡 Notes & Gotchas")
		lines = append(lines, notes)
	}

	return strings.Join(lines, "\n")
}

func handleGetServerInfo(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	server := argString(request, "server", "all")
	cfg, err := loadConfig(resolveConfigPath())
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to load config: %v", err)), nil
	}
	if len(cfg.Entries) == 0 {
		return mcp.NewToolResultText("No SSH servers currently configured in settings."), nil
	}

	target := strings.TrimSpace(server)
	if target == "" || strings.ToLower(target) == "all" {
		var lines []string
		lines = append(lines, fmt.Sprintf("### Configured Servers Overview (%d total)", len(cfg.Entries)))
		lines = append(lines, "")
		lines = append(lines, "| Server | Host | Key Services | Description |")
		lines = append(lines, "| :--- | :--- | :--- | :--- |")

		names := make([]string, 0, len(cfg.Entries))
		for name := range cfg.Entries {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			entry := cfg.Entries[name]
			host := entry.Host
			if host == "" {
				host = "unknown"
			}
			desc := entry.GetDescription()
			if desc == "" {
				desc = "-"
			}
			desc = strings.ReplaceAll(desc, "\n", " ")
			if len(desc) > 60 {
				desc = desc[:57] + "..."
			}

			svcStr := "-"
			if entry.Info != nil && len(entry.Info.Services) > 0 {
				var svcNames []string
				for _, s := range entry.Info.Services {
					parts := strings.Split(s, " - ")
					svcNames = append(svcNames, strings.TrimSpace(parts[0]))
				}
				if len(svcNames) <= 3 {
					svcStr = strings.Join(svcNames, ", ")
				} else {
					svcStr = fmt.Sprintf("%s (+%d more)", strings.Join(svcNames[:3], ", "), len(svcNames)-3)
				}
			}

			lines = append(lines, fmt.Sprintf("| **%s** | `%s` | %s | %s |", name, host, svcStr, desc))
		}

		lines = append(lines, "\n*Tip: Call `get_server_info(server='<name>')` to view full troubleshooting instructions, key logs, and service breakdown for a specific server.*")
		return mcp.NewToolResultText(strings.Join(lines, "\n")), nil
	}

	entry, exists := cfg.Entries[target]
	if !exists {
		var available []string
		for name := range cfg.Entries {
			available = append(available, name)
		}
		sort.Strings(available)
		return mcp.NewToolResultText(fmt.Sprintf("❌ Server '%s' not found in configuration. Available servers: %s", target, strings.Join(available, ", "))), nil
	}

	return mcp.NewToolResultText(formatServerInfo(target, entry, cfg.KeysDir)), nil
}

func handleUpdateServerInfo(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	server := argString(request, "server", "")
	if server == "" {
		return mcp.NewToolResultError("Parameter 'server' is required"), nil
	}

	cfgPath := resolveConfigPath()
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to load config: %v", err)), nil
	}

	entry, exists := cfg.Entries[server]
	if !exists {
		var available []string
		for name := range cfg.Entries {
			available = append(available, name)
		}
		sort.Strings(available)
		return mcp.NewToolResultText(fmt.Sprintf("❌ Server '%s' not found in configuration. Available servers: %s", server, strings.Join(available, ", "))), nil
	}

	if entry.Info == nil {
		entry.Info = &ServerInfo{}
	}

	args := request.GetArguments()
	var updates []string

	if v, ok := args["description"]; ok && v != nil {
		entry.Info.Description = strings.TrimSpace(fmt.Sprintf("%v", v))
		updates = append(updates, "description")
	}

	if v, ok := args["services"]; ok && v != nil {
		entry.Info.Services = argStringSlice(request, "services")
		updates = append(updates, fmt.Sprintf("services (%d items)", len(entry.Info.Services)))
	}

	if v, ok := args["what_to_check"]; ok && v != nil {
		entry.Info.WhatToCheck = strings.TrimSpace(fmt.Sprintf("%v", v))
		updates = append(updates, "what_to_check")
	}

	if v, ok := args["log_paths"]; ok && v != nil {
		entry.Info.LogPaths = argStringSlice(request, "log_paths")
		updates = append(updates, fmt.Sprintf("log_paths (%d items)", len(entry.Info.LogPaths)))
	}

	if v, ok := args["notes"]; ok && v != nil {
		entry.Info.Notes = strings.TrimSpace(fmt.Sprintf("%v", v))
		updates = append(updates, "notes")
	}

	if len(updates) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("⚠️ No fields provided to update for server '%s'.", server)), nil
	}

	entry.Info.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")
	cfg.Entries[server] = entry

	if err := saveConfig(cfgPath, cfg); err != nil {
		return mcp.NewToolResultText(fmt.Sprintf("❌ Failed to save configuration: %v", err)), nil
	}

	formatted := formatServerInfo(server, entry, cfg.KeysDir)
	return mcp.NewToolResultText(fmt.Sprintf("✅ Successfully updated info for `%s` (%s):\n\n%s", server, strings.Join(updates, ", "), formatted)), nil
}
