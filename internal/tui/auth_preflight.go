package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/tmux"
)

const (
	defaultAuthPreflightTimeout = 2 * time.Second
	authObservationMaxAge       = 60 * time.Second
)

// ErrProviderNotSignedIn is returned by test preflight stubs to indicate an
// unauthenticated provider CLI.
var ErrProviderNotSignedIn = errors.New("provider not signed in")

// AuthPreflightFunc checks whether a provider CLI is signed in before MARSHAL
// opens an interactive chat or native session window.
type AuthPreflightFunc func(ctx context.Context, provider, binary string, env []string) error

var (
	authPreflightMu     sync.RWMutex
	customAuthPreflight AuthPreflightFunc

	authUnavailableMu    sync.RWMutex
	authUnavailableState = make(map[string]map[string]string)
)

// SetAuthPreflightForTest replaces the provider authentication preflight and
// returns a restore callback suitable for t.Cleanup.
func SetAuthPreflightForTest(fn AuthPreflightFunc) func() {
	authPreflightMu.Lock()
	prev := customAuthPreflight
	customAuthPreflight = fn
	authPreflightMu.Unlock()
	return func() {
		authPreflightMu.Lock()
		customAuthPreflight = prev
		authPreflightMu.Unlock()
		if prev == nil {
			authUnavailableMu.Lock()
			authUnavailableState = make(map[string]map[string]string)
			authUnavailableMu.Unlock()
			ClearAuthPreflightObservations()
		}
	}
}

type authPreflightCheckedKey struct {
	provider string
}

func withAuthPreflightChecked(ctx context.Context, provider string) context.Context {
	canonical := canonicalAuthProvider(provider)
	if canonical == "" {
		canonical = strings.ToLower(strings.TrimSpace(provider))
	}
	return context.WithValue(ctx, authPreflightCheckedKey{provider: canonical}, true)
}

func isAuthPreflightChecked(ctx context.Context, provider string) bool {
	if ctx == nil {
		return false
	}
	canonical := canonicalAuthProvider(provider)
	if canonical == "" {
		canonical = strings.ToLower(strings.TrimSpace(provider))
	}
	checked, _ := ctx.Value(authPreflightCheckedKey{provider: canonical}).(bool)
	return checked
}

type providerAuthError struct {
	provider string
	message  string
}

func (e *providerAuthError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *providerAuthError) Is(target error) bool {
	return target == ErrProviderNotSignedIn
}

func isProviderAuthError(err error) bool {
	var authErr *providerAuthError
	return errors.As(err, &authErr)
}

func canonicalAuthProvider(provider string) string {
	return canonicalNeutralProvider(provider)
}

func providerNotSignedInMessage(provider string) string {
	switch canonicalAuthProvider(provider) {
	case "codex":
		return "Codex is not signed in - run `codex login`"
	case "claude":
		return "Claude is not signed in - run `claude login`"
	case "opencode":
		return "OpenCode is not signed in - run `opencode auth login`"
	case "agy":
		return "Antigravity is not signed in - sign in through the Antigravity app"
	default:
		name := strings.TrimSpace(provider)
		if name == "" {
			name = "Provider"
		}
		return fmt.Sprintf("%s is not signed in", name)
	}
}

func normalizeProviderAuthError(provider string, err error) *providerAuthError {
	var existing *providerAuthError
	if errors.As(err, &existing) && existing != nil {
		msg := existing.message
		if strings.TrimSpace(msg) == "" {
			msg = providerNotSignedInMessage(provider)
		}
		return &providerAuthError{provider: provider, message: msg}
	}
	msg := providerNotSignedInMessage(provider)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "is not signed in") {
		msg = err.Error()
	}
	return &providerAuthError{provider: provider, message: msg}
}

// AuthObservation records explicit observed authentication evidence.
// It distinguishes signed in, not signed in, skipped, and unsupported checks.
type AuthObservation struct {
	Provider   string
	Canonical  string
	Status     string // "SIGNED_IN", "NOT_SIGNED_IN", "SKIPPED", "UNSUPPORTED", "TIMEOUT", "ERROR", "NOT_PROBED"
	Reason     string
	ObservedAt time.Time
	Source     string
	SessionID  string
}

