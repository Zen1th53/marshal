// Package leakscan provides test helpers to verify that sensitive material
// is not accidentally exposed in directory trees, environment variables,
// or captured byte streams.
package leakscan

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// DefaultMinSubstrLen is the minimum substring length extracted from secrets
// for substring detection.
const DefaultMinSubstrLen = 12

// Finding represents an individual detected leak location.
// To prevent accidental disclosure, Finding never stores or prints the secret value.
type Finding struct {
	Target   string
	Location string
}

func (f Finding) String() string {
	return f.Location
}

// Target combines multiple inspection targets into a single scan request.
type Target struct {
	Dir     string
	Env     []string
	Streams map[string]io.Reader
	Bytes   map[string][]byte
}

// Options configure Scanner behavior.
type Options struct {
	MinSubstrLen int
	Fatal        bool
}

// Option modifies Scanner configuration.
type Option func(*Scanner)

// WithMinSubstrLen sets the minimum substring length for secret substring matching.
func WithMinSubstrLen(n int) Option {
	return func(s *Scanner) {
		if n > 0 {
			s.minSubstrLen = n
		}
	}
}

// WithFatal configures whether test failures terminate immediately via Fatalf
// rather than recording failures via Errorf.
func WithFatal(fatal bool) Option {
	return func(s *Scanner) {
		s.fatal = fatal
	}
}

// Scanner scans files, environment slices, and captured byte streams for leaks.
type Scanner struct {
	minSubstrLen int
	fatal        bool
	secrets      []string
	needleSet    map[string]struct{}
	needles      [][]byte
}

// New creates a Scanner for the given secrets with default options.
func New(secrets ...string) *Scanner {
	return NewWithOptions(Options{}, secrets...)
}

// NewWithOptions creates a Scanner with explicit options and secrets.
func NewWithOptions(opts Options, secrets ...string) *Scanner {
	s := &Scanner{
		minSubstrLen: opts.MinSubstrLen,
		fatal:        opts.Fatal,
		needleSet:    make(map[string]struct{}),
	}
	if s.minSubstrLen <= 0 {
		s.minSubstrLen = DefaultMinSubstrLen
	}
	for _, secret := range secrets {
		s.addSecret(secret)
	}
	return s
}

