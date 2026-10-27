package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// TransferStats summarises a completed upload or download.
type TransferStats struct {
	Files    int
	Bytes    int64
	Duration time.Duration
}

// Throughput returns the average transfer rate in bytes per second.
func (t TransferStats) Throughput() float64 {
	if t.Duration <= 0 {
		return 0
	}
	return float64(t.Bytes) / t.Duration.Seconds()
}

// ErrIsDirectory reports a directory handed to a non-recursive transfer.
var ErrIsDirectory = errors.New("path is a directory: pass recursive to transfer it")

// ErrDestinationExists reports a destination that is already present and would
// be overwritten. Transfers refuse by default so an accidental call cannot
// clobber a file; the caller opts in explicitly.
var ErrDestinationExists = errors.New("destination exists: pass overwrite to replace it")

// transferBufferSize is the copy chunk. 256 KiB keeps the SFTP pipeline busy on
// a high-latency link without holding much memory.
const transferBufferSize = 256 * 1024

// UploadSFTP copies a local file or directory to a remote server, streaming the
// bytes rather than passing them through the caller. It mirrors scp's
// destination handling: when remotePath is an existing directory, the source's
// base name is created inside it.
func UploadSFTP(ctx context.Context, client *ssh.Client, localPath, remotePath string, recursive, overwrite bool) (TransferStats, error) {
	start := time.Now()
	stats := TransferStats{}

	local := expandPath(localPath)
	info, err := os.Stat(local)
	if err != nil {
		return stats, fmt.Errorf("stat local %s: %w", localPath, err)
	}

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return stats, fmt.Errorf("init sftp: %w", err)
	}
	defer sftpClient.Close()

	remote := remotePath
	// scp semantics: an existing remote directory means "put it in here".
	if fi, err := sftpClient.Stat(remote); err == nil && fi.IsDir() {
		remote = path.Join(remote, filepath.Base(local))
	}

	if info.IsDir() {
		if !recursive {
			return stats, fmt.Errorf("%s: %w", localPath, ErrIsDirectory)
		}
		err = uploadDir(ctx, sftpClient, local, remote, overwrite, &stats)
	} else {
		err = uploadFile(ctx, sftpClient, local, remote, info, overwrite, &stats)
	}

	stats.Duration = time.Since(start)
	return stats, err
}

func uploadDir(ctx context.Context, sc *sftp.Client, localDir, remoteDir string, overwrite bool, stats *TransferStats) error {
	return filepath.Walk(localDir, func(localPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		rel, err := filepath.Rel(localDir, localPath)
		if err != nil {
			return err
		}
		// Remote paths are always slash-separated, whatever the local OS uses.
		target := remoteDir
		if rel != "." {
			target = path.Join(remoteDir, filepath.ToSlash(rel))
		}

		if info.IsDir() {
			if err := sc.MkdirAll(target); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			// Sockets, devices and symlinks are skipped rather than guessed at.
			return nil
		}
		return uploadFile(ctx, sc, localPath, target, info, overwrite, stats)
	})
}

func uploadFile(ctx context.Context, sc *sftp.Client, localPath, remotePath string, info os.FileInfo, overwrite bool, stats *TransferStats) error {
	if !overwrite {
		if _, err := sc.Stat(remotePath); err == nil {
			return fmt.Errorf("%s: %w", remotePath, ErrDestinationExists)
		}
	}

	if parent := path.Dir(remotePath); parent != "" && parent != "." && parent != "/" {
		if err := sc.MkdirAll(parent); err != nil {
			return fmt.Errorf("mkdir %s: %w", parent, err)
		}
	}

	src, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open local %s: %w", localPath, err)
	}
	defer src.Close()

	dst, err := sc.Create(remotePath)
	if err != nil {
		return fmt.Errorf("create %s: %w", remotePath, err)
	}

	written, copyErr := copyWithContext(ctx, dst, src)
	stats.Bytes += written
	if copyErr != nil {
		dst.Close()
		return fmt.Errorf("write %s: %w", remotePath, copyErr)
	}
	// Close flushes buffered writes; its error means the data may not have landed.
	if err := dst.Close(); err != nil {
		return fmt.Errorf("close %s: %w", remotePath, err)
	}

	if err := sc.Chmod(remotePath, info.Mode().Perm()); err != nil {
		return fmt.Errorf("chmod %s: %w", remotePath, err)
	}

	stats.Files++
	return nil
}

