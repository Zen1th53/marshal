//go:build !governed && linux

package netpolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/godbus/dbus/v5"
)

// Native secrets remain in host memory. This source neither unlocks a keyring,
// prompts, logs D-Bus errors, refreshes tokens nor opens provider model sessions.
type agyOAuthCredential struct {
	access, refresh, id string
	expires             time.Time
}
type agySecretItem struct {
	Path       string
	Locked     bool
	Attributes map[string]string
}
type agySecretReader interface {
	Search(context.Context, map[string]string) ([]agySecretItem, error)
	Secret(context.Context, agySecretItem) ([]byte, error)
}

// agyUnavailable names why the host's Antigravity sign-in cannot be read. It
// still wraps ErrCredentialBroker, so callers keep refusing governed work.
// Messages carry only MARSHAL's own wording: never a secret, token, item path
// or a D-Bus error text.
func agyUnavailable(cause string) error {
	return fmt.Errorf("%w: Antigravity sign-in: %s", ErrCredentialBroker, cause)
}

const agyFormatCause = "the keyring entry (service=gemini, username=antigravity) is not in the Antigravity consumer sign-in format MARSHAL reads; sign in again with agy"

func agyNativeAttributes() map[string]string {
	return map[string]string{"service": "gemini", "username": "antigravity", "xdg:schema": "org.freedesktop.Secret.Generic"}
}
func exactAGYAttributes(attrs map[string]string) bool {
	want := agyNativeAttributes()
	if len(attrs) != len(want) {
		return false
	}
	for k, v := range want {
		if attrs[k] != v {
			return false
		}
	}
	return true
}
func parseAGYOAuthCredential(raw []byte) (agyOAuthCredential, error) {
	var zero agyOAuthCredential
	if len(raw) == 0 {
		return zero, agyUnavailable("the keyring entry is empty; sign in again with agy")
	}
	if len(raw) > 64<<10 || !utf8.Valid(raw) {
		return zero, agyUnavailable(agyFormatCause)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := checkAGYJSONValue(dec, 0); err != nil {
		return zero, agyUnavailable(agyFormatCause)
	}
	if _, err := dec.Token(); err != io.EOF {
		return zero, agyUnavailable(agyFormatCause)
	}

	// The keyring entry carries id_token. Antigravity CLI 1.4.10 and later
	// keep the sign-in in a file that carries id_token_email instead and adds
	// expires_in to the token; both layouts are exact, nothing else is read.
	var wrapper map[string]json.RawMessage
	if json.Unmarshal(raw, &wrapper) != nil {
		return zero, agyUnavailable(agyFormatCause)
	}
	fileLayout := exactAGYJSONKeys(wrapper, "token", "auth_method", "id_token_email")
	if !fileLayout && !exactAGYJSONKeys(wrapper, "token", "auth_method", "id_token") {
		return zero, agyUnavailable(agyFormatCause + " (expected exactly token, auth_method and id_token)")
	}
	var tokenFields map[string]json.RawMessage
	if json.Unmarshal(wrapper["token"], &tokenFields) != nil || !(exactAGYJSONKeys(tokenFields, "access_token", "refresh_token", "token_type", "expiry") ||
		(fileLayout && exactAGYJSONKeys(tokenFields, "access_token", "refresh_token", "token_type", "expiry", "expires_in"))) {
		return zero, agyUnavailable(agyFormatCause + " (token must hold exactly access_token, refresh_token, token_type and expiry)")
	}
	var auth struct {
		Token struct {
			Access    string `json:"access_token"`
			Refresh   string `json:"refresh_token"`
			Type      string `json:"token_type"`
			Expiry    string `json:"expiry"`
			ExpiresIn int64  `json:"expires_in"`
		} `json:"token"`
		Method string `json:"auth_method"`
		ID     string `json:"id_token"`
		Email  string `json:"id_token_email"`
	}
	dec = json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&auth) != nil {
		return zero, agyUnavailable(agyFormatCause)
	}
	if auth.Method != "consumer" {
		return zero, agyUnavailable("the stored sign-in is not a consumer (Google account) sign-in; only that sign-in can be brokered")
	}
	if auth.Token.Type != "Bearer" {
		return zero, agyUnavailable("the stored token is not a Bearer token")
	}
	required := []string{auth.Token.Access, auth.Token.Refresh, auth.ID}
	if fileLayout {
		required = []string{auth.Token.Access, auth.Token.Refresh, auth.Email}
	}
	for _, value := range required {
		if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
			return zero, agyUnavailable("the stored sign-in is missing or has a malformed access, refresh or id token; sign in again with agy")
		}
	}
	expiry, err := time.Parse(time.RFC3339Nano, auth.Token.Expiry)
	if err != nil {
		return zero, agyUnavailable("the stored token expiry is not an RFC 3339 time")
	}
	return agyOAuthCredential{access: auth.Token.Access, refresh: auth.Token.Refresh, id: auth.ID, expires: expiry}, nil
}

