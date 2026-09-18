package update_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Zen1th53/marshal/internal/update"
)

// releaseServer serves one published release: the feed, the archive for this
// machine, and the checksums that archive must match.
func releaseServer(t *testing.T, tag string, binary []byte, corruptChecksum bool) *httptest.Server {
	t.Helper()
	archiveName := fmt.Sprintf("marshal_%s_linux_%s.tar.gz", tag[1:], runtimeArch())

	var packed bytes.Buffer
	zip := gzip.NewWriter(&packed)
	writer := tar.NewWriter(zip)
	if err := writer.WriteHeader(&tar.Header{Name: "marshal", Mode: 0o755, Size: int64(len(binary)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zip.Close(); err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256(packed.Bytes())
	digest := hex.EncodeToString(sum[:])
	if corruptChecksum {
		digest = hex.EncodeToString(make([]byte, sha256.Size))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+update.Repository+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":%q,"html_url":"https://example.invalid/%s","draft":false}`, tag, tag)
	})
	mux.HandleFunc("/"+update.Repository+"/releases/download/"+tag+"/"+archiveName, func(w http.ResponseWriter, r *http.Request) {
		w.Write(packed.Bytes())
	})
	mux.HandleFunc("/"+update.Repository+"/releases/download/"+tag+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", digest, archiveName)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func runtimeArch() string { return runtime.GOARCH }

func checker(t *testing.T, server *httptest.Server) *update.Checker {
	t.Helper()
	c := update.NewChecker()
	c.BaseAPI = server.URL
	c.BaseDownload = server.URL
	return c
}

func TestNewerOnlyReportsAGenuineUpgrade(t *testing.T) {
	for _, tc := range []struct {
		current, latest string
		want            bool
	}{
		{"v0.0.2", "v0.0.3", true},
		{"v0.0.2", "v0.1.0", true},
		{"v0.9.9", "v1.0.0", true},
		{"v0.0.2", "v0.0.2", false},
		{"v0.0.3", "v0.0.2", false},
		{"v1.0.0", "v0.9.9", false},
		// A build with no stamped version is not told to update: MARSHAL
		// cannot tell what it is running.
		{"", "v0.0.3", false},
		{"dev", "v0.0.3", false},
		{"v0.0.2", "nightly", false},
		// Pre-release tags are not ordered rather than ordered wrongly.
		{"v0.0.2", "v0.0.3-rc1", false},
	} {
		if got := update.Newer(tc.current, tc.latest); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestAvailableReportsThePublishedRelease(t *testing.T) {
	server := releaseServer(t, "v9.9.9", []byte("#!/bin/sh\nexit 0\n"), false)
	release, newer, err := checker(t, server).Available(context.Background(), "v0.0.2")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !newer || release.Tag != "v9.9.9" {
		t.Fatalf("unexpected check result: %+v newer=%v", release, newer)
	}

	_, newer, err = checker(t, server).Available(context.Background(), "v9.9.9")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if newer {
		t.Fatal("the running version was reported as out of date against itself")
	}
}

func TestInstallReplacesTheBinaryAfterVerifying(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "marshal")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	server := releaseServer(t, "v9.9.9", []byte("new binary"), false)

	c := checker(t, server)
	c.Executable = func() (string, error) { return target, nil }
	installed, err := c.Install(context.Background(), update.Release{Tag: "v9.9.9"})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if installed != target {
		t.Fatalf("installed to %s, want %s", installed, target)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new binary" {
		t.Fatalf("binary was not replaced: %q", content)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed binary is not executable: %s", info.Mode())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("install left files behind: %v", entries)
	}
}

// A download that does not match its published checksum is not installed, and
// the binary already in place is left exactly as it was.
func TestInstallRefusesAnArchiveThatFailsVerification(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "marshal")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	server := releaseServer(t, "v9.9.9", []byte("new binary"), true)

	c := checker(t, server)
	c.Executable = func() (string, error) { return target, nil }
	if _, err := c.Install(context.Background(), update.Release{Tag: "v9.9.9"}); err == nil {
		t.Fatal("an unverified archive was installed")
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "old binary" {
		t.Fatalf("the installed binary was touched: %q", content)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("a refused install left files behind: %v", entries)
	}
}

func TestDisabledHonoursTheEnvironment(t *testing.T) {
	for value, want := range map[string]bool{"": false, "0": false, "false": false, "1": true, "yes": true} {
		t.Setenv(update.DisableEnv, value)
		if got := update.Disabled(); got != want {
			t.Errorf("%s=%q: Disabled() = %v, want %v", update.DisableEnv, value, got, want)
		}
	}
}
