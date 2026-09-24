package main

import (
	"fmt"
	"io"
	"sort"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// SFTPFileInfo holds file metadata for remote directory listing.
type SFTPFileInfo struct {
	Name    string
	IsDir   bool
	Size    int64
	Perm    string
	ModTime string
}

// ReadFileSFTP reads up to maxBytes from a remote file via SFTP.
func ReadFileSFTP(client *ssh.Client, remotePath string, maxBytes int) (string, int64, string, bool, error) {
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return "", 0, "", false, fmt.Errorf("init sftp: %w", err)
	}
	defer sftpClient.Close()

	fi, err := sftpClient.Stat(remotePath)
	if err != nil {
		return "", 0, "", false, fmt.Errorf("stat %s: %w", remotePath, err)
	}

	f, err := sftpClient.Open(remotePath)
	if err != nil {
		return "", 0, "", false, fmt.Errorf("open %s: %w", remotePath, err)
	}
	defer f.Close()

	if maxBytes <= 0 {
		maxBytes = 50000
	}

	buf := make([]byte, maxBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", 0, "", false, fmt.Errorf("read %s: %w", remotePath, err)
	}

	truncated := fi.Size() > int64(n)
	permStr := fmt.Sprintf("%04o", fi.Mode().Perm())

	return string(buf[:n]), fi.Size(), permStr, truncated, nil
}

// WriteFileSFTP writes content into a remote file via SFTP.
func WriteFileSFTP(client *ssh.Client, remotePath string, content string) (int, error) {
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return 0, fmt.Errorf("init sftp: %w", err)
	}
	defer sftpClient.Close()

	f, err := sftpClient.Create(remotePath)
	if err != nil {
		return 0, fmt.Errorf("create %s: %w", remotePath, err)
	}
	defer f.Close()

	n, err := f.Write([]byte(content))
	if err != nil {
		return n, fmt.Errorf("write %s: %w", remotePath, err)
	}
	return n, nil
}

// ListDirectorySFTP returns sorted directory entries via SFTP.
func ListDirectorySFTP(client *ssh.Client, remotePath string) ([]SFTPFileInfo, error) {
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return nil, fmt.Errorf("init sftp: %w", err)
	}
	defer sftpClient.Close()

	if remotePath == "" {
		remotePath = "."
	}

	items, err := sftpClient.ReadDir(remotePath)
	if err != nil {
		return nil, fmt.Errorf("readdir %s: %w", remotePath, err)
	}

	sort.Slice(items, func(i, j int) bool {
		iDir := items[i].IsDir()
		jDir := items[j].IsDir()
		if iDir != jDir {
			return iDir
		}
		return items[i].Name() < items[j].Name()
	})

	var result []SFTPFileInfo
	for _, it := range items {
		result = append(result, SFTPFileInfo{
			Name:    it.Name(),
			IsDir:   it.IsDir(),
			Size:    it.Size(),
			Perm:    fmt.Sprintf("%04o", it.Mode().Perm()),
			ModTime: it.ModTime().Format("2006-01-02 15:04:05"),
		})
	}
	return result, nil
}