var (
	authObsMu    sync.RWMutex
	authObsCache = make(map[string]AuthObservation)
)

// RecordAuthPreflightObservation saves an explicit auth observation.
func RecordAuthPreflightObservation(obs AuthObservation) {
	if obs.ObservedAt.IsZero() {
		obs.ObservedAt = time.Now().UTC()
	}
	canonical := canonicalAuthProvider(obs.Provider)
	if canonical == "" {
		canonical = strings.ToLower(strings.TrimSpace(obs.Provider))
	}
	obs.Canonical = canonical
	authObsMu.Lock()
	authObsCache[canonical] = obs
	authObsMu.Unlock()
}

// GetAuthPreflightObservation retrieves a cached auth observation if within maxAge.
func GetAuthPreflightObservation(provider string, maxAge time.Duration) (AuthObservation, bool) {
	canonical := canonicalAuthProvider(provider)
	if canonical == "" {
		canonical = strings.ToLower(strings.TrimSpace(provider))
	}
	authObsMu.RLock()
	obs, ok := authObsCache[canonical]
	authObsMu.RUnlock()
	if !ok {
		return AuthObservation{}, false
	}
	if maxAge > 0 && time.Since(obs.ObservedAt) > maxAge {
		return obs, false
	}
	return obs, true
}

// ClearAuthPreflightObservations resets cached observations.
func ClearAuthPreflightObservations() {
	authObsMu.Lock()
	authObsCache = make(map[string]AuthObservation)
	authObsMu.Unlock()
}

// ProbeAuthReadOnly runs a bounded, read-only authentication check without side effects.
// It never scrapes credentials, never executes interactive turns, and never mutates auth files.
func ProbeAuthReadOnly(ctx context.Context, provider, binary string, env []string) AuthObservation {
	canonical := canonicalAuthProvider(provider)
	if canonical == "" {
		canonical = strings.ToLower(strings.TrimSpace(provider))
	}
	now := time.Now().UTC()

	authPreflightMu.RLock()
	fn := customAuthPreflight
	authPreflightMu.RUnlock()
	if fn != nil {
		err := fn(ctx, provider, binary, env)
		if err == nil {
			obs := AuthObservation{
				Provider:   provider,
				Canonical:  canonical,
				Status:     "SIGNED_IN",
				Reason:     "auth preflight hook reported signed in",
				ObservedAt: now,
				Source:     "customAuthPreflight",
			}
			RecordAuthPreflightObservation(obs)
			return obs
		}
		status := "NOT_SIGNED_IN"
		if errors.Is(err, context.DeadlineExceeded) {
			status = "TIMEOUT"
		}
		obs := AuthObservation{
			Provider:   provider,
			Canonical:  canonical,
			Status:     status,
			Reason:     err.Error(),
			ObservedAt: now,
			Source:     "customAuthPreflight",
		}
		RecordAuthPreflightObservation(obs)
		return obs
	}

	if canonical != "codex" {
		var reason string
		switch canonical {
		case "claude":
			reason = "claude auth status mutates ~/.claude.json on disk; read-only auth inspection is not supported"
		case "opencode":
			reason = "opencode has no read-only auth status CLI; inspection is not supported"
		case "agy", "antigravity":
			reason = "antigravity has no read-only auth status CLI; sign-in is managed via application"
		default:
			reason = fmt.Sprintf("%s does not expose a read-only auth status CLI", provider)
		}
		obs := AuthObservation{
			Provider:   provider,
			Canonical:  canonical,
			Status:     "UNSUPPORTED",
			Reason:     reason,
			ObservedAt: now,
			Source:     "internal/tui/auth_preflight.go",
		}
		RecordAuthPreflightObservation(obs)
		return obs
	}

	err := defaultAuthPreflight(ctx, provider, binary, env)
	if err == nil {
		obs, ok := GetAuthPreflightObservation(canonical, 5*time.Second)
		if ok && obs.Status != "" {
			return obs
		}
		obs = AuthObservation{
			Provider:   provider,
			Canonical:  canonical,
			Status:     "SIGNED_IN",
			Reason:     "Codex is signed in",
			ObservedAt: now,
			Source:     "codex login status",
		}
		RecordAuthPreflightObservation(obs)
		return obs
	}

	obs, ok := GetAuthPreflightObservation(canonical, 5*time.Second)
	if ok && obs.Status != "" {
		return obs
	}
	obs = AuthObservation{
		Provider:   provider,
		Canonical:  canonical,
		Status:     "NOT_SIGNED_IN",
		Reason:     err.Error(),
		ObservedAt: now,
		Source:     "codex login status",
	}
	RecordAuthPreflightObservation(obs)
	return obs
}