// Reject duplicate fields before semantic decoding. Future/unknown native
// formats fail closed rather than being treated as another provider's sign-in.
func checkAGYJSONValue(dec *json.Decoder, depth int) error {
	if depth > 16 {
		return ErrCredentialBroker
	}
	token, err := dec.Token()
	if err != nil {
		return err
	}
	d, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch d {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return ErrCredentialBroker
			}
			seen[name] = true
			if err := checkAGYJSONValue(dec, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := checkAGYJSONValue(dec, depth+1); err != nil {
				return err
			}
		}
	default:
		return ErrCredentialBroker
	}
	_, err = dec.Token()
	return err
}
func readAGYNativeCredential(ctx context.Context, store agySecretReader) (agyOAuthCredential, error) {
	var zero agyOAuthCredential
	if store == nil {
		return zero, agyUnavailable("no Secret Service reader")
	}
	if ctx.Err() != nil {
		return zero, agyUnavailable("the keyring read timed out")
	}
	items, err := store.Search(ctx, agyNativeAttributes())
	if err != nil {
		return zero, agyCause(err, "the Secret Service search failed")
	}
	switch {
	case len(items) == 0:
		return zero, agyUnavailable("no Antigravity sign-in in the keyring (service=gemini, username=antigravity); run agy and sign in")
	case len(items) > 1:
		return zero, agyUnavailable(fmt.Sprintf("%d keyring entries match service=gemini, username=antigravity; exactly one is required, remove the stale ones", len(items)))
	case items[0].Locked:
		return zero, agyUnavailable("the keyring holding the Antigravity sign-in is locked; unlock it (MARSHAL never unlocks a keyring or prompts)")
	case items[0].Path == "" || !exactAGYAttributes(items[0].Attributes):
		return zero, agyUnavailable("the keyring entry carries attributes other than exactly service=gemini, username=antigravity, xdg:schema=org.freedesktop.Secret.Generic")
	}
	raw, err := store.Secret(ctx, items[0])
	if err != nil {
		return zero, agyCause(err, "the keyring secret could not be read")
	}
	defer func() {
		for i := range raw {
			raw[i] = 0
		}
	}()
	c, err := parseAGYOAuthCredential(raw)
	if err != nil {
		return zero, err
	}
	if left := time.Until(c.expires); left <= 2*time.Minute {
		state := "expired at"
		if left > 0 {
			state = "expires within two minutes, at"
		}
		return zero, agyUnavailable(fmt.Sprintf("the stored access token %s %s; MARSHAL never refreshes it, run agy once so it renews its sign-in, then retry", state, c.expires.UTC().Format(time.RFC3339)))
	}
	if ctx.Err() != nil {
		return zero, agyUnavailable("the keyring read timed out")
	}
	return c, nil
}

// agyCause keeps a specific cause raised further down and otherwise names the
// failed step.
func agyCause(err error, step string) error {
	if errors.Is(err, ErrCredentialBroker) && err != ErrCredentialBroker {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return agyUnavailable(step + ": the keyring read timed out")
	}
	return agyUnavailable(step)
}

type agyDBusReader struct {
	conn  *dbus.Conn
	owner string
}

