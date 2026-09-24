package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// ExecResult captures the output and timing of an executed remote command.
type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Duration float64
}

type pooledClient struct {
	client   *ssh.Client
	lastUsed time.Time
}

// SSHPool maintains active, reusable SSH connections to configured remote servers.
type SSHPool struct {
	mu      sync.Mutex
	clients map[string]*pooledClient
}

var globalPool = &SSHPool{
	clients: make(map[string]*pooledClient),
}

// GetClient retrieves an active SSH client from the pool or opens a new connection.
func (p *SSHPool) GetClient(serverName string, timeout time.Duration) (*ssh.Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	cfg, err := loadConfig(resolveConfigPath())
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	entry, ok := cfg.Entries[serverName]
	if !ok {
		return nil, fmt.Errorf("server %q not found in configuration", serverName)
	}

	if pc, exists := p.clients[serverName]; exists && pc.client != nil {
		// Probe connection health with a keepalive request
		_, _, err := pc.client.SendRequest("keepalive@openssh.com", true, nil)
		if err == nil {
			pc.lastUsed = time.Now()
			return pc.client, nil
		}
		// Stale / disconnected connection, clean up
		_ = pc.client.Close()
		delete(p.clients, serverName)
	}

	client, err := dialEntry(entry, cfg.KeysDir, timeout)
	if err != nil {
		return nil, err
	}

	p.clients[serverName] = &pooledClient{
		client:   client,
		lastUsed: time.Now(),
	}
	return client, nil
}

// Close closes and removes a client connection for a server from the pool.
func (p *SSHPool) Close(serverName string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if pc, exists := p.clients[serverName]; exists {
		if pc.client != nil {
			_ = pc.client.Close()
		}
		delete(p.clients, serverName)
	}
}

// CloseAll closes all active connections in the pool.
func (p *SSHPool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for name, pc := range p.clients {
		if pc.client != nil {
			_ = pc.client.Close()
		}
		delete(p.clients, name)
	}
}

func dialEntry(entry Entry, keysDir string, timeout time.Duration) (*ssh.Client, error) {
	clientConfig, err := buildSSHClientConfig(entry, keysDir)
	if err != nil {
		return nil, fmt.Errorf("build client config: %w", err)
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	clientConfig.Timeout = timeout

	port := entry.Port
	if port <= 0 {
		port = 22
	}
	addr := net.JoinHostPort(entry.Host, strconv.Itoa(port))

	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		user := entry.User
		if user == "" {
			user = "root"
		}
		return nil, fmt.Errorf("connect to %s (%s@%s): %w", addr, user, entry.Host, err)
	}

	clientConn, chans, reqs, err := ssh.NewClientConn(conn, addr, clientConfig)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("handshake with %s: %w", addr, err)
	}

	return ssh.NewClient(clientConn, chans, reqs), nil
}

// RunRemoteCommand executes a shell command on an SSH client with timeout and optional PTY.
func RunRemoteCommand(client *ssh.Client, cmd string, timeout time.Duration, pty bool) (*ExecResult, error) {
	startTime := time.Now()

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	defer session.Close()

	if pty {
		modes := ssh.TerminalModes{
			ssh.ECHO:          0,
			ssh.TTY_OP_ISPEED: 14400,
			ssh.TTY_OP_OSPEED: 14400,
		}
		_ = session.RequestPty("xterm", 40, 80, modes)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	session.Stderr = &stderrBuf

	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmdDone := make(chan error, 1)
	go func() {
		cmdDone <- session.Run(cmd)
	}()

	var runErr error
	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		return nil, fmt.Errorf("command timed out after %v", timeout)
	case runErr = <-cmdDone:
	}

	duration := time.Since(startTime).Seconds()
	duration = float64(int(duration*1000)) / 1000.0

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*ssh.ExitError); ok {
			exitCode = exitErr.ExitStatus()
		} else {
			return nil, runErr
		}
	}

	return &ExecResult{
		ExitCode: exitCode,
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		Duration: duration,
	}, nil
}
