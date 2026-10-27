package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubSSHDashG replaces the `ssh -G` call for the duration of a test and clears
// the resolution cache on both sides, so cases cannot leak into each other.
func stubSSHDashG(t *testing.T, outputs map[string]string) {
	t.Helper()
	original := runSSHDashG
	InvalidateSSHConfigCache()
	runSSHDashG = func(alias string) (string, error) {
		out, ok := outputs[alias]
		if !ok {
			return "", fmt.Errorf("no stub for alias %q", alias)
		}
		return out, nil
	}
	t.Cleanup(func() {
		runSSHDashG = original
		InvalidateSSHConfigCache()
	})
}

func TestParseSSHDashG(t *testing.T) {
	out := strings.Join([]string{
		"user azureuser",
		"hostname 10.66.66.5",
		"port 2222",
		"identityfile ~/.ssh/id_ed25519_worklaptop",
		"identityfile ~/.ssh/id_rsa",
		"proxyjump bastion",
		"forwardagent no",
		"", // blank lines are ignored
		"loglevel",
	}, "\n")

	target := parseSSHDashG("gcloud-work", out)

	if target.HostName != "10.66.66.5" {
		t.Fatalf("HostName = %q, want 10.66.66.5", target.HostName)
	}
	if target.User != "azureuser" {
		t.Fatalf("User = %q, want azureuser", target.User)
	}
	if target.Port != 2222 {
		t.Fatalf("Port = %d, want 2222", target.Port)
	}
	if len(target.IdentityFiles) != 2 {
		t.Fatalf("IdentityFiles = %v, want 2 entries", target.IdentityFiles)
	}
	if target.ProxyJump != "bastion" {
		t.Fatalf("ProxyJump = %q, want bastion", target.ProxyJump)
	}
	if target.Unresolved() {
		t.Fatal("target should be resolved")
	}
}

func TestParseSSHDashGProxyJumpNoneIsNotAJump(t *testing.T) {
	target := parseSSHDashG("host1", "hostname 10.0.0.1\nproxyjump none\n")
	if target.ProxyJump != "" {
		t.Fatalf("ProxyJump = %q, want empty for \"none\"", target.ProxyJump)
	}
}

func TestSSHConfigTargetUnresolvedWhenHostnameEchoesAlias(t *testing.T) {
	// `ssh -G` exits 0 for unknown aliases and echoes the alias as the hostname.
	target := parseSSHDashG("not-in-config", "hostname not-in-config\nport 22\n")
	if !target.Unresolved() {
		t.Fatal("alias absent from ssh_config should report Unresolved")
	}
}

func TestEntryWithHostDoesNotConsultSSHConfig(t *testing.T) {
	called := false
	original := runSSHDashG
	runSSHDashG = func(string) (string, error) {
		called = true
		return "", nil
	}
	t.Cleanup(func() { runSSHDashG = original })

	e := Entry{User: "root", Host: "10.0.0.9", Port: 22}
	resolved, err := e.Effective("prod1", "")
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if called {
		t.Fatal("entry with an explicit host must not run ssh -G")
	}
	if resolved.Host != "10.0.0.9" || resolved.User != "root" {
		t.Fatalf("entry was altered: %+v", resolved)
	}
}

func TestEffectiveFillsFromSSHConfig(t *testing.T) {
	keysDir := t.TempDir()
	keyPath := filepath.Join(keysDir, "id_ed25519_worklaptop")
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}

	stubSSHDashG(t, map[string]string{
		"gcloud-work": "hostname 10.66.66.5\nuser azureuser\nport 22\nidentityfile " + keyPath + "\n",
	})

	resolved, err := Entry{}.Effective("gcloud-work", keysDir)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if resolved.Host != "10.66.66.5" {
		t.Fatalf("Host = %q, want 10.66.66.5", resolved.Host)
	}
	if resolved.User != "azureuser" {
		t.Fatalf("User = %q, want azureuser", resolved.User)
	}
	if resolved.Port != 22 {
		t.Fatalf("Port = %d, want 22", resolved.Port)
	}
	if resolved.Identity != keyPath {
		t.Fatalf("Identity = %q, want %q", resolved.Identity, keyPath)
	}
}

func TestEffectiveSkipsIdentityFilesThatDoNotExist(t *testing.T) {
	stubSSHDashG(t, map[string]string{
		"host1": "hostname 10.0.0.1\nidentityfile /nonexistent/key\n",
	})

	resolved, err := Entry{}.Effective("host1", "")
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if resolved.Identity != "" {
		t.Fatalf("Identity = %q, want empty when the key file is missing", resolved.Identity)
	}
}

