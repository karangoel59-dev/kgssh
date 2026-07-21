package main

import (
    "encoding/json"
    "fmt"
    "net"
    "os"
    "path/filepath"
    "strconv"
    "strings"

    "github.com/spf13/cobra"
    "golang.org/x/crypto/ssh"
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
    root.AddCommand(newRemoveCommand())
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

            clientConfig, err := buildSSHClientConfig(entry)
            if err != nil {
                return fmt.Errorf("ssh config error: %w", err)
            }

            return connectWithNativeSSH(entry, clientConfig)
        },
    }
}

func newAddCommand() *cobra.Command {
    var user, host, alias, password string
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

            cfg.Entries[alias] = entry{
                User:      user,
                Host:      host,
                Port:      port,
                Password:  password,
                ExtraArgs: extraArgs,
            }

            if err := saveConfig(configPath, cfg); err != nil {
                return err
            }
            fmt.Printf("Added connection %q\n", alias)
            return nil
        },
    }

    cmd.Flags().StringVarP(&user, "user", "u", "", "SSH username")
    cmd.Flags().StringVarP(&host, "host", "H", "", "SSH host")
    cmd.Flags().IntVarP(&port, "port", "p", 0, "SSH port")
    cmd.Flags().StringVarP(&password, "password", "P", "", "SSH password")
    cmd.Flags().StringSliceVar(&extraArgs, "extra-arg", nil, "Additional SSH arguments")
    return cmd
}

func newRemoveCommand() *cobra.Command {
    return &cobra.Command{
        Use:   "remove <name>",
        Short: "Remove a configured SSH server",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            configPath := resolveConfigPath()
            cfg, err := loadConfig(configPath)
            if err != nil {
                if os.IsNotExist(err) {
                    return fmt.Errorf("no configured servers")
                }
                return fmt.Errorf("load config: %w", err)
            }

            if _, ok := cfg.Entries[args[0]]; !ok {
                return fmt.Errorf("unknown server: %s", args[0])
            }

            delete(cfg.Entries, args[0])
            if err := saveConfig(configPath, cfg); err != nil {
                return err
            }

            fmt.Printf("Removed connection %q\n", args[0])
            return nil
        },
    }
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
    Password  string   `json:"password,omitempty"`
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

func saveConfig(path string, cfg config) error {
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

func connectWithNativeSSH(entry entry, clientConfig *ssh.ClientConfig) error {
    address := entry.Host
    if entry.Port > 0 {
        address = net.JoinHostPort(entry.Host, strconv.Itoa(entry.Port))
    }

    fmt.Fprintf(os.Stderr, "Connecting to %s...\n", address)

    conn, err := ssh.Dial("tcp", address, clientConfig)
    if err != nil {
        return fmt.Errorf("dial SSH: %w", err)
    }
    defer conn.Close()

    session, err := conn.NewSession()
    if err != nil {
        return fmt.Errorf("create SSH session: %w", err)
    }
    defer session.Close()

    session.Stdin = os.Stdin
    session.Stdout = os.Stdout
    session.Stderr = os.Stderr

    if err := session.RequestPty("xterm", 40, 80, ssh.TerminalModes{ssh.ECHO: 1}); err != nil {
        return fmt.Errorf("request PTY: %w", err)
    }

    if err := session.Shell(); err != nil {
        return fmt.Errorf("start shell: %w", err)
    }

    if err := session.Wait(); err != nil {
        if exitErr, ok := err.(*ssh.ExitError); ok {
            os.Exit(exitErr.ExitStatus())
        }
        return fmt.Errorf("SSH session failed: %w", err)
    }
    return nil
}

func buildSSHClientConfig(entry entry) (*ssh.ClientConfig, error) {
    clientConfig := &ssh.ClientConfig{
        User:            entry.User,
        HostKeyCallback: ssh.InsecureIgnoreHostKey(),
    }

    var authMethods []ssh.AuthMethod
    if entry.Password != "" {
        authMethods = append(authMethods, ssh.Password(entry.Password))
    }

    for i := 0; i < len(entry.ExtraArgs); i++ {
        if entry.ExtraArgs[i] != "-i" && entry.ExtraArgs[i] != "--identity-file" {
            continue
        }
        if i+1 >= len(entry.ExtraArgs) {
            return nil, fmt.Errorf("missing path for identity file")
        }
        signer, err := loadSSHPrivateKey(entry.ExtraArgs[i+1])
        if err != nil {
            return nil, fmt.Errorf("load identity %q: %w", entry.ExtraArgs[i+1], err)
        }
        authMethods = append(authMethods, ssh.PublicKeys(signer))
        i++
    }

    clientConfig.Auth = authMethods
    return clientConfig, nil
}

func loadSSHPrivateKey(path string) (ssh.Signer, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }
    return ssh.ParsePrivateKey(data)
}