// NewFromSlice creates a Scanner from a slice of secrets and optional Option modifiers.
func NewFromSlice(secrets []string, opts ...Option) *Scanner {
	s := &Scanner{
		minSubstrLen: DefaultMinSubstrLen,
		needleSet:    make(map[string]struct{}),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	for _, secret := range secrets {
		s.addSecret(secret)
	}
	return s
}

func (s *Scanner) addNeedle(needle string) {
	if len(needle) == 0 {
		return
	}
	if _, exists := s.needleSet[needle]; !exists {
		s.needleSet[needle] = struct{}{}
		s.needles = append(s.needles, []byte(needle))
	}
}

func (s *Scanner) addEncodings(val string) {
	if len(val) == 0 {
		return
	}
	raw := []byte(val)

	// Base64 variations: standard and URL-safe, with and without padding
	s.addNeedle(base64.StdEncoding.EncodeToString(raw))
	s.addNeedle(base64.RawStdEncoding.EncodeToString(raw))
	s.addNeedle(base64.URLEncoding.EncodeToString(raw))
	s.addNeedle(base64.RawURLEncoding.EncodeToString(raw))

	// URL variations
	q := url.QueryEscape(val)
	s.addNeedle(q)
	for _, v := range hexVariants(q) {
		s.addNeedle(v)
	}

	p := url.PathEscape(val)
	s.addNeedle(p)
	for _, v := range hexVariants(p) {
		s.addNeedle(v)
	}
}

func (s *Scanner) addSecret(secret string) {
	if len(strings.TrimSpace(secret)) == 0 {
		return
	}
	s.secrets = append(s.secrets, secret)

	// 1. Secret itself and its encodings
	s.addNeedle(secret)
	s.addEncodings(secret)

	// 2. Substrings of length >= minSubstrLen
	runes := []rune(secret)
	if len(runes) >= s.minSubstrLen {
		for i := 0; i <= len(runes)-s.minSubstrLen; i++ {
			sub := string(runes[i : i+s.minSubstrLen])
			s.addNeedle(sub)
			s.addEncodings(sub)
		}
	}

	// Also index byte-level substrings if UTF-8 multi-byte sequence differs
	if len(secret) >= s.minSubstrLen && len(secret) != len(runes) {
		for i := 0; i <= len(secret)-s.minSubstrLen; i++ {
			sub := secret[i : i+s.minSubstrLen]
			s.addNeedle(sub)
			s.addEncodings(sub)
		}
	}
}

// FindInDir searches all regular files and file paths under root.
func (s *Scanner) FindInDir(root string) ([]Finding, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}

	var findings []Finding
	seen := make(map[string]struct{})

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		displayPath := filepath.Clean(path)

		// Check if file or directory name/path contains any secret needle
		pathBytes := []byte(displayPath)
		for _, needle := range s.needles {
			if bytes.Contains(pathBytes, needle) {
				loc := fmt.Sprintf("%s (in path)", s.redactPath(displayPath))
				if _, exists := seen[loc]; !exists {
					seen[loc] = struct{}{}
					findings = append(findings, Finding{
						Target:   "dir",
						Location: loc,
					})
				}
				break
			}
		}

		if d.IsDir() {
			return nil
		}

		// Only scan regular files or symlinks pointing to regular files
		fileInfo, err := d.Info()
		if err != nil {
			return err
		}
		if fileInfo.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return nil
			}
			resInfo, err := os.Stat(resolved)
			if err != nil || resInfo.IsDir() || !resInfo.Mode().IsRegular() {
				return nil
			}
		} else if !fileInfo.Mode().IsRegular() {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		fileFindings := s.scanContent("file", displayPath, data)
		for _, f := range fileFindings {
			if _, exists := seen[f.Location]; !exists {
				seen[f.Location] = struct{}{}
				findings = append(findings, f)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return findings, nil
}

// FindInEnv searches an environment variable slice (KEY=VALUE entries).
func (s *Scanner) FindInEnv(env []string) []Finding {
	var findings []Finding
	seen := make(map[string]struct{})

	for i, entry := range env {
		parts := strings.SplitN(entry, "=", 2)
		key := parts[0]
		val := ""
		if len(parts) > 1 {
			val = parts[1]
		}

		valMatched := false
		for _, needle := range s.needles {
			if bytes.Contains([]byte(val), needle) {
				valMatched = true
				break
			}
		}

		keyMatched := false
		for _, needle := range s.needles {
			if bytes.Contains([]byte(key), needle) {
				keyMatched = true
				break
			}
		}

		if valMatched || keyMatched {
			var loc string
			if keyMatched {
				loc = fmt.Sprintf("env var at index %d", i)
			} else {
				loc = fmt.Sprintf("env var %q", key)
			}
			if _, exists := seen[loc]; !exists {
				seen[loc] = struct{}{}
				findings = append(findings, Finding{
					Target:   "env",
					Location: loc,
				})
			}
		}
	}
	return findings
}

// FindInStream searches a captured byte stream.
func (s *Scanner) FindInStream(name string, r io.Reader) ([]Finding, error) {
	if r == nil {
		return nil, nil
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read stream %q: %w", name, err)
	}
	return s.FindInBytes(name, data), nil
}

// FindInBytes searches a captured byte slice.
func (s *Scanner) FindInBytes(name string, data []byte) []Finding {
	if len(data) == 0 {
		return nil
	}
	label := name
	if label == "" {
		label = "stream"
	}
	return s.scanContent("stream", fmt.Sprintf("stream %q", label), data)
}

// Find evaluates all targets defined in Target.
func (s *Scanner) Find(target Target) ([]Finding, error) {
	var all []Finding
	if target.Dir != "" {
		f, err := s.FindInDir(target.Dir)
		if err != nil {
			return nil, err
		}
		all = append(all, f...)
	}
	if len(target.Env) > 0 {
		all = append(all, s.FindInEnv(target.Env)...)
	}
	if len(target.Streams) > 0 {
		names := make([]string, 0, len(target.Streams))
		for name := range target.Streams {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			f, err := s.FindInStream(name, target.Streams[name])
			if err != nil {
				return nil, err
			}
			all = append(all, f...)
		}
	}
	if len(target.Bytes) > 0 {
		names := make([]string, 0, len(target.Bytes))
		for name := range target.Bytes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			all = append(all, s.FindInBytes(name, target.Bytes[name])...)
		}
	}
	return all, nil
}

// ScanDir scans a directory and fails tb if any leak is detected.
func (s *Scanner) ScanDir(tb testing.TB, root string) {
	tb.Helper()
	findings, err := s.FindInDir(root)
	if err != nil {
		tb.Fatalf("leakscan: scan dir %s: %v", root, err)
		return
	}
	s.report(tb, findings)
}

// ScanEnv scans an environment slice and fails tb if any leak is detected.
func (s *Scanner) ScanEnv(tb testing.TB, env []string) {
	tb.Helper()
	findings := s.FindInEnv(env)
	s.report(tb, findings)
}

// ScanStream scans a captured byte stream and fails tb if any leak is detected.
func (s *Scanner) ScanStream(tb testing.TB, name string, r io.Reader) {
	tb.Helper()
	findings, err := s.FindInStream(name, r)
	if err != nil {
		tb.Fatalf("leakscan: scan stream %q: %v", name, err)
		return
	}
	s.report(tb, findings)
}

// ScanBytes scans a byte slice and fails tb if any leak is detected.
func (s *Scanner) ScanBytes(tb testing.TB, name string, data []byte) {
	tb.Helper()
	findings := s.FindInBytes(name, data)
	s.report(tb, findings)
}

// Scan evaluates all components of Target and fails tb if any leak is detected.
func (s *Scanner) Scan(tb testing.TB, target Target) {
	tb.Helper()
	findings, err := s.Find(target)
	if err != nil {
		tb.Fatalf("leakscan: scan: %v", err)
		return
	}
	s.report(tb, findings)
}

func (s *Scanner) report(tb testing.TB, findings []Finding) {
	tb.Helper()
	if len(findings) == 0 {
		return
	}
	if s.fatal {
		var locs []string
		for _, f := range findings {
			locs = append(locs, f.Location)
		}
		tb.Fatalf("leakscan: leak detected at: %s", strings.Join(locs, ", "))
		return
	}
	for _, f := range findings {
		tb.Errorf("leakscan: leak detected at %s", f.Location)
	}
}

func (s *Scanner) scanContent(target, locationPrefix string, data []byte) []Finding {
	if len(data) == 0 {
		return nil
	}

	isBinary := bytes.IndexByte(data, 0) != -1

	if isBinary {
		var findings []Finding
		seenOffset := make(map[int]struct{})
		for _, needle := range s.needles {
			offset := 0
			for {
				idx := bytes.Index(data[offset:], needle)
				if idx == -1 {
					break
				}
				pos := offset + idx
				if _, exists := seenOffset[pos]; !exists {
					seenOffset[pos] = struct{}{}
					findings = append(findings, Finding{
						Target:   target,
						Location: fmt.Sprintf("%s (offset %d)", locationPrefix, pos),
					})
				}
				offset = pos + 1
				if offset >= len(data) {
					break
				}
			}
		}
		return findings
	}

	matchingLines := make(map[int]struct{})
	for _, needle := range s.needles {
		offset := 0
		for {
			idx := bytes.Index(data[offset:], needle)
			if idx == -1 {
				break
			}
			pos := offset + idx
			lineNum := 1 + bytes.Count(data[:pos], []byte("\n"))
			matchingLines[lineNum] = struct{}{}
			offset = pos + 1
			if offset >= len(data) {
				break
			}
		}
	}

	var lines []int
	for l := range matchingLines {
		lines = append(lines, l)
	}
	sort.Ints(lines)

	var findings []Finding
	for _, l := range lines {
		findings = append(findings, Finding{
			Target:   target,
			Location: fmt.Sprintf("%s:%d", locationPrefix, l),
		})
	}
	return findings
}

func (s *Scanner) redactPath(path string) string {
	res := path
	// Sort needles by length descending to replace larger patterns first
	needles := make([][]byte, len(s.needles))
	copy(needles, s.needles)
	sort.Slice(needles, func(i, j int) bool {
		return len(needles[i]) > len(needles[j])
	})
	for _, needle := range needles {
		if len(needle) > 0 {
			res = strings.ReplaceAll(res, string(needle), "[REDACTED]")
		}
	}
	return res
}

func hexVariants(escaped string) []string {
	var hasHex bool
	for i := 0; i+2 < len(escaped); i++ {
		if escaped[i] == '%' && isHex(escaped[i+1]) && isHex(escaped[i+2]) {
			hasHex = true
			break
		}
	}
	if !hasHex {
		return nil
	}
	var lower strings.Builder
	for i := 0; i < len(escaped); i++ {
		if escaped[i] == '%' && i+2 < len(escaped) && isHex(escaped[i+1]) && isHex(escaped[i+2]) {
			lower.WriteByte('%')
			lower.WriteByte(toLower(escaped[i+1]))
			lower.WriteByte(toLower(escaped[i+2]))
			i += 2
		} else {
			lower.WriteByte(escaped[i])
		}
	}
	return []string{lower.String()}
}

func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func toLower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// ScanDir is a convenience helper scanning a directory with default options.
func ScanDir(tb testing.TB, secrets []string, root string) {
	tb.Helper()
	New(secrets...).ScanDir(tb, root)
}

// ScanEnv is a convenience helper scanning an environment slice with default options.
func ScanEnv(tb testing.TB, secrets []string, env []string) {
	tb.Helper()
	New(secrets...).ScanEnv(tb, env)
}

// ScanStream is a convenience helper scanning a captured stream with default options.
func ScanStream(tb testing.TB, secrets []string, name string, r io.Reader) {
	tb.Helper()
	New(secrets...).ScanStream(tb, name, r)
}

// ScanBytes is a convenience helper scanning a byte slice with default options.
func ScanBytes(tb testing.TB, secrets []string, name string, data []byte) {
	tb.Helper()
	New(secrets...).ScanBytes(tb, name, data)
}

// Scan is a convenience helper scanning Target with default options.
func Scan(tb testing.TB, secrets []string, target Target) {
	tb.Helper()
	New(secrets...).Scan(tb, target)
}
