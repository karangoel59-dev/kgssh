package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// newTestSFTPServer starts a real in-process SSH server with an SFTP subsystem
// and returns a connected client. Transfers are exercised end to end rather
// than against a mock, so path handling and the SFTP protocol are both covered.
func newTestSFTPServer(t *testing.T) *ssh.Client {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}

	serverConfig := &ssh.ServerConfig{NoClientAuth: true}
	serverConfig.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveSFTP(conn, serverConfig)
		}
	}()

	clientConfig := &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	client, err := ssh.Dial("tcp", listener.Addr().String(), clientConfig)
	if err != nil {
		t.Fatalf("dial test server: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	return client
}

func serveSFTP(conn net.Conn, cfg *ssh.ServerConfig) {
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			_ = newChan.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		ch, chReqs, err := newChan.Accept()
		if err != nil {
			continue
		}
		go func(ch ssh.Channel, reqs <-chan *ssh.Request) {
			for req := range reqs {
				ok := req.Type == "subsystem" && len(req.Payload) >= 4 && string(req.Payload[4:]) == "sftp"
				if req.WantReply {
					_ = req.Reply(ok, nil)
				}
				if ok {
					server, err := sftp.NewServer(ch)
					if err == nil {
						_ = server.Serve()
						_ = server.Close()
					}
					_ = ch.Close()
				}
			}
		}(ch, chReqs)
	}
}

func TestUploadAndDownloadRoundTrip(t *testing.T) {
	client := newTestSFTPServer(t)
	srcDir, dstDir := t.TempDir(), t.TempDir()

	// Binary content with NUL bytes: this is what read_file/write_file cannot carry.
	payload := make([]byte, 300*1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(srcDir, "payload.bin")
	if err := os.WriteFile(local, payload, 0o640); err != nil {
		t.Fatal(err)
	}

	remote := filepath.Join(dstDir, "uploaded.bin")
	stats, err := UploadSFTP(context.Background(), client, local, remote, false, false)
	if err != nil {
		t.Fatalf("UploadSFTP: %v", err)
	}
	if stats.Files != 1 {
		t.Fatalf("Files = %d, want 1", stats.Files)
	}
	if stats.Bytes != int64(len(payload)) {
		t.Fatalf("Bytes = %d, want %d", stats.Bytes, len(payload))
	}

	got, err := os.ReadFile(remote)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("uploaded bytes differ from the source")
	}
	if fi, err := os.Stat(remote); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %04o, want 0640 (permissions should be preserved)", fi.Mode().Perm())
	}

	// Round-trip back down and compare.
	back := filepath.Join(srcDir, "roundtrip.bin")
	if _, err := DownloadSFTP(context.Background(), client, remote, back, false, false); err != nil {
		t.Fatalf("DownloadSFTP: %v", err)
	}
	returned, err := os.ReadFile(back)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(returned, payload) {
		t.Fatal("downloaded bytes differ from the source")
	}
}

func TestUploadRefusesDirectoryWithoutRecursive(t *testing.T) {
	client := newTestSFTPServer(t)
	srcDir, dstDir := t.TempDir(), t.TempDir()

	if err := os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := UploadSFTP(context.Background(), client, srcDir, filepath.Join(dstDir, "out"), false, false)
	if !errors.Is(err, ErrIsDirectory) {
		t.Fatalf("err = %v, want ErrIsDirectory", err)
	}
}

