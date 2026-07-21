package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{"entries":{"prod":{"user":"ubuntu","host":"prod.example.com","port":2222,"extraArgs":["-i","/tmp/id_rsa"]}}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig returned error: %v", err)
	}

	if got := cfg.Entries["prod"].User; got != "ubuntu" {
		t.Fatalf("user mismatch: got %q", got)
	}
	if got := cfg.Entries["prod"].Port; got != 2222 {
		t.Fatalf("port mismatch: got %d", got)
	}
	if !reflect.DeepEqual(cfg.Entries["prod"].ExtraArgs, []string{"-i", "/tmp/id_rsa"}) {
		t.Fatalf("extra args mismatch: %#v", cfg.Entries["prod"].ExtraArgs)
	}
}

func TestBuildSSHClientConfig(t *testing.T) {
	entry := entry{User: "deploy", Host: "server.internal", Port: 2222, Password: "secret"}
	cfg, err := buildSSHClientConfig(entry, "")
	if err != nil {
		t.Fatalf("buildSSHClientConfig returned error: %v", err)
	}
	if cfg.User != "deploy" {
		t.Fatalf("user mismatch: got %q", cfg.User)
	}
	if len(cfg.Auth) != 1 {
		t.Fatalf("expected one auth method, got %d", len(cfg.Auth))
	}
	if cfg.HostKeyCallback == nil {
		t.Fatalf("expected host key callback to be set")
	}
}

func TestBuildSSHClientConfigExpandsHomeInIdentityPath(t *testing.T) {
	tempHome := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tempHome); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	if oldHome == "" {
		defer os.Unsetenv("HOME")
	} else {
		defer os.Setenv("HOME", oldHome)
	}

	if _, err := writeTempSSHPrivateKey(t, tempHome, "id_rsa_chat360"); err != nil {
		t.Fatalf("write SSH private key: %v", err)
	}

	entry := entry{User: "deploy", Host: "server.internal", ExtraArgs: []string{"-i", "~/id_rsa_chat360"}}
	cfg, err := buildSSHClientConfig(entry, "")
	if err != nil {
		t.Fatalf("buildSSHClientConfig returned error: %v", err)
	}
	if len(cfg.Auth) != 1 {
		t.Fatalf("expected one auth method, got %d", len(cfg.Auth))
	}
}

func TestBuildSSHClientConfigFallsBackToDotSSHDir(t *testing.T) {
	tempHome := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tempHome); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	if oldHome == "" {
		defer os.Unsetenv("HOME")
	} else {
		defer os.Setenv("HOME", oldHome)
	}

	dotSSH := filepath.Join(tempHome, ".ssh")
	if err := os.Mkdir(dotSSH, 0o700); err != nil {
		t.Fatalf("create .ssh dir: %v", err)
	}
	if _, err := writeTempSSHPrivateKey(t, dotSSH, "id_rsa_chat360"); err != nil {
		t.Fatalf("write SSH private key: %v", err)
	}

	entry := entry{User: "deploy", Host: "server.internal", ExtraArgs: []string{"-i", "id_rsa_chat360"}}
	cfg, err := buildSSHClientConfig(entry, "")
	if err != nil {
		t.Fatalf("buildSSHClientConfig returned error: %v", err)
	}
	if len(cfg.Auth) != 1 {
		t.Fatalf("expected one auth method, got %d", len(cfg.Auth))
	}
}

func TestBuildSSHClientConfigUsesDefaultIdentityFiles(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	dotSSH := filepath.Join(tempHome, ".ssh")
	if err := os.Mkdir(dotSSH, 0o700); err != nil {
		t.Fatalf("create .ssh dir: %v", err)
	}
	if _, err := writeTempSSHPrivateKey(t, dotSSH, "id_rsa"); err != nil {
		t.Fatalf("write SSH private key: %v", err)
	}

	entry := entry{User: "deploy", Host: "server.internal"}
	cfg, err := buildSSHClientConfig(entry, "")
	if err != nil {
		t.Fatalf("buildSSHClientConfig returned error: %v", err)
	}
	if len(cfg.Auth) != 1 {
		t.Fatalf("expected one auth method, got %d", len(cfg.Auth))
	}
}

func writeTempSSHPrivateKey(t *testing.T, dir, fileName string) (string, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	})
	path := filepath.Join(dir, fileName)
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		return "", err
	}
	return path, nil
}