// defaultAuthPreflight runs a bounded, side-effect-free CLI auth check where
// the provider offers one. Only Codex currently provides a read-only check
// (`codex login status`); Claude's `claude auth status` mutates ~/.claude.json
// and backups on disk, while OpenCode and Antigravity have no login-status CLI.
func defaultAuthPreflight(ctx context.Context, provider, binary string, env []string) error {
	now := time.Now().UTC()
	if canonicalAuthProvider(provider) != "codex" {
		RecordAuthPreflightObservation(AuthObservation{
			Provider:   provider,
			Canonical:  canonicalAuthProvider(provider),
			Status:     "UNSUPPORTED",
			Reason:     fmt.Sprintf("%s does not offer a read-only auth status CLI; inspection is not supported", provider),
			Source:     "defaultAuthPreflight",
			ObservedAt: now,
		})
		return nil
	}
	if binary != "" {
		base := strings.TrimSuffix(strings.ToLower(filepath.Base(binary)), ".exe")
		if base != "codex" {
			RecordAuthPreflightObservation(AuthObservation{
				Provider:   provider,
				Canonical:  "codex",
				Status:     "NOT_PROBED",
				Reason:     fmt.Sprintf("binary %q is not codex", binary),
				Source:     "defaultAuthPreflight",
				ObservedAt: now,
			})
			return nil
		}
		if !filepath.IsAbs(binary) {
			if _, err := exec.LookPath(binary); err != nil {
				resolved, findErr := project.FindBinary("codex")
				if findErr != nil {
					RecordAuthPreflightObservation(AuthObservation{
						Provider:   "codex",
						Canonical:  "codex",
						Status:     "NOT_PROBED",
						Reason:     "codex executable not found",
						Source:     "defaultAuthPreflight",
						ObservedAt: now,
					})
					return nil
				}
				binary = resolved
			}
		}
	} else {
		resolved, err := project.FindBinary("codex")
		if err != nil {
			RecordAuthPreflightObservation(AuthObservation{
				Provider:   "codex",
				Canonical:  "codex",
				Status:     "NOT_PROBED",
				Reason:     "codex executable not found",
				Source:     "defaultAuthPreflight",
				ObservedAt: now,
			})
			return nil
		}
		binary = resolved
	}

	probeCtx, cancel := context.WithTimeout(ctx, defaultAuthPreflightTimeout)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, binary, "login", "status")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil && cmd.Process.Pid > 1 {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		if cmd.Process != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = 50 * time.Millisecond
	cmd.Env = mergeAuthProbeEnv(os.Environ(), env)

	out, err := cmd.CombinedOutput()
	if err == nil {
		RecordAuthPreflightObservation(AuthObservation{
			Provider:   "codex",
			Canonical:  "codex",
			Status:     "SIGNED_IN",
			Reason:     "Codex reported signed in",
			Source:     "codex login status",
			ObservedAt: now,
		})
		return nil
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	text := string(out)
	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		RecordAuthPreflightObservation(AuthObservation{
			Provider:   "codex",
			Canonical:  "codex",
			Status:     "TIMEOUT",
			Reason:     "Codex auth probe timed out after 2s",
			Source:     "codex login status",
			ObservedAt: now,
		})
		if detectProviderAuthFailure("codex", text) {
			return &providerAuthError{provider: "codex", message: providerNotSignedInMessage("codex")}
		}
		return nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		RecordAuthPreflightObservation(AuthObservation{
			Provider:   "codex",
			Canonical:  "codex",
			Status:     "ERROR",
			Reason:     err.Error(),
			Source:     "codex login status",
			ObservedAt: now,
		})
		return nil
	}
	if isUnsupportedLoginStatusOutput(text) {
		RecordAuthPreflightObservation(AuthObservation{
			Provider:   "codex",
			Canonical:  "codex",
			Status:     "UNSUPPORTED",
			Reason:     "Codex does not support login status subcommand",
			Source:     "codex login status",
			ObservedAt: now,
		})
		return nil
	}
	RecordAuthPreflightObservation(AuthObservation{
		Provider:   "codex",
		Canonical:  "codex",
		Status:     "NOT_SIGNED_IN",
		Reason:     providerNotSignedInMessage("codex"),
		Source:     "codex login status",
		ObservedAt: now,
	})
	return &providerAuthError{provider: "codex", message: providerNotSignedInMessage("codex")}
}

