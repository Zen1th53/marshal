package leakscan_test

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/testutil/leakscan"
)

// assembleTestItem creates a dynamic string at runtime so no credential-shaped
// literals exist in source code.
func assembleTestItem(base string, repeat int) string {
	var b strings.Builder
	for i := 0; i < repeat; i++ {
		b.WriteString(base)
		b.WriteString(strconv.Itoa(i + 1))
	}
	return b.String()
}

// assembleEscapableItem creates a dynamic string containing URL-escapable characters.
func assembleEscapableItem(seed int) string {
	parts := []string{
		fmt.Sprintf("segment%d", seed),
		fmt.Sprintf("data/%d", seed*2),
		fmt.Sprintf("entry+%d", seed*3),
		fmt.Sprintf("query=%d", seed*4),
	}
	return strings.Join(parts, ":")
}

type recordTB struct {
	testing.TB
	failed   bool
	messages []string
}

func newRecordTB(t testing.TB) *recordTB {
	return &recordTB{TB: t}
}

func (r *recordTB) Helper() {}

func (r *recordTB) Error(args ...any) {
	r.failed = true
	r.messages = append(r.messages, fmt.Sprint(args...))
}

func (r *recordTB) Errorf(format string, args ...any) {
	r.failed = true
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}

func (r *recordTB) Fatal(args ...any) {
	r.failed = true
	r.messages = append(r.messages, fmt.Sprint(args...))
}

func (r *recordTB) Fatalf(format string, args ...any) {
	r.failed = true
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}

func (r *recordTB) Fail() {
	r.failed = true
}

func (r *recordTB) FailNow() {
	r.failed = true
}

func (r *recordTB) Failed() bool {
	return r.failed
}

func assertNeverContains(t *testing.T, messages []string, forbidden []string) {
	t.Helper()
	for _, msg := range messages {
		for _, f := range forbidden {
			if len(f) > 0 && strings.Contains(msg, f) {
				t.Fatalf("violation: forbidden secret or derived pattern appeared in output message: %s", msg)
			}
		}
	}
}

