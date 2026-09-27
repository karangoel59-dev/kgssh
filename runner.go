package main

import (
	"fmt"
	"os"
	"os/exec"
)

// RunSSH launches native OpenSSH for an Entry, passing along any trailing arguments.
func RunSSH(e Entry, keysDir string, extraArgs []string) error {
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh binary not found in PATH: %w", err)
	}

	sshArgs := BuildSSHArgs(e, keysDir, false)
	sshArgs = append(sshArgs, extraArgs...)

	var cmd *exec.Cmd
	if e.Password != "" {
		if sshpassPath, err := exec.LookPath("sshpass"); err == nil {
			// -e reads SSHPASS from the environment; -p would expose the password in ps.
			allArgs := append([]string{"-e", sshPath}, sshArgs...)
			cmd = exec.Command(sshpassPath, allArgs...)
			cmd.Env = append(os.Environ(), "SSHPASS="+e.Password)
		} else {
			fmt.Fprintln(os.Stderr, "[kgssh] Note: password is configured, but 'sshpass' is not installed.")
			fmt.Fprintln(os.Stderr, "[kgssh] Connecting with standard ssh (you may be prompted for password)...")
			cmd = exec.Command(sshPath, sshArgs...)
		}
	} else {
		cmd = exec.Command(sshPath, sshArgs...)
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	return nil
}
