package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBuildSSHArgs(t *testing.T) {
	e := Entry{
		User:      "ubuntu",
		Host:      "prod.example.com",
		Port:      2222,
		Identity:  "~/.ssh/id_rsa",
		ProxyJump: "bastion",
		ExtraArgs: []string{"-C", "-o", "StrictHostKeyChecking=no"},
	}

	args := BuildSSHArgs(e, "", true)
	expected := []string{
		"-p", "2222",
		"-i", "~/.ssh/id_rsa",
		"-J", "bastion",
		"-C", "-o", "StrictHostKeyChecking=no",
		"ubuntu@prod.example.com",
	}

	if !reflect.DeepEqual(args, expected) {
		t.Fatalf("expected %#v, got %#v", expected, args)
	}
}

func TestBuildSSHArgsOmitsPort22(t *testing.T) {
	e := Entry{
		User: "root",
		Host: "1.2.3.4",
		Port: 22,
	}

	args := BuildSSHArgs(e, "", true)
	expected := []string{"root@1.2.3.4"}

	if !reflect.DeepEqual(args, expected) {
		t.Fatalf("expected %#v, got %#v", expected, args)
	}
}

func TestBuildSSHArgsExtractsIdentityFromExtraArgs(t *testing.T) {
	e := Entry{
		User:      "root",
		Host:      "1.2.3.4",
		Port:      22,
		ExtraArgs: []string{"-i", "/custom/key", "-o", "ConnectTimeout=5"},
	}

	args := BuildSSHArgs(e, "", true)
	expected := []string{
		"-i", "/custom/key",
		"-o", "ConnectTimeout=5",
		"root@1.2.3.4",
	}

	if !reflect.DeepEqual(args, expected) {
		t.Fatalf("expected %#v, got %#v", expected, args)
	}
}

func TestBuildSSHCommandString(t *testing.T) {
	e := Entry{
		User:     "deploy",
		Host:     "10.0.0.1",
		Port:     3022,
		Identity: "~/.ssh/id_ed25519",
	}

	cmdStr := BuildSSHCommandString("test-server", e, "")
	expected := "ssh -p 3022 -i ~/.ssh/id_ed25519 deploy@10.0.0.1"

	if cmdStr != expected {
		t.Fatalf("expected %q, got %q", expected, cmdStr)
	}
}

func TestGenerateShellDefinitionFunction(t *testing.T) {
	e := Entry{
		User:        "deploy",
		Host:        "app.internal",
		Description: "Production web node",
	}

	out := GenerateShellDefinition("prod-web", e, "", "function", "zsh")
	if !strings.Contains(out, "prod-web() {") {
		t.Fatalf("expected function definition, got:\n%s", out)
	}
	if !strings.Contains(out, `ssh deploy@app.internal "$@"`) {
		t.Fatalf("expected ssh command with \"$@\", got:\n%s", out)
	}
	if !strings.Contains(out, "# prod-web: Production web node") {
		t.Fatalf("expected description comment, got:\n%s", out)
	}
}

func TestGenerateShellDefinitionAlias(t *testing.T) {
	e := Entry{
		User: "deploy",
		Host: "app.internal",
	}

	out := GenerateShellDefinition("prod-web", e, "", "alias", "zsh")
	expected := `alias prod-web="ssh deploy@app.internal"` + "\n"
	if out != expected {
		t.Fatalf("expected %q, got %q", expected, out)
	}
}

func TestGenerateShellDefinitionFish(t *testing.T) {
	e := Entry{
		User: "deploy",
		Host: "app.internal",
	}

	outFunc := GenerateShellDefinition("prod-web", e, "", "function", "fish")
	if !strings.Contains(outFunc, "function prod-web\n    ssh deploy@app.internal $argv\nend\n") {
		t.Fatalf("unexpected fish function: %s", outFunc)
	}

	outAlias := GenerateShellDefinition("prod-web", e, "", "alias", "fish")
	if !strings.Contains(outAlias, `alias prod-web "ssh deploy@app.internal"`) {
		t.Fatalf("unexpected fish alias: %s", outAlias)
	}
}

func TestSyncAliasesFile(t *testing.T) {
	tmpDir := t.TempDir()
	aliasPath := filepath.Join(tmpDir, "aliases.sh")

	cfg := Config{
		Entries: map[string]Entry{
			"alpha": {Host: "1.1.1.1", User: "root"},
			"beta":  {Host: "2.2.2.2", User: "admin", Port: 2200},
		},
	}

	if err := SyncAliasesFile(cfg, aliasPath, "function", "zsh"); err != nil {
		t.Fatalf("SyncAliasesFile error: %v", err)
	}

	data, err := os.ReadFile(aliasPath)
	if err != nil {
		t.Fatalf("read synced file error: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "alpha() {") || !strings.Contains(content, "beta() {") {
		t.Fatalf("synced file missing functions:\n%s", content)
	}
}

func TestInstallShellHook(t *testing.T) {
	tmpDir := t.TempDir()
	rcPath := filepath.Join(tmpDir, ".zshrc")
	aliasPath := filepath.Join(tmpDir, "aliases.sh")

	// Initially empty .zshrc
	if err := os.WriteFile(rcPath, []byte("# existing config\n"), 0o644); err != nil {
		t.Fatalf("write rc file error: %v", err)
	}

	installed, err := InstallShellHook(rcPath, aliasPath)
	if err != nil {
		t.Fatalf("InstallShellHook error: %v", err)
	}
	if !installed {
		t.Fatalf("expected installed=true on first run")
	}

	data, _ := os.ReadFile(rcPath)
	if !strings.Contains(string(data), "aliases.sh") {
		t.Fatalf("expected source line in rc file, got:\n%s", string(data))
	}

	// Running a second time should not duplicate the hook
	installed2, err2 := InstallShellHook(rcPath, aliasPath)
	if err2 != nil {
		t.Fatalf("second InstallShellHook error: %v", err2)
	}
	if installed2 {
		t.Fatalf("expected installed=false on second run")
	}
}

func TestImportSSHConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config")
	content := `
Host dev-server
    HostName 192.168.1.50
    User devuser
    Port 2222
    IdentityFile ~/.ssh/id_rsa_dev

Host jump-host
    HostName jump.example.com
    User admin
    ProxyJump bastion

# Wildcard should be ignored
Host *
    ServerAliveInterval 60
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write ssh config: %v", err)
	}

	entries, err := ImportSSHConfigFile(configPath)
	if err != nil {
		t.Fatalf("ImportSSHConfigFile error: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	dev := entries["dev-server"]
	if dev.Host != "192.168.1.50" || dev.User != "devuser" || dev.Port != 2222 || dev.Identity != "~/.ssh/id_rsa_dev" {
		t.Fatalf("unexpected dev-server entry: %#v", dev)
	}

	jump := entries["jump-host"]
	if jump.Host != "jump.example.com" || jump.ProxyJump != "bastion" {
		t.Fatalf("unexpected jump-host entry: %#v", jump)
	}
}

func TestResolveKeyPathFallback(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dotSSH := filepath.Join(tmpHome, ".ssh")
	if err := os.Mkdir(dotSSH, 0o700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}
	keyFile := filepath.Join(dotSSH, "my_custom_key")
	if err := os.WriteFile(keyFile, []byte("dummy-key"), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	// Path with Linux style /home/otheruser/.ssh/my_custom_key
	foreignPath := "/home/otheruser/.ssh/my_custom_key"
	resolved := resolveKeyPath(foreignPath, "")
	if resolved != keyFile {
		t.Fatalf("expected resolved to %q, got %q", keyFile, resolved)
	}
}
