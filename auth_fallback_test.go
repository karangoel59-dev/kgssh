package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"golang.org/x/crypto/ssh"
)

// startTestSSHServer accepts only the given public key and returns its port.
func startTestSSHServer(t *testing.T, allowed ssh.PublicKey) int {
	t.Helper()

	hostPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatalf("host signer: %v", err)
	}

	serverCfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(key.Marshal(), allowed.Marshal()) {
				return nil, nil
			}
			return nil, fmt.Errorf("key rejected")
		},
	}
	serverCfg.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				sconn, chans, reqs, err := ssh.NewServerConn(conn, serverCfg)
				if err != nil {
					conn.Close()
					return
				}
				defer sconn.Close()
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					ch.Reject(ssh.Prohibited, "no channels in test")
				}
			}()
		}
	}()

	return listener.Addr().(*net.TCPAddr).Port
}

func TestDialFallsBackToDefaultKeyWhenExplicitIdentityRejected(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	dotSSH := filepath.Join(tempHome, ".ssh")
	if err := os.Mkdir(dotSSH, 0o700); err != nil {
		t.Fatalf("create .ssh dir: %v", err)
	}
	explicitPath, err := writeTempSSHPrivateKey(t, t.TempDir(), "explicit_key")
	if err != nil {
		t.Fatalf("write explicit key: %v", err)
	}
	defaultPath, err := writeTempSSHPrivateKey(t, dotSSH, "id_ed25519")
	if err != nil {
		t.Fatalf("write default key: %v", err)
	}

	defaultSigner, err := loadSSHPrivateKey(defaultPath, "")
	if err != nil {
		t.Fatalf("load default key: %v", err)
	}
	port := startTestSSHServer(t, defaultSigner.PublicKey())

	e := Entry{User: "deploy", Host: "127.0.0.1", Port: port, Identity: explicitPath}
	client, err := dialEntry(e, "", 5*time.Second)
	if err != nil {
		t.Fatalf("expected fallback to default ~/.ssh key to authenticate, got: %v", err)
	}
	client.Close()
}

func TestBuildSSHClientConfigDedupesRepeatedKeys(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	dotSSH := filepath.Join(tempHome, ".ssh")
	if err := os.Mkdir(dotSSH, 0o700); err != nil {
		t.Fatalf("create .ssh dir: %v", err)
	}
	keyPath, err := writeTempSSHPrivateKey(t, dotSSH, "id_rsa")
	if err != nil {
		t.Fatalf("write key: %v", err)
	}

	signer, err := loadSSHPrivateKey(keyPath, "")
	if err != nil {
		t.Fatalf("load key: %v", err)
	}
	if got := dedupeSigners([]ssh.Signer{signer, signer}); len(got) != 1 {
		t.Fatalf("expected 1 signer after dedupe, got %d", len(got))
	}

	// Identity set, same file also listed via -i and found by the ~/.ssh scan:
	// still exactly one publickey method.
	e := Entry{User: "deploy", Host: "h", Identity: keyPath, ExtraArgs: []string{"-i", keyPath}}
	cfg, err := buildSSHClientConfig(e, "")
	if err != nil {
		t.Fatalf("buildSSHClientConfig: %v", err)
	}
	if len(cfg.Auth) != 1 {
		t.Fatalf("expected a single publickey auth method, got %d", len(cfg.Auth))
	}
}

func TestMCPAddServerDoesNotOverwriteUnparseableConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("KGSSH_CONFIG", configPath)

	broken := []byte(`{"entries": {"prod": {"host": "1.2.3.4",}}}`) // trailing comma
	if err := os.WriteFile(configPath, broken, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	res, err := handleAddServer(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "add_server",
			Arguments: map[string]any{"name": "new", "host": "5.6.7.8"},
		},
	})
	if err != nil {
		t.Fatalf("handleAddServer error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected tool error for unparseable config, got %#v", res.Content)
	}
	if tc, ok := mcp.AsTextContent(res.Content[0]); !ok || !strings.Contains(tc.Text, "not overwriting") {
		t.Fatalf("unexpected response: %#v", res.Content)
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !bytes.Equal(after, broken) {
		t.Fatalf("config was modified:\n%s", after)
	}
}
