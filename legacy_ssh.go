package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type entry = Entry
type config = Config

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

func buildSSHClientConfig(entry entry, keysDir string) (*ssh.ClientConfig, error) {
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
		signer, err := loadSSHPrivateKey(entry.ExtraArgs[i+1], keysDir)
		if err != nil {
			return nil, fmt.Errorf("load identity %q: %w", entry.ExtraArgs[i+1], err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
		i++
	}

	if agentAuthMethod, err := sshAgentAuthMethod(); err == nil {
		authMethods = append(authMethods, agentAuthMethod)
	}

	authMethods = append(authMethods, loadDefaultSSHAuthMethods(keysDir)...)

	clientConfig.Auth = authMethods
	return clientConfig, nil
}

func loadDefaultSSHAuthMethods(keysDir string) []ssh.AuthMethod {
	var authMethods []ssh.AuthMethod
	seen := map[string]struct{}{}

	for _, dir := range defaultSSHKeyDirs(keysDir) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if !isLikelySSHPrivateKeyFile(entry.Name()) {
				continue
			}
			if _, ok := seen[entry.Name()]; ok {
				continue
			}
			signer, err := loadSSHPrivateKey(entry.Name(), keysDir)
			if err != nil {
				continue
			}
			authMethods = append(authMethods, ssh.PublicKeys(signer))
			seen[entry.Name()] = struct{}{}
		}
	}

	return authMethods
}

func defaultSSHKeyDirs(keysDir string) []string {
	if keysDir != "" {
		expanded := expandPath(keysDir)
		if expanded != "" {
			return []string{expanded}
		}
		return nil
	}
	if home := userHomeDir(); home != "" {
		return []string{filepath.Join(home, ".ssh")}
	}
	return nil
}

func isLikelySSHPrivateKeyFile(name string) bool {
	if name == "" {
		return false
	}
	if strings.HasSuffix(name, ".pub") {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return false
	}
	return true
}

func sshAgentAuthMethod() (ssh.AuthMethod, error) {
	socketPath := os.Getenv("SSH_AUTH_SOCK")
	if socketPath == "" {
		return nil, fmt.Errorf("SSH_AUTH_SOCK is not set")
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, err
	}

	agentClient := agent.NewClient(conn)
	signers, err := agentClient.Signers()
	if err != nil {
		conn.Close()
		return nil, err
	}
	if len(signers) == 0 {
		conn.Close()
		return nil, fmt.Errorf("SSH agent has no identities")
	}

	return ssh.PublicKeysCallback(agentClient.Signers), nil
}

func loadSSHPrivateKey(path, keysDir string) (ssh.Signer, error) {
	expanded := expandPath(path)
	data, err := os.ReadFile(expanded)
	if err == nil {
		return ssh.ParsePrivateKey(data)
	}

	if fallback := sshFallbackPath(expanded, keysDir); fallback != "" {
		data, err2 := os.ReadFile(fallback)
		if err2 == nil {
			return ssh.ParsePrivateKey(data)
		}
	}

	return nil, err
}

func sshFallbackPath(path, keysDir string) string {
	if filepath.IsAbs(path) {
		return ""
	}
	if strings.Contains(path, string(os.PathSeparator)) {
		return ""
	}
	if keysDir == "" {
		if home := userHomeDir(); home != "" {
			return filepath.Join(home, ".ssh", path)
		}
		return ""
	}
	return filepath.Join(expandPath(keysDir), path)
}
