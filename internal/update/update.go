// Package update checks whether a newer MARSHAL release exists and installs
// it on request.
//
// Two rules shape everything here. A check is a read and never changes the
// machine, so it can run on its own; an install replaces the binary the user
// is running and therefore happens only when they ask for it. And an archive
// is installed only after its published SHA-256 matches, which is the same
// guarantee install.sh gives: a download that fails verification is not
// installed, and the binary in place is left alone.
package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Repository is the release source. It is a constant rather than configurable:
// an installer that can be pointed at another host is an installer that can be
// pointed at an attacker's host.
const Repository = "Zen1th53/marshal"

// DisableEnv turns the check off for users who do not want MARSHAL contacting
// GitHub at all.
const DisableEnv = "MARSHAL_NO_UPDATE_CHECK"

// Release is a published release, reduced to what an update needs.
type Release struct {
	// Tag is the release tag, for example "v0.0.3".
	Tag string `json:"tag"`
	// URL is the release page, so a user can read what changed before
	// installing anything.
	URL string `json:"url"`
}

// Checker looks up the latest release.
type Checker struct {
	// HTTP looks the release feed up. Its timeout bounds the whole request,
	// which is right for a small JSON document and wrong for an archive.
	HTTP *http.Client
	// Download fetches release assets. It has no overall timeout: a slow link
	// that is still delivering is not a failure. A link that stops delivering
	// is, and StallTimeout decides how long "stopped" is.
	Download *http.Client
	// StallTimeout is how long a download may go without receiving a byte
	// before it is abandoned.
	StallTimeout time.Duration
	// BaseAPI and BaseDownload exist so tests can serve their own release
	// rather than reaching GitHub.
	BaseAPI      string
	BaseDownload string
	// Executable reports the binary an install replaces. Tests point it at a
	// file of their own; nothing else changes it.
	Executable func() (string, error)
}

// NewChecker returns a checker pointed at GitHub with a bounded timeout. A
// check that cannot finish quickly is not worth delaying the workspace for.
func NewChecker() *Checker {
	return &Checker{
		HTTP: &http.Client{Timeout: 10 * time.Second},
		Download: &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		}},
		StallTimeout: 30 * time.Second,
		BaseAPI:      "https://api.github.com",
		BaseDownload: "https://github.com",
		Executable:   os.Executable,
	}
}

// Disabled reports whether the user has turned update checks off.
func Disabled() bool {
	value := strings.TrimSpace(os.Getenv(DisableEnv))
	return value != "" && value != "0" && !strings.EqualFold(value, "false")
}

// Latest returns the most recent published release.
func (c *Checker) Latest(ctx context.Context) (Release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", strings.TrimSuffix(c.BaseAPI, "/"), Repository)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := c.HTTP.Do(request)
	if err != nil {
		return Release{}, fmt.Errorf("could not reach the release feed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("the release feed answered %s", response.Status)
	}

	var payload struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Draft   bool   `json:"draft"`
	}
	// The feed is read with a bound: a response that keeps coming is not a
	// release description.
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return Release{}, fmt.Errorf("could not read the release feed: %w", err)
	}
	tag := strings.TrimSpace(payload.TagName)
	if tag == "" || payload.Draft {
		return Release{}, fmt.Errorf("the release feed named no published release")
	}
	return Release{Tag: tag, URL: payload.HTMLURL}, nil
}

// Available reports the latest release and whether it is newer than the
// running build.
func (c *Checker) Available(ctx context.Context, current string) (Release, bool, error) {
	latest, err := c.Latest(ctx)
	if err != nil {
		return Release{}, false, err
	}
	return latest, Newer(current, latest.Tag), nil
}

// Newer reports whether tag is a later version than current.
//
// A build whose version was never stamped, or one carrying a version the
// comparison cannot read, is not treated as out of date: telling someone to
// update away from a build MARSHAL cannot identify would be a guess.
func Newer(current, tag string) bool {
	running, ok := parseVersion(current)
	if !ok {
		return false
	}
	candidate, ok := parseVersion(tag)
	if !ok {
		return false
	}
	for i := range candidate {
		if candidate[i] != running[i] {
			return candidate[i] > running[i]
		}
	}
	return false
}

// parseVersion reads a plain vMAJOR.MINOR.PATCH tag. Pre-release and build
// suffixes are rejected rather than guessed at, because ordering them wrongly
// would offer a downgrade as an update.
func parseVersion(value string) ([3]int, bool) {
	var parsed [3]int
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "v")
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return parsed, false
	}
	for i, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return parsed, false
		}
		parsed[i] = number
	}
	return parsed, true
}