func TestEffectiveEntryFieldsOverrideSSHConfig(t *testing.T) {
	stubSSHDashG(t, map[string]string{
		"host1": "hostname 10.0.0.1\nuser fromconfig\nport 22\n",
	})

	resolved, err := Entry{User: "override", Port: 2200}.Effective("host1", "")
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if resolved.User != "override" {
		t.Fatalf("User = %q, want the entry's value to win", resolved.User)
	}
	if resolved.Port != 2200 {
		t.Fatalf("Port = %d, want the entry's value to win", resolved.Port)
	}
	if resolved.Host != "10.0.0.1" {
		t.Fatalf("Host = %q, want it filled from ssh_config", resolved.Host)
	}
}

func TestEffectiveErrorsWhenAliasMissingFromSSHConfig(t *testing.T) {
	stubSSHDashG(t, map[string]string{
		"ghost": "hostname ghost\nport 22\n",
	})

	if _, err := (Entry{}).Effective("ghost", ""); err == nil {
		t.Fatal("expected an error for an alias with no HostName in ssh_config")
	}
}

func TestResolveSSHConfigCachesResult(t *testing.T) {
	original := runSSHDashG
	InvalidateSSHConfigCache()
	calls := 0
	runSSHDashG = func(string) (string, error) {
		calls++
		return "hostname 10.0.0.1\n", nil
	}
	t.Cleanup(func() {
		runSSHDashG = original
		InvalidateSSHConfigCache()
	})

	for i := 0; i < 3; i++ {
		if _, err := ResolveSSHConfig("cached-host"); err != nil {
			t.Fatalf("ResolveSSHConfig: %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("ssh -G ran %d times, want 1 (results should be cached)", calls)
	}

	InvalidateSSHConfigCache()
	if _, err := ResolveSSHConfig("cached-host"); err != nil {
		t.Fatalf("ResolveSSHConfig after invalidate: %v", err)
	}
	if calls != 2 {
		t.Fatalf("ssh -G ran %d times, want 2 after cache invalidation", calls)
	}
}

func TestBuildSSHArgsDelegatesAliasToSSH(t *testing.T) {
	// The whole point: ssh gets the alias and resolves it, so no host, user,
	// port or identity is restated on the command line.
	args := BuildSSHArgs("gcloud-work", Entry{Description: "metadata only"}, "", true)

	if len(args) != 1 || args[0] != "gcloud-work" {
		t.Fatalf("args = %v, want exactly [gcloud-work]", args)
	}
}

func TestBuildSSHArgsDelegatedEntryKeepsExplicitOverrides(t *testing.T) {
	e := Entry{User: "root", Port: 22, ExtraArgs: []string{"-A"}}
	args := BuildSSHArgs("prod1", e, "", true)

	joined := strings.Join(args, " ")
	// Port 22 is normally omitted, but an entry that states it is overriding
	// whatever ssh_config says, so it has to be emitted.
	if !strings.Contains(joined, "-p 22") {
		t.Fatalf("args = %v, want an explicit -p 22 override", args)
	}
	if !strings.Contains(joined, "-A") {
		t.Fatalf("args = %v, want ExtraArgs preserved", args)
	}
	if args[len(args)-1] != "root@prod1" {
		t.Fatalf("target = %q, want root@prod1", args[len(args)-1])
	}
}

func TestBuildSSHCommandStringDelegated(t *testing.T) {
	got := BuildSSHCommandString("langfuse", Entry{}, "")
	if got != "ssh langfuse" {
		t.Fatalf("command = %q, want \"ssh langfuse\"", got)
	}
}

func TestGenerateShellDefinitionDelegated(t *testing.T) {
	out := GenerateShellDefinition("langfuse", Entry{Description: "Langfuse box"}, "", "function", "zsh")
	if !strings.Contains(out, "ssh langfuse \"$@\"") {
		t.Fatalf("definition = %q, want it to call ssh with the alias", out)
	}
}

func TestDelegatesToSSHConfig(t *testing.T) {
	cases := []struct {
		name string
		e    Entry
		want bool
	}{
		{"no host", Entry{}, true},
		{"blank host", Entry{Host: "   "}, true},
		{"metadata only", Entry{Description: "x", Info: &ServerInfo{Notes: "y"}}, true},
		{"explicit host", Entry{Host: "10.0.0.1"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.e.DelegatesToSSHConfig(); got != tc.want {
				t.Fatalf("DelegatesToSSHConfig() = %v, want %v", got, tc.want)
			}
		})
	}
}
