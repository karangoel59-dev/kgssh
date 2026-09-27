package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"":                 "''",
		"deploy@10.0.0.1":  "deploy@10.0.0.1",
		"~/.ssh/id_rsa":    "~/.ssh/id_rsa",
		"~/my keys/id":     "~/'my keys/id'",
		"pa$$w0rd":         "'pa$$w0rd'",
		"it's":             `'it'\''s'`,
		"a b":              "'a b'",
		"`whoami`":         "'`whoami`'",
		"StrictHostKey=no": "StrictHostKey=no",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// Runs the generated shell function in real bash with stub ssh/sshpass
// commands, checking that hostile values arrive byte-for-byte.
func TestGeneratedFunctionPassesValuesLiterally(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}

	password := `p'a$$w"o\rd` + "`id`"
	e := Entry{
		User:      "deploy",
		Host:      "10.0.0.1",
		Password:  password,
		ExtraArgs: []string{"-o", "ProxyCommand=nc -X 5 $x %h %p"},
	}
	def := GenerateShellDefinition("prod", e, "", "function", "bash")

	script := `ssh() { printf '%s\n' "SSHPASS=$SSHPASS" "$@"; }
sshpass() { shift; "$@"; }
` + def + `
prod uptime`
	out, err := exec.Command(bash, "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("bash failed: %v\n%s\nscript:\n%s", err, out, script)
	}

	want := strings.Join([]string{
		"SSHPASS=" + password,
		"-o", "ProxyCommand=nc -X 5 $x %h %p",
		"deploy@10.0.0.1",
		"uptime",
	}, "\n") + "\n"
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestGeneratedCommandHidesPasswordFromArgs(t *testing.T) {
	cmd := BuildSSHCommandString("x", Entry{User: "u", Host: "h", Password: "secret"}, "")
	if strings.Contains(cmd, "-p ") {
		t.Fatalf("password passed via sshpass -p: %s", cmd)
	}
	if !strings.HasPrefix(cmd, "SSHPASS=secret sshpass -e ssh ") {
		t.Fatalf("unexpected command: %s", cmd)
	}
}

func TestDescriptionNewlineCannotInjectCode(t *testing.T) {
	e := Entry{User: "u", Host: "h", Description: "web node\nrm -rf ~"}
	out := GenerateShellDefinition("web", e, "", "function", "zsh")
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "rm ") {
			t.Fatalf("description escaped comment line:\n%s", out)
		}
	}
}

func TestSyncAliasesFileRestrictsPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aliases.sh")
	// Simulate a file created by an older version with 0644.
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg := Config{Entries: map[string]Entry{"a": {Host: "h", Password: "pw"}}}
	if err := SyncAliasesFile(cfg, path, "function", "zsh"); err != nil {
		t.Fatalf("SyncAliasesFile: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("aliases file mode = %04o, want 0600", perm)
	}
}