func (s *agyDBusReader) item(ctx context.Context, path dbus.ObjectPath) (agySecretItem, error) {
	var props map[string]dbus.Variant
	if s.conn.Object(s.owner, path).CallWithContext(ctx, "org.freedesktop.DBus.Properties.GetAll", dbus.FlagNoAutoStart, "org.freedesktop.Secret.Item").Store(&props) != nil {
		return agySecretItem{}, agyUnavailable("the keyring entry's properties could not be read from the Secret Service")
	}
	attrs, ok := props["Attributes"].Value().(map[string]string)
	if !ok {
		return agySecretItem{}, agyUnavailable("the Secret Service returned no attributes for the keyring entry")
	}
	locked, ok := props["Locked"].Value().(bool)
	if !ok {
		return agySecretItem{}, agyUnavailable("the Secret Service returned no lock state for the keyring entry")
	}
	return agySecretItem{Path: string(path), Locked: locked, Attributes: attrs}, nil
}
func (s *agyDBusReader) Search(ctx context.Context, attrs map[string]string) ([]agySecretItem, error) {
	var unlocked, locked []dbus.ObjectPath
	if s.conn.Object(s.owner, "/org/freedesktop/secrets").CallWithContext(ctx, "org.freedesktop.Secret.Service.SearchItems", dbus.FlagNoAutoStart, attrs).Store(&unlocked, &locked) != nil {
		return nil, agyUnavailable("the Secret Service search failed")
	}
	switch {
	case len(unlocked)+len(locked) == 0:
		return nil, nil
	case len(unlocked)+len(locked) > 1:
		// Report the count; readAGYNativeCredential words the cause.
		return make([]agySecretItem, len(unlocked)+len(locked)), nil
	case len(locked) == 1:
		return []agySecretItem{{Path: string(locked[0]), Locked: true, Attributes: agyNativeAttributes()}}, nil
	}
	item, err := s.item(ctx, unlocked[0])
	if err != nil {
		return nil, err
	}
	return []agySecretItem{item}, nil
}
func (s *agyDBusReader) Secret(ctx context.Context, item agySecretItem) ([]byte, error) {
	current, err := s.item(ctx, dbus.ObjectPath(item.Path))
	if err != nil {
		return nil, err
	}
	if current.Locked {
		return nil, agyUnavailable("the keyring holding the Antigravity sign-in was locked during the read; unlock it")
	}
	if !exactAGYAttributes(current.Attributes) {
		return nil, agyUnavailable("the keyring entry's attributes changed during the read")
	}
	var output dbus.Variant
	var session dbus.ObjectPath
	if s.conn.Object(s.owner, "/org/freedesktop/secrets").CallWithContext(ctx, "org.freedesktop.Secret.Service.OpenSession", dbus.FlagNoAutoStart, "plain", dbus.MakeVariant("")).Store(&output, &session) != nil || !session.IsValid() || session == "/" {
		return nil, agyUnavailable("the Secret Service refused a plain session")
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.conn.Object(s.owner, session).CallWithContext(closeCtx, "org.freedesktop.Secret.Session.Close", dbus.FlagNoAutoStart).Err
	}()
	var secret struct {
		Session     dbus.ObjectPath
		Parameters  []byte
		Value       []byte
		ContentType string
	}
	if s.conn.Object(s.owner, dbus.ObjectPath(item.Path)).CallWithContext(ctx, "org.freedesktop.Secret.Item.GetSecret", dbus.FlagNoAutoStart, session).Store(&secret) != nil {
		return nil, agyUnavailable("the Secret Service did not return the keyring secret")
	}
	if secret.Session != session || len(secret.Parameters) != 0 {
		return nil, agyUnavailable("the Secret Service answered for another session or with encryption parameters")
	}
	if len(secret.Value) > 64<<10 {
		return nil, agyUnavailable(agyFormatCause)
	}
	if secret.ContentType != "text/plain" && secret.ContentType != "text/plain; charset=utf-8" && secret.ContentType != "application/json" {
		return nil, agyUnavailable("the keyring secret has an unsupported content type")
	}
	return secret.Value, nil
}

// agyFileRelativePath is where Antigravity CLI 1.4.10 and later keep the
// sign-in they renew; older releases kept it only in the keyring.
const agyFileRelativePath = ".gemini/antigravity-cli/antigravity-oauth-token"

