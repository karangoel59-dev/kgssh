package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "kgssh",
		Short: "Launch SSH connections from named config entries",
		Long:  "kgssh connects to SSH targets defined in a JSON config file.",
	}

	root.AddCommand(newListCommand())
	root.AddCommand(newConnectCommand())
	root.AddCommand(newAddCommand())
	return root
}

func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured SSH servers",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(resolveConfigPath())
			if err != nil {
				return err
			}
			if len(cfg.Entries) == 0 {
				fmt.Println("No configured servers")
				return nil
			}
			fmt.Println("Configured servers:")
			for name := range cfg.Entries {
				fmt.Printf("  - %s\n", name)
			}
			return nil
		},
	}
}

func newConnectCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "connect <name>",
		Short: "Connect to a configured SSH server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(resolveConfigPath())
			if err != nil {
				return fmt.Errorf("config error: %w", err)
			}

			entry, ok := cfg.Entries[args[0]]
			if !ok {
				return fmt.Errorf("unknown server: %s", args[0])
			}

			sshArgs := buildSSHArgs(entry)
			fmt.Fprintf(os.Stderr, "Connecting to %s...\n", entry.Host)
			c := exec.Command("ssh", sshArgs...)
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			if err := c.Run(); err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					os.Exit(exitErr.ExitCode())
				}
				return fmt.Errorf("ssh failed: %w", err)
			}
			return nil
		},
	}
}

func newAddCommand() *cobra.Command {
	var user, host, alias string
	var port int
	var extraArgs []string

	cmd := &cobra.Command{
		Use:   "add <name> [user@host]",
		Short: "Add a new SSH connection entry",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			alias = args[0]
			if len(args) == 2 {
				parsedUser, parsedHost, found := strings.Cut(args[1], "@")
				if !found || parsedUser == "" || parsedHost == "" {
					return fmt.Errorf("target must be in the form user@host")
				}
				if user == "" {
					user = parsedUser
				}
				if host == "" {
					host = parsedHost
				}
			}
			if user == "" {
				return fmt.Errorf("user is required")
			}
			if host == "" {
				return fmt.Errorf("host is required")
			}

			configPath := resolveConfigPath()
			cfg, err := loadConfig(configPath)
			if err != nil {
				if !os.IsNotExist(err) {
					return fmt.Errorf("load config: %w", err)
				}
				cfg = config{Entries: map[string]entry{}}
			}
			if cfg.Entries == nil {
				cfg.Entries = map[string]entry{}
			}

			cfg.Entries[alias] = entry{User: user, Host: host, Port: port, ExtraArgs: extraArgs}
			data, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				return fmt.Errorf("encode config: %w", err)
			}
			if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
				return fmt.Errorf("create config directory: %w", err)
			}
			if err := os.WriteFile(configPath, append(data, '\n'), 0o600); err != nil {
				return fmt.Errorf("write config: %w", err)
			}
			fmt.Printf("Added connection %q\n", alias)
			return nil
		},
	}

	cmd.Flags().StringVarP(&user, "user", "u", "", "SSH username")
	cmd.Flags().StringVarP(&host, "host", "H", "", "SSH host")
	cmd.Flags().IntVarP(&port, "port", "p", 0, "SSH port")
	cmd.Flags().StringSliceVar(&extraArgs, "extra-arg", nil, "Additional SSH arguments")
	return cmd
}

func resolveConfigPath() string {
	if path := os.Getenv("KGSSH_CONFIG"); path != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".kgssh", "config.json")
}

type config struct {
	Entries map[string]entry `json:"entries"`
}

type entry struct {
	User      string   `json:"user"`
	Host      string   `json:"host"`
	Port      int      `json:"port,omitempty"`
	ExtraArgs []string `json:"extraArgs,omitempty"`
}

func loadConfig(path string) (config, error) {
	var cfg config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	if cfg.Entries == nil {
		cfg.Entries = map[string]entry{}
	}
	return cfg, nil
}

func buildSSHArgs(entry entry) []string {
	args := []string{"-o", "BatchMode=yes"}
	if entry.Port > 0 {
		args = append(args, "-p", strconv.Itoa(entry.Port))
	}
	args = append(args, entry.ExtraArgs...)
	args = append(args, entry.User+"@"+entry.Host)
	return args
}