func isUnsupportedLoginStatusOutput(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"unrecognized subcommand",
		"unknown subcommand",
		"unknown command",
		"unexpected argument 'login'",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return strings.Contains(text, "CODEX-READY") || strings.Contains(text, "NATIVE-READY")
}

func mergeAuthProbeEnv(base, overrides []string) []string {
	merged := make([]string, 0, len(base)+len(overrides)+1)
	index := make(map[string]int, len(base)+len(overrides)+1)
	put := func(entry string) {
		eq := strings.IndexByte(entry, '=')
		if eq <= 0 {
			return
		}
		key := entry[:eq]
		if pos, exists := index[key]; exists {
			merged[pos] = entry
			return
		}
		index[key] = len(merged)
		merged = append(merged, entry)
	}
	for _, entry := range base {
		put(entry)
	}
	for _, entry := range overrides {
		put(entry)
	}
	put("NO_COLOR=1")
	return merged
}

func shouldSkipAuthPreflight(requestedArgs, launchArgs []string) bool {
	candidates := requestedArgs
	if len(candidates) == 0 {
		candidates = launchArgs
	}
	if len(candidates) == 0 {
		return false
	}
	for _, arg := range candidates {
		switch arg {
		case "--help", "-h", "--version", "-v", "-V":
			return true
		}
	}
	sub, rest := firstProviderPositional(candidates)
	switch sub {
	case "login", "logout", "auth", "help", "doctor", "mcp", "plugin", "plugins", "features":
		return true
	case "session":
		if len(rest) > 0 && (rest[0] == "--help" || rest[0] == "-h") {
			return true
		}
	}
	return false
}

func isAuthLoginSubcommand(args []string) bool {
	sub, rest := firstProviderPositional(args)
	if sub == "login" {
		return true
	}
	return sub == "auth" && len(rest) > 0 && strings.EqualFold(rest[0], "login")
}

func firstProviderPositional(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		switch arg {
		case "-c", "--config", "-m", "--model", "--sandbox", "--ask-for-approval", "-C", "--cd", "-p", "--profile":
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return strings.ToLower(arg), args[i+1:]
	}
	return "", nil
}

