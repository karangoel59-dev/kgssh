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
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type testServerOpts struct {
	// hangGlobalRequests never answers global requests (e.g. keepalives),
	// simulating a half-dead connection.
	hangGlobalRequests bool
	// serveSFTP accepts session channels and serves the local filesystem over SFTP.
	serveSFTP bool
}

// startTestSSHServer accepts only the given public key and returns its port.
func startTestSSHServer(t *testing.T, allowed ssh.PublicKey) int {
	return startTestSSHServerWith(t, allowed, testServerOpts{})
}

func startTestSSHServerWith(t *testing.T, allowed ssh.PublicKey, opts testServerOpts) int {
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
				if !opts.hangGlobalRequests {
					go ssh.DiscardRequests(reqs)
				}
				for ch := range chans {
					if !opts.serveSFTP || ch.ChannelType() != "session" {
						ch.Reject(ssh.Prohibited, "no channels in test")
						continue
					}
					channel, chReqs, err := ch.Accept()
					if err != nil {
						continue
					}
					go serveSFTPSession(channel, chReqs)
				}
			}()
		}
	}()

	return listener.Addr().(*net.TCPAddr).Port
}

func serveSFTPSession(channel ssh.Channel, reqs <-chan *ssh.Request) {
	defer channel.Close()
	for req := range reqs {
		// Subsystem payload is an SSH string: 4-byte length + "sftp".
		ok := req.Type == "subsystem" && len(req.Payload) >= 4 && string(req.Payload[4:]) == "sftp"
		req.Reply(ok, nil)
		if !ok {
			continue
		}
		go ssh.DiscardRequests(reqs)
		if srv, err := sftp.NewServer(channel); err == nil {
			_ = srv.Serve()
		}
		return
	}
}

// writeTestServerConfig points KGSSH_CONFIG at a config with the given entries
// and resets the global pool around the test.
func writeTestServerConfig(t *testing.T, entries map[string]Entry) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("KGSSH_CONFIG", configPath)
	if err := saveConfig(configPath, Config{Entries: entries}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	globalPool.CloseAll()
	t.Cleanup(globalPool.CloseAll)
}

// newTestKey writes a private key and returns its path and public key.
func newTestKey(t *testing.T) (string, ssh.PublicKey) {
	t.Helper()
	path, err := writeTempSSHPrivateKey(t, t.TempDir(), "id_test")
	if err != nil {
		t.Fatalf("write key: %v", err)
	}
	signer, err := loadSSHPrivateKey(path, "")
	if err != nil {
		t.Fatalf("load key: %v", err)
	}
	return path, signer.PublicKey()
}

func TestWriteFileKeepsContentVerbatim(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv("HOME", t.TempDir())
	keyPath, pub := newTestKey(t)
	port := startTestSSHServerWith(t, pub, testServerOpts{serveSFTP: true})
	writeTestServerConfig(t, map[string]Entry{
		"local": {User: "deploy", Host: "127.0.0.1", Port: port, Identity: keyPath},
	})

	target := filepath.Join(t.TempDir(), "app.yaml")
	content := "  indented: true\nlast: line\n\n"
	res, err := handleWriteFile(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "write_file",
			Arguments: map[string]any{"server": "local", "remote_path": target, "content": content},
		},
	})
	if err != nil {
		t.Fatalf("handleWriteFile: %v", err)
	}
	if tc, ok := mcp.AsTextContent(res.Content[0]); !ok || !strings.Contains(tc.Text, "Successfully wrote") {
		t.Fatalf("unexpected response: %#v", res.Content)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(got) != content {
		t.Fatalf("content changed:\ngot  %q\nwant %q", got, content)
	}
}

func TestPoolHungServerDoesNotBlockOtherServers(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv("HOME", t.TempDir())
	oldProbe := keepaliveProbeTimeout
	keepaliveProbeTimeout = time.Second
	t.Cleanup(func() { keepaliveProbeTimeout = oldProbe })

	keyPath, pub := newTestKey(t)
	hungPort := startTestSSHServerWith(t, pub, testServerOpts{hangGlobalRequests: true})
	okPort := startTestSSHServer(t, pub)
	writeTestServerConfig(t, map[string]Entry{
		"hung": {User: "deploy", Host: "127.0.0.1", Port: hungPort, Identity: keyPath},
		"ok":   {User: "deploy", Host: "127.0.0.1", Port: okPort, Identity: keyPath},
	})

	first, err := globalPool.GetClient("hung", 5*time.Second)
	if err != nil {
		t.Fatalf("initial dial to hung server: %v", err)
	}

	// Reusing the pooled "hung" connection triggers a keepalive probe that never gets a reply.
	hungDone := make(chan error, 1)
	var second *ssh.Client
	go func() {
		c, err := globalPool.GetClient("hung", 5*time.Second)
		second = c
		hungDone <- err
	}()
	time.Sleep(100 * time.Millisecond) // let the probe start

	okDone := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := globalPool.GetClient("ok", 5*time.Second)
		okDone <- err
	}()
	select {
	case err := <-okDone:
		if err != nil {
			t.Fatalf("dial ok server: %v", err)
		}
		if elapsed := time.Since(start); elapsed > 700*time.Millisecond {
			t.Fatalf("ok server waited %v behind the hung probe", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("GetClient for a healthy server blocked behind a hung keepalive probe")
	}

	select {
	case err := <-hungDone:
		if err != nil {
			t.Fatalf("redial after failed probe: %v", err)
		}
		if second == first {
			t.Fatal("expected stale connection to be replaced after probe timeout")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("keepalive probe never timed out")
	}
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