// Install downloads the release for this machine, verifies it against the
// release's published checksums, and replaces the running binary.
//
// The replacement is a rename within the install directory, so the binary is
// never half-written: either the new one is in place or the old one still is.
// Processes already running keep the code they started with, which is why the
// caller is told to restart rather than being told the update is live.
func (c *Checker) Install(ctx context.Context, release Release) (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("release binaries are built for Linux; this is %s", runtime.GOOS)
	}
	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		return "", fmt.Errorf("no release binary for %s; MARSHAL publishes linux amd64 and arm64", arch)
	}

	locate := c.Executable
	if locate == nil {
		locate = os.Executable
	}
	executable, err := locate()
	if err != nil {
		return "", fmt.Errorf("could not locate the running binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}

	version := strings.TrimPrefix(release.Tag, "v")
	archive := fmt.Sprintf("marshal_%s_linux_%s.tar.gz", version, arch)
	base := fmt.Sprintf("%s/%s/releases/download/%s", strings.TrimSuffix(c.BaseDownload, "/"), Repository, release.Tag)

	payload, err := c.download(ctx, base+"/"+archive, 256<<20)
	if err != nil {
		return "", fmt.Errorf("could not download %s: %w", archive, err)
	}
	checksums, err := c.download(ctx, base+"/checksums.txt", 1<<20)
	if err != nil {
		return "", fmt.Errorf("release %s publishes no checksums.txt; refusing to install unverified: %w", release.Tag, err)
	}
	expected, found := checksumFor(string(checksums), archive)
	if !found {
		return "", fmt.Errorf("checksums.txt does not list %s; refusing to install unverified", archive)
	}
	sum := sha256.Sum256(payload)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), expected) {
		return "", fmt.Errorf("checksum verification failed for %s; nothing was installed", archive)
	}

	binary, err := extractBinary(payload)
	if err != nil {
		return "", err
	}

	// The staged file is written next to the binary it replaces so the rename
	// stays within one filesystem and cannot degrade into a copy.
	dir := filepath.Dir(executable)
	staged, err := os.CreateTemp(dir, ".marshal-update-*")
	if err != nil {
		return "", fmt.Errorf("could not write to %s: %w", dir, err)
	}
	stagedName := staged.Name()
	defer os.Remove(stagedName)
	if _, err := staged.Write(binary); err != nil {
		staged.Close()
		return "", fmt.Errorf("could not write the new binary: %w", err)
	}
	if err := staged.Close(); err != nil {
		return "", fmt.Errorf("could not write the new binary: %w", err)
	}
	if err := os.Chmod(stagedName, 0o755); err != nil {
		return "", fmt.Errorf("could not make the new binary executable: %w", err)
	}
	if err := os.Rename(stagedName, executable); err != nil {
		return "", fmt.Errorf("could not replace %s: %w", executable, err)
	}
	return executable, nil
}

func (c *Checker) download(ctx context.Context, url string, limit int64) ([]byte, error) {
	client := c.Download
	if client == nil {
		client = http.DefaultClient
	}
	stall := c.StallTimeout
	if stall <= 0 {
		stall = 30 * time.Second
	}

	// The request is cancelled when no byte has arrived for the stall window.
	// Each read that makes progress pushes the deadline back, so the only
	// thing bounded is silence, never the size of the download.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	errStalled := fmt.Errorf("no data received for %s; the connection stalled", stall)
	watchdog := time.AfterFunc(stall, func() { cancel(errStalled) })
	defer watchdog.Stop()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, stalledOr(ctx, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("answered %s", response.Status)
	}
	watchdog.Reset(stall)
	data, err := io.ReadAll(&progressReader{
		reader:   io.LimitReader(response.Body, limit),
		progress: func() { watchdog.Reset(stall) },
	})
	if err != nil {
		return nil, stalledOr(ctx, err)
	}
	return data, nil
}

// stalledOr reports the stall rather than the context error it caused, so the
// user reads "the connection stalled" instead of "context canceled".
func stalledOr(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		return cause
	}
	return err
}

// progressReader calls progress after every read that returned data.
type progressReader struct {
	reader   io.Reader
	progress func()
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.progress()
	}
	return n, err
}

// checksumFor finds one file's line in a sha256sum-format list.
func checksumFor(checksums, name string) (string, bool) {
	for _, line := range strings.Split(checksums, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") == name {
			return fields[0], true
		}
	}
	return "", false
}

// extractBinary reads the marshal binary out of a release archive.
func extractBinary(payload []byte) ([]byte, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("the download is not a gzip archive: %w", err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("could not read the archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != "marshal" {
			continue
		}
		// Bounded: an archive claiming a binary larger than any MARSHAL build
		// is not one to unpack into memory.
		binary, err := io.ReadAll(io.LimitReader(reader, 512<<20))
		if err != nil {
			return nil, fmt.Errorf("could not read the archive: %w", err)
		}
		return binary, nil
	}
	return nil, fmt.Errorf("the archive contains no marshal binary")
}