// detectProviderAuthFailure inspects visible pane text or captured CLI output
// for interactive login prompts and non-interactive token/authentication errors.
func detectProviderAuthFailure(provider, raw string) bool {
	clean := StripANSI(raw)
	if strings.TrimSpace(clean) == "" {
		return false
	}
	lines := strings.Split(clean, "\n")
	if len(lines) > 60 {
		lines = lines[len(lines)-60:]
	}

	var unquotedLines []string
	for _, line := range lines {
		trimmed := trimPromptChrome(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if lower == "not logged in" || lower == "error: not logged in" ||
			(strings.HasPrefix(lower, "not logged in") && strings.Contains(lower, "login")) ||
			(strings.HasPrefix(lower, "invalid api key") && strings.Contains(lower, "login")) {
			if !looksQuotedLiteral(line, "not logged in") && !looksQuotedLiteral(line, "invalid api key") {
				return true
			}
		}
		if !looksQuotedLiteral(line, "sign in with chatgpt") &&
			!looksQuotedLiteral(line, "failed to refresh token") &&
			!looksQuotedLiteral(line, "refresh token was revoked") &&
			!looksQuotedLiteral(line, "refresh_token_reused") {
			unquotedLines = append(unquotedLines, lower)
		}
	}
	if len(unquotedLines) == 0 {
		return false
	}
	joined := strings.Join(unquotedLines, "\n")

	if strings.Contains(joined, "sign in with chatgpt") &&
		(strings.Contains(joined, "device code") || strings.Contains(joined, "api key")) {
		return true
	}
	if strings.Contains(joined, "failed to refresh token") &&
		(strings.Contains(joined, "revoked") ||
			strings.Contains(joined, "reconnecting") ||
			strings.Contains(joined, "expired") ||
			strings.Contains(joined, "401") ||
			strings.Contains(joined, "sign in") ||
			strings.Contains(joined, "log out")) {
		return true
	}
	if strings.Contains(joined, "refresh token was revoked") || strings.Contains(joined, "refresh_token_reused") {
		return true
	}
	switch canonicalAuthProvider(provider) {
	case "claude":
		if strings.Contains(joined, "not logged in") && (strings.Contains(joined, "/login") || strings.Contains(joined, "claude login") || strings.Contains(joined, "claude auth login")) {
			return true
		}
	case "opencode":
		if strings.Contains(joined, "opencode auth login") && (strings.Contains(joined, "not signed in") || strings.Contains(joined, "unauthorized") || strings.Contains(joined, "authentication")) {
			return true
		}
	}
	return false
}

func trimPromptChrome(line string) string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.Trim(trimmed, "│╭╮╰╯─┌┐└┘├┤┬┴┼")
	trimmed = strings.TrimSpace(trimmed)
	trimmed = strings.TrimLeft(trimmed, "•❯>*0123456789.) ")
	return strings.TrimSpace(trimmed)
}

func looksQuotedLiteral(line, phrase string) bool {
	lower := strings.ToLower(line)
	idx := strings.Index(lower, phrase)
	if idx <= 0 {
		return false
	}
	prev := lower[idx-1]
	return prev == '"' || prev == '\'' || prev == '`'
}