// loadAGYNativeCredential reads the sign-in from the file and from the
// keyring and uses whichever is valid for longer. A stale keyring entry left
// by an older CLI must not hide the sign-in the current CLI keeps renewing.
func loadAGYNativeCredential(ctx context.Context) (agyOAuthCredential, error) {
	home, homeErr := os.UserHomeDir()
	var fromFile agyOAuthCredential
	fileErr := agyUnavailable("no home directory for the sign-in file")
	if homeErr == nil {
		fromFile, fileErr = readAGYFileCredential(filepath.Join(home, agyFileRelativePath))
	}
	fromKeyring, keyringErr := loadAGYKeyringCredential(ctx)
	switch {
	case fileErr == nil && (keyringErr != nil || fromFile.expires.After(fromKeyring.expires)):
		return fromFile, nil
	case keyringErr == nil:
		return fromKeyring, nil
	case errors.Is(fileErr, os.ErrNotExist):
		return agyOAuthCredential{}, keyringErr
	}
	return agyOAuthCredential{}, fileErr
}

// readAGYFileCredential reads the CLI's sign-in file only when it is a
// regular file of this user that no one else can read, as the CLI writes it.
func readAGYFileCredential(path string) (agyOAuthCredential, error) {
	var zero agyOAuthCredential
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return zero, fmt.Errorf("%w: %w", agyUnavailable("no sign-in file"), os.ErrNotExist)
	}
	if err != nil {
		return zero, agyUnavailable("the sign-in file cannot be inspected")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || stat.Uid != uint32(os.Getuid()) || info.Mode().Perm()&0o077 != 0 || info.Size() > 64<<10 {
		return zero, agyUnavailable("the sign-in file is not a regular file of this user readable only by its owner")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return zero, agyUnavailable("the sign-in file could not be read")
	}
	defer func() {
		for i := range raw {
			raw[i] = 0
		}
	}()
	c, err := parseAGYOAuthCredential(raw)
	if err != nil {
		return zero, err
	}
	if left := time.Until(c.expires); left <= 2*time.Minute {
		return zero, agyUnavailable(fmt.Sprintf("the sign-in file's access token expired at %s; MARSHAL never refreshes it, run agy once so it renews its sign-in, then retry", c.expires.UTC().Format(time.RFC3339)))
	}
	return c, nil
}

func loadAGYKeyringCredential(ctx context.Context) (agyOAuthCredential, error) {
	var zero agyOAuthCredential
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Never autolaunch a D-Bus service or borrow a remote/system keyring. Native
	// desktop authentication belongs to this UID's already running local bus.
	socket := filepath.Join("/run/user", strconv.Itoa(os.Getuid()), "bus")
	info, err := os.Lstat(socket)
	if err != nil {
		return zero, agyUnavailable("no D-Bus session bus for this user at " + socket + "; sign in to a desktop session or start the user bus (systemctl --user start dbus)")
	}
	if info.Mode()&os.ModeSocket == 0 {
		return zero, agyUnavailable(socket + " is not a socket")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return zero, agyUnavailable(socket + " is not owned by this user")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return zero, agyUnavailable("cannot connect to the session bus at " + socket)
	}
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		conn.Close()
		return zero, agyUnavailable("cannot set a deadline on the session bus connection")
	}
	bus, err := dbus.NewConn(conn, dbus.WithContext(ctx))
	if err != nil {
		conn.Close()
		return zero, agyUnavailable("cannot open the session bus connection")
	}
	defer bus.Close()
	if bus.Auth([]dbus.Auth{dbus.AuthExternal(strconv.Itoa(os.Getuid()))}) != nil || bus.Hello() != nil {
		return zero, agyUnavailable("session bus authentication failed")
	}
	var owner string
	if bus.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", dbus.FlagNoAutoStart, "org.freedesktop.secrets").Store(&owner) != nil || !strings.HasPrefix(owner, ":") {
		return zero, agyUnavailable("no Secret Service (org.freedesktop.secrets) is running on the session bus; start your keyring (gnome-keyring, KWallet or KeePassXC Secret Service) and unlock it — MARSHAL never starts one")
	}
	return readAGYNativeCredential(ctx, &agyDBusReader{conn: bus, owner: owner})
}

func exactAGYJSONKeys(fields map[string]json.RawMessage, keys ...string) bool {
	if len(fields) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := fields[key]; !ok {
			return false
		}
	}
	return true
}
