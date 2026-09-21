package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ImportSSHConfigFile parses an OpenSSH config file and extracts Host blocks.
func ImportSSHConfigFile(path string) (map[string]Entry, error) {
	if path == "" {
		home := userHomeDir()
		path = filepath.Join(home, ".ssh", "config")
	}

	expanded := expandPath(path)
	file, err := os.Open(expanded)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	entries := make(map[string]Entry)
	var currentHosts []string
	var currentEntry Entry

	commitCurrent := func() {
		for _, hostAlias := range currentHosts {
			if hostAlias != "" && !strings.ContainsAny(hostAlias, "*?") {
				// Inherit Host if not explicitly specified
				entryCopy := currentEntry
				if entryCopy.Host == "" {
					entryCopy.Host = hostAlias
				}
				entries[hostAlias] = entryCopy
			}
		}
		currentHosts = nil
		currentEntry = Entry{}
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		key := strings.ToLower(parts[0])
		val := strings.Join(parts[1:], " ")

		if key == "host" {
			commitCurrent()
			currentHosts = parts[1:]
			continue
		}

		if len(currentHosts) == 0 {
			continue
		}

		switch key {
		case "hostname":
			currentEntry.Host = val
		case "user":
			currentEntry.User = val
		case "port":
			if p, err := strconv.Atoi(val); err == nil {
				currentEntry.Port = p
			}
		case "identityfile":
			currentEntry.Identity = val
		case "proxyjump":
			currentEntry.ProxyJump = val
		}
	}
	commitCurrent()

	return entries, scanner.Err()
}
