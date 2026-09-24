package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/cobra"
)

var builtinCommands = map[string]bool{
	"add":               true,
	"remove":            true,
	"rm":                true,
	"list":              true,
	"ls":                true,
	"show":              true,
	"get":               true,
	"cmd":               true,
	"command":           true,
	"run":               true,
	"connect":           true,
	"exec":              true,
	"export":            true,
	"env":               true,
	"init":              true,
	"sync":              true,
	"import-ssh-config": true,
	"set-keys-dir":      true,
	"show-config":       true,
	"mcp":               true,
	"serve":             true,
	"mcp-server":        true,
	"test":              true,
	"info":              true,
	"help":              true,
	"completion":        true,
}

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	cfg, _ := loadConfig(resolveConfigPath())

	root := &cobra.Command{
		Use:   "kgssh [command|alias] [args...]",
		Short: "SSH alias manager and MCP server — configure, generate, run, and automate SSH connections",
		Long: `kgssh is an SSH alias manager and Model Context Protocol (MCP) server.
It organizes named SSH targets, exports them as native shell functions/aliases
for zsh and bash, syncs them to your environment, provides a direct runner,
and serves high-performance remote execution and SFTP tools over MCP.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			aliasName := args[0]
			entry, ok := cfg.Entries[aliasName]
			if !ok {
				return fmt.Errorf("unknown command or alias: %q\nRun 'kgssh list' to see configured aliases", aliasName)
			}
			return RunSSH(entry, cfg.KeysDir, args[1:])
		},
	}

	root.AddCommand(newListCommand())
	root.AddCommand(newShowCommand())
	root.AddCommand(newCommandCommand())
	root.AddCommand(newAddCommand())
	root.AddCommand(newRemoveCommand())
	root.AddCommand(newRunCommand())
	root.AddCommand(newConnectCommand())
	root.AddCommand(newExportCommand())
	root.AddCommand(newInitCommand())
	root.AddCommand(newSyncCommand())
	root.AddCommand(newImportSSHConfigCommand())
	root.AddCommand(newSetKeysDirCommand())
	root.AddCommand(newShowConfigCommand())
	root.AddCommand(newMCPCommand())
	root.AddCommand(newTestCommand())
	root.AddCommand(newInfoCommand())

	// Dynamically register aliases as runnable subcommands for shell completion & direct execution
	for name, entry := range cfg.Entries {
		if builtinCommands[name] {
			continue
		}
		targetName := name
		targetEntry := entry
		aliasCmd := &cobra.Command{
			Use:                targetName + " [flags/remote-args...]",
			Short:              fmt.Sprintf("Connect to %s (%s)", targetName, targetEntry.Host),
			DisableFlagParsing: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				return RunSSH(targetEntry, cfg.KeysDir, args)
			},
		}
		root.AddCommand(aliasCmd)
	}

	return root
}

func newListCommand() *cobra.Command {
	var raw, showCmds, asJSON bool

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List configured SSH aliases",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(resolveConfigPath())
			if err != nil {
				if os.IsNotExist(err) || len(cfg.Entries) == 0 {
					fmt.Println("No configured SSH aliases. Use 'kgssh add <name> <user@host>' to add one.")
					return nil
				}
				return err
			}

			if len(cfg.Entries) == 0 {
				fmt.Println("No configured SSH aliases. Use 'kgssh add <name> <user@host>' to add one.")
				return nil
			}

			names := make([]string, 0, len(cfg.Entries))
			for name := range cfg.Entries {
				names = append(names, name)
			}
			sort.Strings(names)

			if asJSON {
				data, err := json.MarshalIndent(cfg.Entries, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
				return nil
			}

			if raw {
				for _, name := range names {
					fmt.Println(name)
				}
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			if showCmds {
				fmt.Fprintln(w, "ALIAS\tCOMMAND")
				fmt.Fprintln(w, "-----\t-------")
				for _, name := range names {
					entry := cfg.Entries[name]
					cmdStr := BuildSSHCommandString(name, entry, cfg.KeysDir)
					fmt.Fprintf(w, "%s\t%s\n", name, cmdStr)
				}
			} else {
				fmt.Fprintln(w, "ALIAS\tTARGET\tIDENTITY\tDESCRIPTION")
				fmt.Fprintln(w, "-----\t------\t--------\t-----------")
				for _, name := range names {
					entry := cfg.Entries[name]
					target := entry.Host
					if entry.User != "" {
						target = entry.User + "@" + entry.Host
					}
					if entry.Port > 0 && entry.Port != 22 {
						target = fmt.Sprintf("%s:%d", target, entry.Port)
					}

					id := entry.ResolvedIdentity(cfg.KeysDir)
					if id != "" {
						id = DisplayPath(id)
					} else {
						id = "-"
					}

					desc := entry.Description
					if desc == "" {
						desc = "-"
					}

					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", name, target, id, desc)
				}
			}
			return w.Flush()
		},
	}

	cmd.Flags().BoolVarP(&raw, "raw", "q", false, "Output only alias names")
	cmd.Flags().BoolVarP(&showCmds, "commands", "c", false, "Display full SSH command for each alias")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output aliases as JSON")
	return cmd
}

func newShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "show <alias>",
		Aliases: []string{"get"},
		Short:   "Show details for an SSH alias",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(resolveConfigPath())
			if err != nil {
				return err
			}

			entry, ok := cfg.Entries[args[0]]
			if !ok {
				return fmt.Errorf("unknown SSH alias: %q", args[0])
			}

			target := entry.Host
			if entry.User != "" {
				target = entry.User + "@" + entry.Host
			}
			if entry.Port > 0 {
				target = fmt.Sprintf("%s:%d", target, entry.Port)
			}

			fmt.Printf("Alias:       %s\n", args[0])
			fmt.Printf("Target:      %s\n", target)
			fmt.Printf("Host:        %s\n", entry.Host)
			if entry.User != "" {
				fmt.Printf("User:        %s\n", entry.User)
			}
			if entry.Port > 0 {
				fmt.Printf("Port:        %d\n", entry.Port)
			}
			if id := entry.ResolvedIdentity(cfg.KeysDir); id != "" {
				fmt.Printf("Identity:    %s\n", DisplayPath(id))
			}
			if entry.ProxyJump != "" {
				fmt.Printf("ProxyJump:   %s\n", entry.ProxyJump)
			}
			if entry.Description != "" {
				fmt.Printf("Description: %s\n", entry.Description)
			}
			if len(entry.ExtraArgs) > 0 {
				fmt.Printf("Extra Args:  %s\n", strings.Join(entry.ExtraArgs, " "))
			}
			fmt.Printf("SSH Command: %s\n", BuildSSHCommandString(args[0], entry, cfg.KeysDir))

			return nil
		},
	}
}

func newCommandCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "cmd <alias>",
		Aliases: []string{"command"},
		Short:   "Print the raw SSH command for an alias",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(resolveConfigPath())
			if err != nil {
				return err
			}
			entry, ok := cfg.Entries[args[0]]
			if !ok {
				return fmt.Errorf("unknown SSH alias: %q", args[0])
			}
			fmt.Println(BuildSSHCommandString(args[0], entry, cfg.KeysDir))
			return nil
		},
	}
}

func newAddCommand() *cobra.Command {
	var user, host, password, identity, proxyJump, description string
	var port int
	var extraArgs []string
	var noSync bool

	cmd := &cobra.Command{
		Use:   "add <alias> [user@host[:port]]",
		Short: "Add or update an SSH alias",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			alias := args[0]
			if builtinCommands[alias] {
				return fmt.Errorf("cannot use reserved command name %q as an alias", alias)
			}

			if len(args) == 2 {
				raw := args[1]
				// Parse user@host:port or host:port or user@host
				if atIdx := strings.Index(raw, "@"); atIdx != -1 {
					if user == "" {
						user = raw[:atIdx]
					}
					raw = raw[atIdx+1:]
				}
				if colonIdx := strings.LastIndex(raw, ":"); colonIdx != -1 {
					if p, err := strconv.Atoi(raw[colonIdx+1:]); err == nil && port == 0 {
						port = p
					}
					raw = raw[:colonIdx]
				}
				if host == "" {
					host = raw
				}
			}

			if host == "" {
				return fmt.Errorf("host is required (pass user@host or use --host)")
			}
			if port == 0 {
				port = 22
			}

			configPath := resolveConfigPath()
			cfg, err := loadConfig(configPath)
			if err != nil {
				if !os.IsNotExist(err) {
					return fmt.Errorf("load config: %w", err)
				}
				cfg = Config{Entries: map[string]Entry{}}
			}
			if cfg.Entries == nil {
				cfg.Entries = map[string]Entry{}
			}

			// Preserve existing properties if updating
			existing, exists := cfg.Entries[alias]
			if exists {
				if user == "" {
					user = existing.User
				}
				if identity == "" && existing.Identity != "" {
					identity = existing.Identity
				}
				if proxyJump == "" && existing.ProxyJump != "" {
					proxyJump = existing.ProxyJump
				}
				if description == "" && existing.Description != "" {
					description = existing.Description
				}
				if password == "" && existing.Password != "" {
					password = existing.Password
				}
			}

			cfg.Entries[alias] = Entry{
				User:        user,
				Host:        host,
				Port:        port,
				Password:    password,
				Identity:    identity,
				ProxyJump:   proxyJump,
				Description: description,
				ExtraArgs:   extraArgs,
			}

			if err := saveConfig(configPath, cfg); err != nil {
				return err
			}
			fmt.Printf("✓ Added SSH alias %q -> %s\n", alias, BuildSSHCommandString(alias, cfg.Entries[alias], cfg.KeysDir))

			if !noSync {
				if err := SyncAliasesFile(cfg, "", "function", detectShell()); err == nil {
					fmt.Printf("✓ Synced aliases to %s\n", DisplayPath(ResolveAliasesFilePath()))
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&user, "user", "u", "", "SSH username")
	cmd.Flags().StringVarP(&host, "host", "H", "", "SSH host")
	cmd.Flags().IntVarP(&port, "port", "p", 0, "SSH port")
	cmd.Flags().StringVarP(&identity, "identity", "i", "", "SSH identity/private key file")
	cmd.Flags().StringVarP(&proxyJump, "proxy-jump", "J", "", "SSH proxy jump target")
	cmd.Flags().StringVarP(&description, "description", "d", "", "Description for this alias")
	cmd.Flags().StringVarP(&password, "password", "P", "", "SSH password")
	cmd.Flags().StringSliceVar(&extraArgs, "extra-arg", nil, "Additional SSH arguments")
	cmd.Flags().BoolVar(&noSync, "no-sync", false, "Skip auto-syncing ~/.kgssh/aliases.sh")
	return cmd
}

func newRemoveCommand() *cobra.Command {
	var noSync bool

	cmd := &cobra.Command{
		Use:     "remove <alias>",
		Aliases: []string{"rm"},
		Short:   "Remove a configured SSH alias",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath := resolveConfigPath()
			cfg, err := loadConfig(configPath)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("no configured aliases")
				}
				return fmt.Errorf("load config: %w", err)
			}

			alias := args[0]
			if _, ok := cfg.Entries[alias]; !ok {
				return fmt.Errorf("unknown SSH alias: %q", alias)
			}

			delete(cfg.Entries, alias)
			if err := saveConfig(configPath, cfg); err != nil {
				return err
			}

			fmt.Printf("✓ Removed SSH alias %q\n", alias)
			if !noSync {
				if err := SyncAliasesFile(cfg, "", "function", detectShell()); err == nil {
					fmt.Printf("✓ Synced aliases to %s\n", DisplayPath(ResolveAliasesFilePath()))
				}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&noSync, "no-sync", false, "Skip auto-syncing ~/.kgssh/aliases.sh")
	return cmd
}

func newRunCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "run <alias> [args...]",
		Aliases:            []string{"exec"},
		Short:              "Connect to an SSH alias or execute a remote command",
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(resolveConfigPath())
			if err != nil {
				return fmt.Errorf("config error: %w", err)
			}

			aliasName := args[0]
			entry, ok := cfg.Entries[aliasName]
			if !ok {
				return fmt.Errorf("unknown SSH alias: %q\nRun 'kgssh list' to view aliases", aliasName)
			}

			return RunSSH(entry, cfg.KeysDir, args[1:])
		},
	}
}

func newConnectCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "connect <alias> [args...]",
		Short:              "Connect to an SSH alias (synonym for 'run')",
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(resolveConfigPath())
			if err != nil {
				return fmt.Errorf("config error: %w", err)
			}

			aliasName := args[0]
			entry, ok := cfg.Entries[aliasName]
			if !ok {
				return fmt.Errorf("unknown SSH alias: %q", aliasName)
			}

			return RunSSH(entry, cfg.KeysDir, args[1:])
		},
	}
}

func newExportCommand() *cobra.Command {
	var format, shellType string

	cmd := &cobra.Command{
		Use:     "export",
		Aliases: []string{"env"},
		Short:   "Export SSH aliases as shell functions or aliases",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(resolveConfigPath())
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if shellType == "" {
				shellType = detectShell()
			}
			fmt.Print(GenerateAll(cfg, format, shellType))
			return nil
		},
	}

	cmd.Flags().StringVar(&format, "format", "function", "Format to export: 'function' (supports extra args) or 'alias'")
	cmd.Flags().StringVar(&shellType, "shell", "", "Shell type: 'zsh', 'bash', or 'fish' (auto-detected by default)")
	return cmd
}

func newInitCommand() *cobra.Command {
	var format, shellType string

	cmd := &cobra.Command{
		Use:   "init [shell]",
		Short: "Output shell initialization hook for eval \"$(kgssh init)\"",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(resolveConfigPath())
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if len(args) > 0 {
				shellType = args[0]
			}
			if shellType == "" {
				shellType = detectShell()
			}
			fmt.Print(GenerateAll(cfg, format, shellType))
			return nil
		},
	}

	cmd.Flags().StringVar(&format, "format", "function", "Format: 'function' (recommended) or 'alias'")
	cmd.Flags().StringVar(&shellType, "shell", "", "Shell: 'zsh', 'bash', or 'fish'")
	return cmd
}

func newSyncCommand() *cobra.Command {
	var install bool
	var outFile, format, shellType string

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync SSH aliases to ~/.kgssh/aliases.sh",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(resolveConfigPath())
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("no config found at %s", resolveConfigPath())
				}
				return err
			}

			if shellType == "" {
				shellType = detectShell()
			}

			targetFile := outFile
			if targetFile == "" {
				targetFile = ResolveAliasesFilePath()
			}

			if err := SyncAliasesFile(cfg, targetFile, format, shellType); err != nil {
				return fmt.Errorf("sync aliases: %w", err)
			}
			fmt.Printf("✓ Successfully synced %d SSH aliases to %s\n", len(cfg.Entries), DisplayPath(targetFile))

			if install {
				installed, err := InstallShellHook("", targetFile)
				if err != nil {
					return fmt.Errorf("install shell hook: %w", err)
				}
				if installed {
					fmt.Println("✓ Added source hook to your shell RC file! Run 'source ~/.zshrc' (or open a new terminal) to use aliases.")
				} else {
					fmt.Println("✓ Shell RC file is already configured to source aliases.")
				}
			} else {
				fmt.Printf("Tip: Run 'kgssh sync --install' to auto-add source hook to your shell RC file,\n     or add: [ -f %s ] && source %s\n", DisplayPath(targetFile), DisplayPath(targetFile))
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&install, "install", false, "Automatically configure ~/.zshrc or ~/.bashrc to source aliases")
	cmd.Flags().StringVar(&outFile, "file", "", "Target file to write aliases to (default ~/.kgssh/aliases.sh)")
	cmd.Flags().StringVar(&format, "format", "function", "Format: 'function' or 'alias'")
	cmd.Flags().StringVar(&shellType, "shell", "", "Shell: 'zsh', 'bash', or 'fish'")
	return cmd
}

func newImportSSHConfigCommand() *cobra.Command {
	var overwrite, dryRun, noSync bool

	cmd := &cobra.Command{
		Use:   "import-ssh-config [path]",
		Short: "Import SSH aliases from OpenSSH ~/.ssh/config",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath := ""
			if len(args) > 0 {
				configPath = args[0]
			}

			imported, err := ImportSSHConfigFile(configPath)
			if err != nil {
				return fmt.Errorf("import ssh config: %w", err)
			}

			if len(imported) == 0 {
				fmt.Println("No valid Host entries found in SSH config.")
				return nil
			}

			cfgPath := resolveConfigPath()
			cfg, _ := loadConfig(cfgPath)
			if cfg.Entries == nil {
				cfg.Entries = map[string]Entry{}
			}

			count := 0
			for name, entry := range imported {
				if builtinCommands[name] {
					continue
				}
				if _, exists := cfg.Entries[name]; exists && !overwrite {
					fmt.Printf("Skipping existing alias %q (use --overwrite to replace)\n", name)
					continue
				}
				if dryRun {
					fmt.Printf("[dry-run] Would import %s -> %s\n", name, BuildSSHCommandString(name, entry, cfg.KeysDir))
				} else {
					cfg.Entries[name] = entry
					fmt.Printf("✓ Imported alias %q -> %s\n", name, BuildSSHCommandString(name, entry, cfg.KeysDir))
				}
				count++
			}

			if dryRun {
				fmt.Printf("\n[dry-run] Total %d aliases would be imported.\n", count)
				return nil
			}

			if count > 0 {
				if err := saveConfig(cfgPath, cfg); err != nil {
					return err
				}
				fmt.Printf("✓ Saved %d imported aliases to %s\n", count, DisplayPath(cfgPath))

				if !noSync {
					_ = SyncAliasesFile(cfg, "", "function", detectShell())
				}
			} else {
				fmt.Println("No new aliases imported.")
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "Overwrite existing aliases with same name")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview imports without modifying configuration")
	cmd.Flags().BoolVar(&noSync, "no-sync", false, "Skip auto-syncing ~/.kgssh/aliases.sh")
	return cmd
}

func newSetKeysDirCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "set-keys-dir <dir>",
		Short: "Set the SSH keys directory for identity fallback resolution",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath := resolveConfigPath()
			cfg, err := loadConfig(configPath)
			if err != nil {
				if !os.IsNotExist(err) {
					return fmt.Errorf("load config: %w", err)
				}
				cfg = Config{Entries: map[string]Entry{}}
			}
			cfg.KeysDir = args[0]
			if err := saveConfig(configPath, cfg); err != nil {
				return err
			}
			fmt.Printf("✓ SSH keys directory set to %q\n", args[0])
			_ = SyncAliasesFile(cfg, "", "function", detectShell())
			return nil
		},
	}
}

func newShowConfigCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show-config",
		Short: "Show raw kgssh JSON configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(resolveConfigPath())
			if err != nil {
				return err
			}
			data, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				return fmt.Errorf("encode config: %w", err)
			}
			fmt.Println(string(data))
			return nil
		},
	}
}

func detectShell() string {
	shell := os.Getenv("SHELL")
	if strings.Contains(shell, "zsh") {
		return "zsh"
	}
	if strings.Contains(shell, "bash") {
		return "bash"
	}
	if strings.Contains(shell, "fish") {
		return "fish"
	}
	return "zsh"
}

func newMCPCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "mcp",
		Aliases: []string{"serve", "mcp-server"},
		Short:   "Start Model Context Protocol (MCP) server over stdio",
		RunE: func(cmd *cobra.Command, args []string) error {
			return RunMCPServer()
		},
	}
}

func newTestCommand() *cobra.Command {
	var timeoutSec int

	cmd := &cobra.Command{
		Use:   "test [alias]",
		Short: "Test SSH connectivity and measure latency to one or all aliases",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := "all"
			if len(args) > 0 {
				target = args[0]
			}
			res, err := handleTestConnection(context.Background(), mcp.CallToolRequest{
				Params: mcp.CallToolParams{
					Name: "test_connection",
					Arguments: map[string]any{
						"server":  target,
						"timeout": timeoutSec,
					},
				},
			})
			if err != nil {
				return err
			}
			for _, content := range res.Content {
				if tc, ok := mcp.AsTextContent(content); ok {
					fmt.Println(tc.Text)
				}
			}
			return nil
		},
	}

	cmd.Flags().IntVarP(&timeoutSec, "timeout", "t", 5, "Connection timeout in seconds")
	return cmd
}

func newInfoCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "info [alias]",
		Short: "Display operational context, architecture info, and checklists for aliases",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := "all"
			if len(args) > 0 {
				target = args[0]
			}
			res, err := handleGetServerInfo(context.Background(), mcp.CallToolRequest{
				Params: mcp.CallToolParams{
					Name: "get_server_info",
					Arguments: map[string]any{
						"server": target,
					},
				},
			})
			if err != nil {
				return err
			}
			for _, content := range res.Content {
				if tc, ok := mcp.AsTextContent(content); ok {
					fmt.Println(tc.Text)
				}
			}
			return nil
		},
	}
}