func TestUploadRecursivePreservesTree(t *testing.T) {
	client := newTestSFTPServer(t)
	srcDir, dstDir := t.TempDir(), t.TempDir()

	if err := os.MkdirAll(filepath.Join(srcDir, "nested", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"top.txt":                                "top",
		filepath.Join("nested", "mid.txt"):       "mid",
		filepath.Join("nested", "deep", "x.txt"): "deep",
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(srcDir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	remote := filepath.Join(dstDir, "tree")
	stats, err := UploadSFTP(context.Background(), client, srcDir, remote, true, false)
	if err != nil {
		t.Fatalf("UploadSFTP recursive: %v", err)
	}
	if stats.Files != len(files) {
		t.Fatalf("Files = %d, want %d", stats.Files, len(files))
	}

	for rel, content := range files {
		got, err := os.ReadFile(filepath.Join(remote, rel))
		if err != nil {
			t.Fatalf("missing %s: %v", rel, err)
		}
		if string(got) != content {
			t.Fatalf("%s = %q, want %q", rel, got, content)
		}
	}
}

func TestUploadIntoExistingRemoteDirectoryUsesBaseName(t *testing.T) {
	client := newTestSFTPServer(t)
	srcDir, dstDir := t.TempDir(), t.TempDir()

	local := filepath.Join(srcDir, "report.txt")
	if err := os.WriteFile(local, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// scp semantics: destination is an existing directory.
	if _, err := UploadSFTP(context.Background(), client, local, dstDir, false, false); err != nil {
		t.Fatalf("UploadSFTP: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dstDir, "report.txt")); err != nil {
		t.Fatalf("expected the file inside the destination directory: %v", err)
	}
}

func TestUploadRefusesToOverwriteByDefault(t *testing.T) {
	client := newTestSFTPServer(t)
	srcDir, dstDir := t.TempDir(), t.TempDir()

	local := filepath.Join(srcDir, "f.txt")
	if err := os.WriteFile(local, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(dstDir, "f.txt")
	if err := os.WriteFile(remote, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := UploadSFTP(context.Background(), client, local, remote, false, false)
	if !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("err = %v, want ErrDestinationExists", err)
	}
	if got, _ := os.ReadFile(remote); string(got) != "original" {
		t.Fatalf("destination was modified: %q", got)
	}

	if _, err := UploadSFTP(context.Background(), client, local, remote, false, true); err != nil {
		t.Fatalf("UploadSFTP with overwrite: %v", err)
	}
	if got, _ := os.ReadFile(remote); string(got) != "new" {
		t.Fatalf("overwrite did not apply: %q", got)
	}
}

func TestDownloadRefusesToOverwriteByDefault(t *testing.T) {
	client := newTestSFTPServer(t)
	srcDir, dstDir := t.TempDir(), t.TempDir()

	remote := filepath.Join(srcDir, "r.txt")
	if err := os.WriteFile(remote, []byte("remote"), 0o644); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(dstDir, "r.txt")
	if err := os.WriteFile(local, []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := DownloadSFTP(context.Background(), client, remote, local, false, false)
	if !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("err = %v, want ErrDestinationExists", err)
	}
	if got, _ := os.ReadFile(local); string(got) != "local" {
		t.Fatalf("local file was modified: %q", got)
	}
}

func TestDownloadCreatesMissingParentDirectories(t *testing.T) {
	client := newTestSFTPServer(t)
	srcDir, dstDir := t.TempDir(), t.TempDir()

	remote := filepath.Join(srcDir, "r.txt")
	if err := os.WriteFile(remote, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	local := filepath.Join(dstDir, "does", "not", "exist", "r.txt")
	if _, err := DownloadSFTP(context.Background(), client, remote, local, false, false); err != nil {
		t.Fatalf("DownloadSFTP: %v", err)
	}
	if _, err := os.Stat(local); err != nil {
		t.Fatalf("expected parents to be created: %v", err)
	}
}

func TestTransferHonoursContextCancellation(t *testing.T) {
	client := newTestSFTPServer(t)
	srcDir, dstDir := t.TempDir(), t.TempDir()

	local := filepath.Join(srcDir, "big.bin")
	if err := os.WriteFile(local, make([]byte, 2*transferBufferSize), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: the copy must not proceed

	_, err := UploadSFTP(ctx, client, local, filepath.Join(dstDir, "big.bin"), false, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestCopyWithContextCopiesEverything(t *testing.T) {
	src := bytes.NewReader(bytes.Repeat([]byte("ab"), transferBufferSize))
	var dst bytes.Buffer

	n, err := copyWithContext(context.Background(), &dst, src)
	if err != nil {
		t.Fatalf("copyWithContext: %v", err)
	}
	if want := int64(2 * transferBufferSize); n != want {
		t.Fatalf("copied %d bytes, want %d", n, want)
	}
	if dst.Len() != 2*transferBufferSize {
		t.Fatalf("destination holds %d bytes", dst.Len())
	}
}

func TestCopyWithContextPropagatesWriteError(t *testing.T) {
	_, err := copyWithContext(context.Background(), failingWriter{}, bytes.NewReader([]byte("data")))
	if err == nil {
		t.Fatal("expected the write error to propagate")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{3 * 1024 * 1024 * 1024, "3.0 GiB"},
	}
	for _, tc := range cases {
		if got := formatBytes(tc.in); got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatTransferSummaryReportsCounts(t *testing.T) {
	stats := TransferStats{Files: 3, Bytes: 2048, Duration: 2 * time.Second}
	out := formatTransferSummary("Uploaded", "/tmp/x", "host:/tmp/y", stats)

	for _, want := range []string{"Uploaded", "/tmp/x", "host:/tmp/y", "**Files:** 3", "2.0 KiB", "1.0 KiB/s"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
}

func TestThroughputZeroDuration(t *testing.T) {
	if got := (TransferStats{Bytes: 100}).Throughput(); got != 0 {
		t.Fatalf("Throughput() = %v, want 0 when duration is unset", got)
	}
}

func TestUploadReportsMissingLocalFile(t *testing.T) {
	client := newTestSFTPServer(t)
	_, err := UploadSFTP(context.Background(), client, filepath.Join(t.TempDir(), "nope"), "/tmp/x", false, false)
	if err == nil {
		t.Fatal("expected an error for a missing local file")
	}
	if got := fmt.Sprint(err); !bytes.Contains([]byte(got), []byte("stat local")) {
		t.Fatalf("err = %q, want it to name the failing step", got)
	}
}