func authWorkspaceKeys(sessionID string, dirs ...string) []string {
	if sessionID == "" {
		return nil
	}
	seen := make(map[string]bool, len(dirs)+1)
	var keys []string
	for _, dir := range dirs {
		clean := ""
		if strings.TrimSpace(dir) != "" {
			clean = filepath.Clean(dir)
		}
		key := sessionID + "\x00" + clean
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	return keys
}

func (w *Workspace) preflightProviderAuth(ctx context.Context, provider, binary string, env []string) error {
	if isAuthPreflightChecked(ctx, provider) {
		return nil
	}
	authPreflightMu.RLock()
	fn := customAuthPreflight
	authPreflightMu.RUnlock()
	if fn == nil {
		fn = defaultAuthPreflight
	}
	err := fn(ctx, provider, binary, env)
	if err == nil {
		if customAuthPreflight != nil {
			RecordAuthPreflightObservation(AuthObservation{
				Provider:   provider,
				Status:     "SIGNED_IN",
				Reason:     "preflight check passed",
				Source:     "customAuthPreflight",
				ObservedAt: time.Now().UTC(),
			})
		}
		w.clearProviderAuthUnavailable(provider)
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	authErr := normalizeProviderAuthError(provider, err)
	RecordAuthPreflightObservation(AuthObservation{
		Provider:   provider,
		Status:     "NOT_SIGNED_IN",
		Reason:     authErr.Error(),
		Source:     "preflightProviderAuth",
		ObservedAt: time.Now().UTC(),
	})
	w.markProviderAuthUnavailable(provider, authErr.Error())
	return authErr
}

func (w *Workspace) hasHealthyMarshalChatWindow(ctx context.Context, provider string) bool {
	if !w.isTmuxActive() {
		return false
	}
	w.tmuxMu.Lock()
	existing := copyAgentLocked(w.tmuxActiveWins["marshal-chat"])
	session := w.tmuxSession
	w.tmuxMu.Unlock()
	if existing == nil || existing.paneID == "" || existing.state == "unavailable" {
		return false
	}
	if canonicalAuthProvider(existing.provider) != canonicalAuthProvider(provider) {
		return false
	}
	panes, err := tmux.ListPanes(ctx, session)
	if err != nil {
		return false
	}
	for _, p := range panes {
		if p.PaneID == existing.paneID && !p.Dead {
			return true
		}
	}
	return false
}

func (w *Workspace) markProviderAuthUnavailable(provider, msg string) {
	if w == nil {
		return
	}
	canonical := canonicalAuthProvider(provider)
	if canonical == "" {
		canonical = strings.ToLower(strings.TrimSpace(provider))
	}
	if strings.TrimSpace(msg) == "" {
		msg = providerNotSignedInMessage(provider)
	}

	w.mu.RLock()
	sessionID := w.sessionID
	workDir := w.workDir
	w.mu.RUnlock()
	root := w.providerRoot()

	keys := authWorkspaceKeys(sessionID, workDir, root)
	if len(keys) > 0 {
		authUnavailableMu.Lock()
		for _, k := range keys {
			byProvider := authUnavailableState[k]
			if byProvider == nil {
				byProvider = make(map[string]string)
				authUnavailableState[k] = byProvider
			}
			byProvider[canonical] = msg
		}
		authUnavailableMu.Unlock()
	}

	sanitized := hideMarshalProtocol(RedactContent(msg, nil))
	w.mu.Lock()
	for i := range w.state.Participants {
		p := &w.state.Participants[i]
		if participantMatchesProvider(*p, canonical, provider) {
			p.IsActive = false
			p.Model = UnavailableModel
		}
	}
	if !containsExactLine(w.state.LastOutput, sanitized) {
		if w.state.LastOutput != "" {
			w.state.LastOutput += "\n" + sanitized
		} else {
			w.state.LastOutput = sanitized
			w.state.LastOutputIsError = true
		}
		w.state.LastOutput = activityTail(w.state.LastOutput)
	}
	w.mu.Unlock()
	w.addNote(sanitized, true)

	w.tmuxMu.Lock()
	if w.tmuxAlerts == nil {
		w.tmuxAlerts = make(map[string]string)
	}
	w.tmuxAlerts["auth:"+canonical] = canonical + ": not signed in"
	hasTmux := w.tmuxPath != "" && w.tmuxSession != ""
	w.tmuxMu.Unlock()
	if hasTmux {
		w.updateTmuxStatusLine(context.Background())
	}
	w.requestRepaint()
}

func (w *Workspace) clearProviderAuthUnavailable(provider string) {
	if w == nil {
		return
	}
	canonical := canonicalAuthProvider(provider)
	if canonical == "" {
		canonical = strings.ToLower(strings.TrimSpace(provider))
	}

	w.mu.RLock()
	sessionID := w.sessionID
	workDir := w.workDir
	w.mu.RUnlock()
	root := w.providerRoot()

	keys := authWorkspaceKeys(sessionID, workDir, root)
	wasUnavailable := false
	authUnavailableMu.Lock()
	for _, k := range keys {
		if byProvider := authUnavailableState[k]; byProvider != nil {
			if _, ok := byProvider[canonical]; ok {
				wasUnavailable = true
				delete(byProvider, canonical)
				if len(byProvider) == 0 {
					delete(authUnavailableState, k)
				}
			}
		}
	}
	authUnavailableMu.Unlock()

	w.tmuxMu.Lock()
	alertKey := "auth:" + canonical
	_, hadAlert := w.tmuxAlerts[alertKey]
	if hadAlert {
		delete(w.tmuxAlerts, alertKey)
	}
	hasTmux := w.tmuxPath != "" && w.tmuxSession != ""
	w.tmuxMu.Unlock()

	if !wasUnavailable && !hadAlert {
		return
	}

	installed := isProviderBinaryInstalled(canonical)
	w.mu.Lock()
	for i := range w.state.Participants {
		p := &w.state.Participants[i]
		if participantMatchesProvider(*p, canonical, provider) && installed {
			p.IsActive = true
			if p.Model == UnavailableModel {
				p.Model = UnknownModel
			}
		}
	}
	w.mu.Unlock()

	if hasTmux && hadAlert {
		w.updateTmuxStatusLine(context.Background())
	}
	w.requestRepaint()
}

func isProviderBinaryInstalled(canonical string) bool {
	if canonical == "" {
		return false
	}
	if _, err := project.FindBinary(canonical); err == nil {
		return true
	}
	binaryName := canonical
	if canonical == "antigravity" {
		binaryName = "agy"
	}
	_, err := exec.LookPath(binaryName)
	return err == nil
}

func participantMatchesProvider(p model.Participant, canonical, raw string) bool {
	if canonical != "" {
		if canonicalAuthProvider(p.AgentID) == canonical || canonicalAuthProvider(p.Harness) == canonical {
			return true
		}
	}
	return raw != "" && (strings.EqualFold(p.AgentID, raw) || strings.EqualFold(p.Harness, raw))
}

func containsExactLine(text, target string) bool {
	if target == "" {
		return true
	}
	for _, line := range strings.Split(text, "\n") {
		if line == target || line == "Error: "+target {
			return true
		}
	}
	return false
}

func applyAuthUnavailableToState(s UIState, workDir string) (UIState, []string) {
	if s.SessionID == "" {
		return s, nil
	}
	keys := authWorkspaceKeys(s.SessionID, workDir)
	authUnavailableMu.RLock()
	var unavailable map[string]string
	for _, k := range keys {
		if byProvider := authUnavailableState[k]; len(byProvider) > 0 {
			if unavailable == nil {
				unavailable = make(map[string]string, len(byProvider))
			}
			for prov, msg := range byProvider {
				unavailable[prov] = msg
			}
		}
	}
	authUnavailableMu.RUnlock()
	if len(unavailable) == 0 {
		return s, nil
	}

	if len(s.Participants) > 0 {
		copied := make([]model.Participant, len(s.Participants))
		copy(copied, s.Participants)
		for i := range copied {
			for prov := range unavailable {
				if participantMatchesProvider(copied[i], prov, prov) {
					copied[i].IsActive = false
					copied[i].Model = UnavailableModel
				}
			}
		}
		s.Participants = copied
	}

	providers := make([]string, 0, len(unavailable))
	for prov := range unavailable {
		providers = append(providers, prov)
	}
	sort.Strings(providers)
	notices := make([]string, 0, len(providers))
	for _, prov := range providers {
		notices = append(notices, unavailable[prov])
	}
	return s, notices
}

func (w *Workspace) monitorTaskAuthAndState(ctx context.Context, agent *activeTmuxAgent, h *driver.Handle) {
	authCtx, cancelAuth := context.WithCancel(ctx)
	var authWG sync.WaitGroup
	authWG.Add(1)
	go func() {
		defer authWG.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		reported := false
		for {
			select {
			case <-authCtx.Done():
				return
			case <-h.Done():
				return
			case <-ticker.C:
				w.tmuxMu.Lock()
				paneID := agent.paneID
				provider := agent.provider
				w.tmuxMu.Unlock()
				if strings.TrimSpace(paneID) == "" || reported {
					continue
				}
				visible, err := tmux.CaptureVisiblePane(authCtx, paneID)
				if err == nil && detectProviderAuthFailure(provider, visible) {
					reported = true
					w.tmuxMu.Lock()
					agent.state = "unavailable"
					w.tmuxMu.Unlock()
					w.markProviderAuthUnavailable(provider, providerNotSignedInMessage(provider))
				}
			}
		}
	}()
	w.monitorTaskState(ctx, agent, h)
	cancelAuth()
	authWG.Wait()
}