func TestDir_ExactSecret(t *testing.T) {
	secret := assembleTestItem("alpha", 4)
	dir := t.TempDir()
	filePath := filepath.Join(dir, "record.txt")

	content := "header line\ncontent with " + secret + " embedded\nfooter line\n"
	if err := os.WriteFile(filePath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	rec := newRecordTB(t)
	scanner := leakscan.New(secret)
	scanner.ScanDir(rec, dir)

	if !rec.failed {
		t.Fatal("expected scanner to fail the test, but it passed")
	}

	foundLocation := false
	for _, msg := range rec.messages {
		if strings.Contains(msg, "record.txt:2") {
			foundLocation = true
			break
		}
	}
	if !foundLocation {
		t.Fatalf("expected failure location record.txt:2 in messages: %v", rec.messages)
	}

	assertNeverContains(t, rec.messages, []string{secret})
}

func TestDir_Substring12(t *testing.T) {
	secret := assembleTestItem("bravo", 5) // length ~30
	sub12 := secret[4:16]                  // exact 12 chars
	dir := t.TempDir()
	filePath := filepath.Join(dir, "output.log")

	content := "log line prefix " + sub12 + " log line suffix\n"
	if err := os.WriteFile(filePath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	rec := newRecordTB(t)
	scanner := leakscan.New(secret)
	scanner.ScanDir(rec, dir)

	if !rec.failed {
		t.Fatal("expected scanner to detect 12-char substring, but passed")
	}

	foundLocation := false
	for _, msg := range rec.messages {
		if strings.Contains(msg, "output.log:1") {
			foundLocation = true
			break
		}
	}
	if !foundLocation {
		t.Fatalf("expected failure location output.log:1 in messages: %v", rec.messages)
	}

	assertNeverContains(t, rec.messages, []string{secret, sub12})
}

func TestDir_SubstringUnder12_Ignored(t *testing.T) {
	secret := assembleTestItem("charlie", 4)
	sub11 := secret[0:11] // 11 characters
	dir := t.TempDir()
	filePath := filepath.Join(dir, "clean.txt")

	content := "normal content with " + sub11 + " here\n"
	if err := os.WriteFile(filePath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	rec := newRecordTB(t)
	scanner := leakscan.New(secret)
	scanner.ScanDir(rec, dir)

	if rec.failed {
		t.Fatalf("expected scanner NOT to fail on substring under 12 characters, but got: %v", rec.messages)
	}
}

func TestDir_Base64Forms(t *testing.T) {
	secret := assembleTestItem("delta", 4)
	stdB64 := base64.StdEncoding.EncodeToString([]byte(secret))
	urlB64Raw := base64.RawURLEncoding.EncodeToString([]byte(secret))

	dir := t.TempDir()
	file1 := filepath.Join(dir, "b64std.txt")
	file2 := filepath.Join(dir, "b64url.txt")

	if err := os.WriteFile(file1, []byte("data: "+stdB64+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte("data: "+urlB64Raw+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	rec := newRecordTB(t)
	scanner := leakscan.New(secret)
	scanner.ScanDir(rec, dir)

	if !rec.failed {
		t.Fatal("expected base64 forms to trigger test failure")
	}

	assertNeverContains(t, rec.messages, []string{secret, stdB64, urlB64Raw})
}

func TestDir_URLEncoded(t *testing.T) {
	secret := assembleEscapableItem(3)
	escaped := url.QueryEscape(secret)

	// Also create lowercase hex version: e.g. %2f instead of %2F
	var lower strings.Builder
	for i := 0; i < len(escaped); i++ {
		if escaped[i] == '%' && i+2 < len(escaped) {
			lower.WriteByte('%')
			lower.WriteByte(strings.ToLower(string(escaped[i+1]))[0])
			lower.WriteByte(strings.ToLower(string(escaped[i+2]))[0])
			i += 2
		} else {
			lower.WriteByte(escaped[i])
		}
	}
	escapedLower := lower.String()

	dir := t.TempDir()
	f1 := filepath.Join(dir, "query_upper.txt")
	f2 := filepath.Join(dir, "query_lower.txt")

	if err := os.WriteFile(f1, []byte("url: "+escaped+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("url: "+escapedLower+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	rec := newRecordTB(t)
	scanner := leakscan.New(secret)
	scanner.ScanDir(rec, dir)

	if !rec.failed {
		t.Fatal("expected url-encoded forms to trigger test failure")
	}

	assertNeverContains(t, rec.messages, []string{secret, escaped, escapedLower})
}

func TestDir_FilePathLeak(t *testing.T) {
	secret := assembleTestItem("echo", 4)
	dir := t.TempDir()
	leakyFileName := "config-" + secret + ".json"
	filePath := filepath.Join(dir, leakyFileName)

	if err := os.WriteFile(filePath, []byte("innocuous line\n"), 0600); err != nil {
		t.Fatal(err)
	}

	rec := newRecordTB(t)
	scanner := leakscan.New(secret)
	scanner.ScanDir(rec, dir)

	if !rec.failed {
		t.Fatal("expected file path containing secret to trigger test failure")
	}

	assertNeverContains(t, rec.messages, []string{secret})
}

func TestDir_ShortSecret(t *testing.T) {
	// Secret shorter than 12 characters
	shortSecret := assembleTestItem("fox", 1) // "fox1" (length 4)
	dir := t.TempDir()

	f1 := filepath.Join(dir, "hit.txt")
	if err := os.WriteFile(f1, []byte("payload "+shortSecret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	rec := newRecordTB(t)
	scanner := leakscan.New(shortSecret)
	scanner.ScanDir(rec, dir)

	if !rec.failed {
		t.Fatal("expected exact short secret to be detected")
	}
	assertNeverContains(t, rec.messages, []string{shortSecret})

	// Substring of short secret should NOT trigger
	rec2 := newRecordTB(t)
	dir2 := t.TempDir()
	f2 := filepath.Join(dir2, "sub.txt")
	if err := os.WriteFile(f2, []byte("payload "+shortSecret[:2]+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	scanner.ScanDir(rec2, dir2)
	if rec2.failed {
		t.Fatal("substring of short secret under 12 characters should not trigger")
	}
}

func TestDir_MultipleFindingsDeduplicated(t *testing.T) {
	secret := assembleTestItem("golf", 4)
	dir := t.TempDir()
	f := filepath.Join(dir, "multi.txt")

	// Same line contains secret multiple times, line 3 also contains it
	content := "clean line 1\n" +
		secret + " and " + secret + "\n" +
		"another " + secret + "\n"
	if err := os.WriteFile(f, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	scanner := leakscan.New(secret)
	findings, err := scanner.FindInDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Should report multi.txt:2 and multi.txt:3 exactly once each
	if len(findings) != 2 {
		t.Fatalf("expected 2 deduplicated findings, got %d: %v", len(findings), findings)
	}
	if !strings.Contains(findings[0].Location, "multi.txt:2") {
		t.Errorf("expected line 2, got %s", findings[0].Location)
	}
	if !strings.Contains(findings[1].Location, "multi.txt:3") {
		t.Errorf("expected line 3, got %s", findings[1].Location)
	}
}

func TestEnv_ValueLeak(t *testing.T) {
	secret := assembleTestItem("hotel", 4)
	env := []string{
		"PATH=/bin:/usr/bin",
		"APP_SETTING=safe",
		"TARGET_CREDS=" + secret,
	}

	rec := newRecordTB(t)
	scanner := leakscan.New(secret)
	scanner.ScanEnv(rec, env)

	if !rec.failed {
		t.Fatal("expected env value match to fail the test")
	}

	foundLocation := false
	for _, msg := range rec.messages {
		if strings.Contains(msg, `env var "TARGET_CREDS"`) {
			foundLocation = true
			break
		}
	}
	if !foundLocation {
		t.Fatalf("expected env location in messages: %v", rec.messages)
	}

	assertNeverContains(t, rec.messages, []string{secret})
}

func TestEnv_KeyLeak(t *testing.T) {
	secret := assembleTestItem("india", 4)
	env := []string{
		"NORMAL=123",
		secret + "=some_val",
	}

	rec := newRecordTB(t)
	scanner := leakscan.New(secret)
	scanner.ScanEnv(rec, env)

	if !rec.failed {
		t.Fatal("expected env key match to fail the test")
	}

	assertNeverContains(t, rec.messages, []string{secret})
}

func TestEnv_Clean(t *testing.T) {
	secret := assembleTestItem("juliet", 4)
	env := []string{
		"PATH=/bin:/usr/bin",
		"APP_KEY=clean_data",
	}

	rec := newRecordTB(t)
	scanner := leakscan.New(secret)
	scanner.ScanEnv(rec, env)

	if rec.failed {
		t.Fatalf("expected clean env not to fail, got: %v", rec.messages)
	}
}

func TestStream_ValueLeak(t *testing.T) {
	secret := assembleTestItem("kilo", 4)
	var buf bytes.Buffer
	buf.WriteString("line one\nline two contains " + secret + "\nline three\n")

	rec := newRecordTB(t)
	scanner := leakscan.New(secret)
	scanner.ScanStream(rec, "stdout", &buf)

	if !rec.failed {
		t.Fatal("expected stream leak to fail test")
	}

	foundLocation := false
	for _, msg := range rec.messages {
		if strings.Contains(msg, `stream "stdout":2`) {
			foundLocation = true
			break
		}
	}
	if !foundLocation {
		t.Fatalf("expected location stream \"stdout\":2 in messages: %v", rec.messages)
	}

	assertNeverContains(t, rec.messages, []string{secret})
}

func TestStream_Clean(t *testing.T) {
	secret := assembleTestItem("lima", 4)
	var buf bytes.Buffer
	buf.WriteString("safe line one\nsafe line two\n")

	rec := newRecordTB(t)
	scanner := leakscan.New(secret)
	scanner.ScanStream(rec, "stderr", &buf)

	if rec.failed {
		t.Fatalf("expected clean stream not to fail, got: %v", rec.messages)
	}
}

func TestBytes_ValueLeak(t *testing.T) {
	secret := assembleTestItem("mike", 4)
	payload := []byte("payload line 1\n" + secret + "\n")

	rec := newRecordTB(t)
	scanner := leakscan.New(secret)
	scanner.ScanBytes(rec, "captured_bytes", payload)

	if !rec.failed {
		t.Fatal("expected byte slice leak to fail test")
	}

	assertNeverContains(t, rec.messages, []string{secret})
}

func TestTarget_Unified(t *testing.T) {
	s1 := assembleTestItem("november", 4)
	s2 := assembleTestItem("oscar", 4)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("clean file\n"), 0600); err != nil {
		t.Fatal(err)
	}

	env := []string{"ENV_A=clean", "ENV_B=" + s1}

	var streamBuf bytes.Buffer
	streamBuf.WriteString("line1\n" + s2 + "\n")

	target := leakscan.Target{
		Dir:     dir,
		Env:     env,
		Streams: map[string]io.Reader{"proc_out": &streamBuf},
	}

	rec := newRecordTB(t)
	scanner := leakscan.New(s1, s2)
	scanner.Scan(rec, target)

	if !rec.failed {
		t.Fatal("expected unified target to fail test")
	}

	assertNeverContains(t, rec.messages, []string{s1, s2})
}

func TestStandaloneHelpers(t *testing.T) {
	s := assembleTestItem("papa", 4)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("data "+s+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	rec := newRecordTB(t)
	leakscan.ScanDir(rec, []string{s}, dir)
	if !rec.failed {
		t.Fatal("expected standalone ScanDir to detect leak")
	}

	recEnv := newRecordTB(t)
	leakscan.ScanEnv(recEnv, []string{s}, []string{"VAR=" + s})
	if !recEnv.failed {
		t.Fatal("expected standalone ScanEnv to detect leak")
	}

	recStream := newRecordTB(t)
	var streamBuf bytes.Buffer
	streamBuf.WriteString(s)
	leakscan.ScanStream(recStream, []string{s}, "output", &streamBuf)
	if !recStream.failed {
		t.Fatal("expected standalone ScanStream to detect leak")
	}

	recBytes := newRecordTB(t)
	leakscan.ScanBytes(recBytes, []string{s}, "raw", []byte(s))
	if !recBytes.failed {
		t.Fatal("expected standalone ScanBytes to detect leak")
	}
}

func TestWithOptions_Fatal(t *testing.T) {
	s := assembleTestItem("quebec", 4)
	scanner := leakscan.NewWithOptions(leakscan.Options{Fatal: true}, s)

	rec := newRecordTB(t)
	scanner.ScanEnv(rec, []string{"VAR=" + s})

	if !rec.failed {
		t.Fatal("expected fatal scanner to fail")
	}
	assertNeverContains(t, rec.messages, []string{s})
}

func TestDir_Substring15(t *testing.T) {
	secret := assembleTestItem("romeo", 6) // length ~36
	sub15 := secret[5:20]                  // 15 characters (> 12)
	dir := t.TempDir()
	filePath := filepath.Join(dir, "sub15.txt")

	if err := os.WriteFile(filePath, []byte("prefix "+sub15+" suffix\n"), 0600); err != nil {
		t.Fatal(err)
	}

	rec := newRecordTB(t)
	scanner := leakscan.New(secret)
	scanner.ScanDir(rec, dir)

	if !rec.failed {
		t.Fatal("expected 15-char substring to trigger leak detection")
	}
	assertNeverContains(t, rec.messages, []string{secret, sub15})
}

func TestDir_Substring12_Base64(t *testing.T) {
	secret := assembleTestItem("sierra", 5)
	sub12 := secret[3:15]
	subB64 := base64.StdEncoding.EncodeToString([]byte(sub12))

	dir := t.TempDir()
	filePath := filepath.Join(dir, "sub_b64.txt")

	if err := os.WriteFile(filePath, []byte("data "+subB64+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	rec := newRecordTB(t)
	scanner := leakscan.New(secret)
	scanner.ScanDir(rec, dir)

	if !rec.failed {
		t.Fatal("expected base64 of 12-char substring to trigger leak detection")
	}
	assertNeverContains(t, rec.messages, []string{secret, sub12, subB64})
}

func TestDir_Substring12_URLEncoded(t *testing.T) {
	secret := assembleEscapableItem(7)
	runes := []rune(secret)
	sub12 := string(runes[2:14])
	subURL := url.QueryEscape(sub12)

	dir := t.TempDir()
	filePath := filepath.Join(dir, "sub_url.txt")

	if err := os.WriteFile(filePath, []byte("query="+subURL+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	rec := newRecordTB(t)
	scanner := leakscan.New(secret)
	scanner.ScanDir(rec, dir)

	if !rec.failed {
		t.Fatal("expected url-encoded 12-char substring to trigger leak detection")
	}
	assertNeverContains(t, rec.messages, []string{secret, sub12, subURL})
}

func TestDir_BinaryContent(t *testing.T) {
	secret := assembleTestItem("tango", 4)
	dir := t.TempDir()
	filePath := filepath.Join(dir, "binary.bin")

	// Null byte makes it binary
	data := append([]byte{0x00, 0x01, 0x02}, []byte(secret)...)
	data = append(data, 0x00, 0xFF)
	if err := os.WriteFile(filePath, data, 0600); err != nil {
		t.Fatal(err)
	}

	rec := newRecordTB(t)
	scanner := leakscan.New(secret)
	scanner.ScanDir(rec, dir)

	if !rec.failed {
		t.Fatal("expected binary leak to trigger failure")
	}

	foundOffset := false
	for _, msg := range rec.messages {
		if strings.Contains(msg, "offset 3") {
			foundOffset = true
			break
		}
	}
	if !foundOffset {
		t.Fatalf("expected offset 3 in failure message: %v", rec.messages)
	}
	assertNeverContains(t, rec.messages, []string{secret})
}

func TestScanner_EmptySecretIgnored(t *testing.T) {
	realSecret := assembleTestItem("uniform", 4)
	dir := t.TempDir()
	filePath := filepath.Join(dir, "clean.txt")
	if err := os.WriteFile(filePath, []byte("harmless content\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// Scanner initialized with empty strings and whitespace alongside real secret
	scanner := leakscan.New("", "   ", realSecret)

	rec := newRecordTB(t)
	scanner.ScanDir(rec, dir)

	if rec.failed {
		t.Fatalf("clean directory should not fail when empty secrets are ignored, got: %v", rec.messages)
	}
}
