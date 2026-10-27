package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SSHConfigTarget holds the connection facts OpenSSH resolves for an alias.
type SSHConfigTarget struct {
	Alias         string
	HostName      string
	User          string
	Port          int
	IdentityFiles []string
	ProxyJump     string
}

// Unresolved reports that ssh_config had no HostName for the alias. `ssh -G`
// always succeeds, echoing the alias back as the hostname when nothing matched,
// so this is the only signal that an alias is absent from the config.
func (t SSHConfigTarget) Unresolved() bool {
	return t.HostName == "" || t.HostName == t.Alias
}

// sshConfigCacheTTL keeps `ssh -G` off the hot path without pinning stale
// values: the MCP server is long-lived and ~/.ssh/config can change under it.
const sshConfigCacheTTL = 15 * time.Second

type sshConfigCacheEntry struct {
	target SSHConfigTarget
	err    error
	at     time.Time
}

var (
	sshConfigMu    sync.Mutex
	sshConfigCache = map[string]sshConfigCacheEntry{}
	sshConfigNow   = time.Now
)

// runSSHDashG is a variable so tests can supply `ssh -G` output without a
// real ssh binary or a real config file.
var runSSHDashG = func(alias string) (string, error) {
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return "", fmt.Errorf("ssh binary not found in PATH: %w", err)
	}
	// -G prints the effective configuration and exits without connecting.
	out, err := exec.Command(sshPath, "-G", alias).Output()
	if err != nil {
		return "", fmt.Errorf("ssh -G %s: %w", alias, err)
	}
	return string(out), nil
}

// ResolveSSHConfig asks OpenSSH to resolve an alias with `ssh -G`, so kgssh
// honours everything ~/.ssh/config can express — Host blocks, Match rules,
// Include files, canonicalisation and per-host IdentityFile — instead of
// keeping a second copy of the same facts.
func ResolveSSHConfig(alias string) (SSHConfigTarget, error) {
	if alias == "" {
		return SSHConfigTarget{}, fmt.Errorf("empty ssh_config alias")
	}

	sshConfigMu.Lock()
	cached, ok := sshConfigCache[alias]
	sshConfigMu.Unlock()
	if ok && sshConfigNow().Sub(cached.at) < sshConfigCacheTTL {
		return cached.target, cached.err
	}

	out, err := runSSHDashG(alias)
	target := SSHConfigTarget{Alias: alias}
	if err == nil {
		target = parseSSHDashG(alias, out)
	}

	sshConfigMu.Lock()
	sshConfigCache[alias] = sshConfigCacheEntry{target: target, err: err, at: sshConfigNow()}
	sshConfigMu.Unlock()

	return target, err
}

// InvalidateSSHConfigCache drops cached `ssh -G` results. Called after kgssh
// itself changes anything that could affect resolution.
func InvalidateSSHConfigCache() {
	sshConfigMu.Lock()
	sshConfigCache = map[string]sshConfigCacheEntry{}
	sshConfigMu.Unlock()
}

// parseSSHDashG reads `ssh -G` output: one lowercase keyword per line followed
// by its value. Unknown keywords are ignored; identityfile repeats.
func parseSSHDashG(alias, out string) SSHConfigTarget {
	target := SSHConfigTarget{Alias: alias}

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, found := strings.Cut(line, " ")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		switch strings.ToLower(key) {
		case "hostname":
			target.HostName = value
		case "user":
			target.User = value
		case "port":
			if p, err := strconv.Atoi(value); err == nil {
				target.Port = p
			}
		case "identityfile":
			target.IdentityFiles = append(target.IdentityFiles, expandPath(value))
		case "proxyjump":
			// `ssh -G` prints "none" when ProxyJump is explicitly cleared.
			if !strings.EqualFold(value, "none") {
				target.ProxyJump = value
			}
		}
	}

	return target
}
