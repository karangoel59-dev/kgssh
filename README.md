# kgssh

**kgssh** is a unified SSH Alias Manager & Model Context Protocol (MCP) Server for macOS and Linux. It lets you define named SSH connections, exports them as native shell functions/aliases (with argument and command forwarding), syncs them to your active shell (`zsh`, `bash`, `fish`), provides a direct runner CLI, and serves high-performance remote execution, SFTP, and server management tools to AI assistants via MCP.

---

## Features

- **Model Context Protocol (MCP) Server**: Run `kgssh mcp` to serve native MCP tools over stdio for AI coding assistants (Antigravity, Cursor, Claude Desktop, etc.).
  - `list_servers`: List configured servers and auth methods.
  - `ssh_exec`: Execute shell commands on remote servers with timeout, PTY, and connection pooling.
  - `test_connection`: Test connectivity and latency to servers.
  - `read_file` / `write_file` / `list_directory`: Secure SFTP file reading, writing, and directory listing.
  - `add_server` / `remove_server`: Add/remove servers with instant auto-sync to shell aliases.
  - `get_server_info` / `update_server_info`: Store and inspect operational playbooks, key services, health checks, and log paths.
- **Native Shell Functions & Aliases**: Generates functions like `prod1() { ssh ... "$@"; }` allowing direct terminal usage (`prod1`, `prod1 "ls -la"`, `prod1 -L 8080:localhost:8080`).
- **Hybrid CLI & Shell**:
  - Run directly via shell: `prod1`
  - Run via CLI: `kgssh prod1` or `kgssh run prod1`
  - Test latency via CLI: `kgssh test prod1` or `kgssh test`
  - View operational docs via CLI: `kgssh info prod1` or `kgssh info`
  - Inspect resolved command: `kgssh cmd prod1`
- **Automatic Syncing**: Any `kgssh add`, `kgssh remove`, or MCP mutation automatically refreshes `~/.kgssh/aliases.sh`.
- **Cross-Platform Identity Resolution**: Automatically detects and heals key paths across environments (e.g. Linux `/home/...` to macOS `~/.ssh/...`).
- **OpenSSH Import**: Easily import existing `Host` blocks from `~/.ssh/config` using `kgssh import-ssh-config`.
- **Full OpenSSH Compatibility**: Leverages your system `ssh` binary directly, ensuring full support for agent forwarding, tmux, vim, escape sequences, and native PTY.

---

## Quick Start & Shell Setup

To install the aliases into your current shell (`~/.zshrc` or `~/.bashrc`), simply run:

```bash
kgssh sync --install
```

This generates `~/.kgssh/aliases.sh` and adds the source line to your shell configuration:
```bash
[ -f ~/.kgssh/aliases.sh ] && source ~/.kgssh/aliases.sh
```

Alternatively, add dynamic evaluation to `~/.zshrc`:
```bash
eval "$(kgssh init)"
```

---

## Commands

### 1. List Aliases
```bash
# Formatted table view
kgssh list

# Show raw SSH command lines
kgssh list -c

# Output alias names only (for scripts / completions)
kgssh list -q

# Output JSON
kgssh list --json
```

### 2. Add / Update an Alias
```bash
# Syntax: kgssh add <alias> [user@host[:port]] [flags]
kgssh add web ubuntu@192.168.1.100 -p 2222 -i ~/.ssh/id_rsa -d "Primary web node"
kgssh add jump admin@jump.example.com -J bastion
kgssh add staging root@staging.internal
```

Flags:
- `-u, --user`: SSH username
- `-H, --host`: SSH host / IP
- `-p, --port`: SSH port (default 22)
- `-i, --identity`: Private key file path
- `-J, --proxy-jump`: Proxy jump target
- `-d, --description`: Description for the alias
- `-P, --password`: SSH password (for sshpass wrapper if installed)
- `--extra-arg`: Arbitrary extra SSH flags

### 3. Show Details / Raw Command
```bash
# Full details
kgssh show prod1

# Raw runnable command string (great for piping to pbcopy)
kgssh cmd prod1
kgssh cmd prod1 | pbcopy
```

### 4. Run / Connect via CLI
```bash
# Interactive shell
kgssh prod1
# or
kgssh run prod1

# Execute remote command
kgssh prod1 "sudo systemctl restart nginx"
# or
kgssh run prod1 uptime
```

### 5. Remove an Alias
```bash
kgssh remove prod1
# or
kgssh rm prod1
```

### 6. Sync Aliases to Shell File
```bash
# Syncs to ~/.kgssh/aliases.sh
kgssh sync

# Sync and automatically hook into ~/.zshrc or ~/.bashrc
kgssh sync --install
```

### 7. Import from `~/.ssh/config`
```bash
# Preview what would be imported
kgssh import-ssh-config --dry-run

# Import hosts
kgssh import-ssh-config

# Overwrite existing aliases if duplicate names match
kgssh import-ssh-config --overwrite
### 8. Test Connectivity & Latency
```bash
# Test all configured servers
kgssh test

# Test a specific server
kgssh test prod1 -t 10
```

### 9. View Operational Context & Playbooks
```bash
# Overview of all servers
kgssh info

# Detailed architecture, services, logs, and health check instructions for a server
kgssh info agentic-stage
```

### 10. Run as Model Context Protocol (MCP) Server
```bash
# Start MCP server over stdio
kgssh mcp
```

#### MCP Integration Config
Add to your MCP configuration (e.g. `~/.gemini/config/mcp_config.json` or Claude Desktop):

```json
{
  "mcpServers": {
    "ssh": {
      "command": "/usr/local/bin/kgssh",
      "args": ["mcp"],
      "env": {
        "KGSSH_CONFIG": "~/.kgssh/config.json"
      }
    }
  }
}
```

---

## Configuration

Stored in `~/.kgssh/config.json` (or override with `KGSSH_CONFIG` environment variable):

```json
{
  "entries": {
    "prod1": {
      "user": "root",
      "host": "159.89.171.7",
      "port": 22,
      "identity": "~/.ssh/dev_chat360"
    }
  },
  "keysDir": "~/.ssh"
}
```

---

## Building & Testing

```bash
go test -v ./...
go build -o kgssh .
```