// DownloadSFTP copies a remote file or directory to the local filesystem. As
// with upload, an existing local directory destination receives the source's
// base name inside it.
func DownloadSFTP(ctx context.Context, client *ssh.Client, remotePath, localPath string, recursive, overwrite bool) (TransferStats, error) {
	start := time.Now()
	stats := TransferStats{}

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return stats, fmt.Errorf("init sftp: %w", err)
	}
	defer sftpClient.Close()

	info, err := sftpClient.Stat(remotePath)
	if err != nil {
		return stats, fmt.Errorf("stat remote %s: %w", remotePath, err)
	}

	local := expandPath(localPath)
	if fi, err := os.Stat(local); err == nil && fi.IsDir() {
		local = filepath.Join(local, path.Base(remotePath))
	}

	if info.IsDir() {
		if !recursive {
			return stats, fmt.Errorf("%s: %w", remotePath, ErrIsDirectory)
		}
		err = downloadDir(ctx, sftpClient, remotePath, local, overwrite, &stats)
	} else {
		err = downloadFile(ctx, sftpClient, remotePath, local, info, overwrite, &stats)
	}

	stats.Duration = time.Since(start)
	return stats, err
}

func downloadDir(ctx context.Context, sc *sftp.Client, remoteDir, localDir string, overwrite bool, stats *TransferStats) error {
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", localDir, err)
	}

	entries, err := sc.ReadDir(remoteDir)
	if err != nil {
		return fmt.Errorf("readdir %s: %w", remoteDir, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		remoteChild := path.Join(remoteDir, e.Name())
		localChild := filepath.Join(localDir, e.Name())

		switch {
		case e.IsDir():
			if err := downloadDir(ctx, sc, remoteChild, localChild, overwrite, stats); err != nil {
				return err
			}
		case e.Mode().IsRegular():
			if err := downloadFile(ctx, sc, remoteChild, localChild, e, overwrite, stats); err != nil {
				return err
			}
		}
	}
	return nil
}

func downloadFile(ctx context.Context, sc *sftp.Client, remotePath, localPath string, info os.FileInfo, overwrite bool, stats *TransferStats) error {
	if !overwrite {
		if _, err := os.Stat(localPath); err == nil {
			return fmt.Errorf("%s: %w", localPath, ErrDestinationExists)
		}
	}

	if parent := filepath.Dir(localPath); parent != "" {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", parent, err)
		}
	}

	src, err := sc.Open(remotePath)
	if err != nil {
		return fmt.Errorf("open %s: %w", remotePath, err)
	}
	defer src.Close()

	// The remote mode is applied at creation so a private key never exists
	// locally as world-readable, even briefly.
	dst, err := os.OpenFile(localPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create local %s: %w", localPath, err)
	}

	written, copyErr := copyWithContext(ctx, dst, src)
	stats.Bytes += written
	if copyErr != nil {
		dst.Close()
		return fmt.Errorf("read %s: %w", remotePath, copyErr)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("close %s: %w", localPath, err)
	}

	stats.Files++
	return nil
}

// copyWithContext streams src into dst in chunks, checking for cancellation
// between them so a stalled or oversized transfer can be abandoned.
func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, transferBufferSize)
	var total int64

	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			written, writeErr := dst.Write(buf[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

// formatBytes renders a byte count for human-facing transfer summaries.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	for _, u := range units {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, u)
		}
	}
	return fmt.Sprintf("%.1f PiB", value)
}

// formatTransferSummary renders the result line shared by both MCP transfer tools.
func formatTransferSummary(verb, from, to string, stats TransferStats) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "✅ %s `%s` → `%s`\n", verb, from, to)
	fmt.Fprintf(&sb, "- **Files:** %d\n", stats.Files)
	fmt.Fprintf(&sb, "- **Transferred:** %s (%d bytes)\n", formatBytes(stats.Bytes), stats.Bytes)
	fmt.Fprintf(&sb, "- **Duration:** %.2fs\n", stats.Duration.Seconds())
	if rate := stats.Throughput(); rate > 0 {
		fmt.Fprintf(&sb, "- **Throughput:** %s/s\n", formatBytes(int64(rate)))
	}
	return sb.String()
}
