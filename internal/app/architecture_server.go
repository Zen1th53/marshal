package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/statepath"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Zen1th53/marshal/internal/architecturereview"
	"github.com/Zen1th53/marshal/internal/model"
)

// ArchitectureReviewSession owns a local view and its read-only model turns.
// It never starts a worker or grants plan or execution authority.
type ArchitectureReviewSession struct {
	URL    string
	Done   <-chan struct{}
	close  func() error
	accept func(context.Context, string, string, string, string, int64, string) error
}

type ArchitectureReviewOwnedError struct{ URL string }

func (e *ArchitectureReviewOwnedError) Error() string {
	return "architecture review already has a live owner; view: " + e.URL
}
func (e *ArchitectureReviewOwnedError) Unwrap() error { return ErrRuntimeOwned }

func (s *ArchitectureReviewSession) Close() error {
	if s == nil || s.close == nil {
		return nil
	}
	return s.close()
}

func (r *Runtime) StartArchitectureReview(ctx context.Context, provider, goal string) (*ArchitectureReviewSession, error) {
	return r.StartArchitectureReviewForSession(ctx, provider, goal, "")
}

// StartArchitectureReviewForSession binds browser model turns to the current
// canonical Goal even before a plan exists. It conveys no acceptance authority.
func (r *Runtime) StartArchitectureReviewForSession(ctx context.Context, provider, goal, sessionID string) (*ArchitectureReviewSession, error) {
	if r == nil || r.store == nil {
		return nil, model.ErrUnavailable
	}
	service, err := r.MarshalWired(MarshalWiring{Provider: provider, SessionID: sessionID, Approver: func(context.Context, string, string) (string, error) {
		return "", errors.New("architecture view cannot authorize execution")
	}})
	if err != nil {
		return nil, err
	}
	// The review model gets bounded source observation, never recalled facts.
	reviewModel := *service.Model.(*MarshalCLI)
	reviewModel.ProjectContext = r.MarshalArchitectureDiscoveryContext
	return r.startArchitectureReview(ctx, &reviewModel, goal)
}

func (r *Runtime) startArchitectureReview(ctx context.Context, cli *MarshalCLI, goal string) (*ArchitectureReviewSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil || cli == nil || r.layout.RuntimeDir != filepath.Join(r.layout.Root, statepath.DirName) {
		return nil, model.ErrPolicyDenied
	}
	if cli.runtime == nil {
		cli.runtime = r
	}
	if cli.ProjectID == "" {
		cli.ProjectID = r.ProjectIdentity()
	}
	if cli.Dir == "" {
		cli.Dir = r.ProjectRoot()
	}
	lease, err := r.AcquireArchitectureReviewLease()
	if err != nil {
		return nil, err
	}
	leased := false
	defer func() {
		if !leased {
			_ = lease.Close()
		}
	}()
	directory, err := architectureReviewDirectory(r.layout.RuntimeDir)
	if err != nil {
		return nil, err
	}
	config := architecturereview.Config{Project: r.ProjectIdentity(), ArchitectureID: "architecture-" + r.ProjectIdentity(), StateDir: directory}
	config.Builders = r.architectureBuilders()
	language, err := r.ApprovedMarshalLanguage()
	if err != nil {
		return nil, err
	}
	config.Language = language
	var architecture json.RawMessage
	reuseUI := false
	file := filepath.Join(directory, "state.json")
	info, err := os.Lstat(file)
	if err == nil {
		if goal != "" {
			return nil, errors.New("architecture already exists; submit a selected change request in its browser view")
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 128<<10 {
			return nil, model.ErrPolicyDenied
		}
		data, err := readArchitecturePrivateFile(directory, "state.json", 128<<10)
		if err != nil {
			return nil, err
		}
		if _, err = architecturereview.ValidateStoredState(data, config.Project); err != nil {
			return nil, err
		}
		var state struct {
			ArchitectureID string          `json:"architecture_id"`
			Architecture   json.RawMessage `json:"architecture"`
			Language       string          `json:"language"`
			UI             json.RawMessage `json:"ui"`
		}
		if err = json.Unmarshal(data, &state); err != nil {
			return nil, err
		}
		config.ArchitectureID = state.ArchitectureID
		architecture = state.Architecture
		if state.Language == language && len(state.UI) > 0 {
			reuseUI = true
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	} else {
		if goal == "" {
			goal = "Derive the current imported project's architecture from its observed purpose, tracked code and interfaces. Cite evidence for every existing component and connection. Identify unknown facts explicitly; do not guess topology or implementation. Prepare the browser draft for the owner to review."
		}
		modelCtx, cancel := context.WithTimeout(ctx, 600*time.Second)
		config.Proposal, err = cli.DraftArchitecture(modelCtx, goal+"\nOwner-approved language: "+language)
		cancel()
		if err != nil {
			return nil, err
		}
		config.Layouts, err = architecturereview.GenericLayouts(config.Proposal)
		if err != nil {
			return nil, err
		}
		architecture = config.Proposal
	}
	if !reuseUI {
		modelCtx, cancel := context.WithTimeout(ctx, 600*time.Second)
		config.UI, err = cli.GenerateArchitectureUI(modelCtx, language, architecture)
		cancel()
		if err != nil {
			return nil, err
		}
	}
	serverCtx, cancel := context.WithCancel(ctx)
	config.Context = serverCtx
	var turns sync.WaitGroup
	var mu sync.Mutex
	closed := false
	config.Feedback = func(_ context.Context, envelope json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		if closed {
			mu.Unlock()
			return nil, context.Canceled
		}
		turns.Add(1)
		mu.Unlock()
		defer turns.Done()
		turnCtx, turnCancel := context.WithTimeout(serverCtx, 600*time.Second)
		defer turnCancel()
		var input map[string]json.RawMessage
		if err := json.Unmarshal(envelope, &input); err != nil {
			return nil, err
		}
		input["language"], _ = json.Marshal(language)
		encoded, marshalErr := json.Marshal(input)
		if marshalErr != nil {
			return nil, marshalErr
		}
		envelope = encoded
		response, err := cli.ArchitectureFeedback(turnCtx, envelope)
		if err != nil {
			return nil, err
		}
		if cli.MarshalConversationID() == "" {
			return nil, errors.New("architecture model returned no provider session receipt")
		}
		var result map[string]json.RawMessage
		if err = json.Unmarshal(response, &result); err != nil {
			return nil, err
		}
		result["provider"], _ = json.Marshal(cli.Provider)
		result["session_id"], _ = json.Marshal(cli.MarshalConversationID())
		return json.Marshal(result)
	}
	config.Localize = func(_ context.Context, language string, architecture json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		if closed {
			mu.Unlock()
			return nil, context.Canceled
		}
		turns.Add(1)
		mu.Unlock()
		defer turns.Done()
		turnCtx, turnCancel := context.WithTimeout(serverCtx, 600*time.Second)
		defer turnCancel()
		return cli.GenerateArchitectureUI(turnCtx, language, architecture)
	}
	review, err := architecturereview.New(config)
	if err != nil {
		cancel()
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cancel()
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		cancel()
		listener.Close()
		return nil, err
	}
	token := hex.EncodeToString(secret)
	url := "http://" + listener.Addr().String() + "/?token=" + token
	if err = writeArchitectureURL(filepath.Join(directory, "url.txt"), url); err != nil {
		cancel()
		listener.Close()
		return nil, err
	}
	// Handlers still running when Close returns would keep writing review
	// state, so each request is counted with the model turns that Close joins.
	reviewHandler := review.Handler(token)
	tracked := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		if closed {
			mu.Unlock()
			http.Error(w, "architecture review is closed", http.StatusServiceUnavailable)
			return
		}
		turns.Add(1)
		mu.Unlock()
		defer turns.Done()
		reviewHandler.ServeHTTP(w, req)
	})
	server := &http.Server{Handler: tracked, ReadHeaderTimeout: 5 * time.Second,
		BaseContext: func(net.Listener) context.Context { return serverCtx }}
	done := make(chan struct{})
	session := &ArchitectureReviewSession{URL: url, Done: done}
	session.accept = func(ctx context.Context, chat, ref, occurrence, id string, revision int64, digest string) error {
		mu.Lock()
		if closed {
			mu.Unlock()
			return context.Canceled
		}
		turns.Add(1)
		mu.Unlock()
		defer turns.Done()
		return r.acceptArchitectureByMarshal(ctx, review, chat, ref, occurrence, id, revision, digest)
	}
	var once sync.Once
	var closeErr error
	session.close = func() error {
		once.Do(func() {
			mu.Lock()
			closed = true
			cancel()
			mu.Unlock()
			closeErr = server.Close()
			_ = listener.Close()
			turns.Wait()
			review.Wait()
			_ = lease.Close()
			close(done)
		})
		return closeErr
	}
	go func() { _ = server.Serve(listener); _ = session.Close() }()
	go func() { <-serverCtx.Done(); _ = session.Close() }()
	leased = true
	return session, nil
}

// AcquireArchitectureReviewLease prevents two browser servers from replacing
// each other's canonical revision. The kernel releases the lease after a crash.
func (r *Runtime) AcquireArchitectureReviewLease() (*os.File, error) {
	if r == nil || !filepath.IsAbs(r.layout.Root) || r.layout.RuntimeDir != filepath.Join(r.layout.Root, statepath.DirName) {
		return nil, model.ErrPolicyDenied
	}
	directory, err := architectureReviewDirectory(r.layout.RuntimeDir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "runtime-owner.lock")
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0) {
		return nil, model.ErrPolicyDenied
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	lease, err := acquireRuntimeOwner(directory)
	if errors.Is(err, ErrRuntimeOwned) {
		data, readErr := readArchitecturePrivateFile(directory, "url.txt", 4096)
		if readErr == nil {
			current := strings.TrimSpace(string(data))
			location, parseErr := url.Parse(current)
			if parseErr == nil && location.Scheme == "http" && location.User == nil && location.Path == "/" && location.Fragment == "" {
				if address := net.ParseIP(location.Hostname()); address != nil && address.IsLoopback() && location.Port() != "" && len(location.Query().Get("token")) >= 32 {
					return nil, &ArchitectureReviewOwnedError{URL: current}
				}
			}
		}
		return nil, fmt.Errorf("%w; architecture generation or review already owns this project's browser state", err)
	}
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	opened, statErr := lease.Stat()
	if err != nil || statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !os.SameFile(info, opened) {
		_ = lease.Close()
		return nil, model.ErrPolicyDenied
	}
	return lease, nil
}

func architectureReviewDirectory(state string) (string, error) {
	for _, path := range []string{state, filepath.Join(state, "architecture"), filepath.Join(state, "architecture", "review")} {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			err = os.Mkdir(path, 0700)
			if err == nil {
				info, err = os.Lstat(path)
			}
		}
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
			return "", model.ErrPolicyDenied
		}
	}
	return filepath.Join(state, "architecture", "review"), nil
}

func writeArchitectureURL(path, url string) error {
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0) {
		return model.ErrPolicyDenied
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".architecture-url-*.tmp")
	if err != nil {
		return err
	}
	if err = file.Chmod(0600); err == nil {
		_, err = io.WriteString(file, url)
	}
	name := file.Name()
	defer os.Remove(name)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("persist private architecture URL: %w", err)
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}

// ApprovedMarshalLanguage reads only the owner's saved intake preference.
// English is used for an absent or blank choice; process locale is irrelevant.
func (r *Runtime) ApprovedMarshalLanguage() (string, error) {
	if r == nil || r.layout.RuntimeDir != filepath.Join(r.layout.Root, statepath.DirName) {
		return "", model.ErrPolicyDenied
	}
	data, err := readArchitecturePrivateFile(r.layout.RuntimeDir, "marshal-intake.json", 4096)
	if os.IsNotExist(err) {
		return "English", nil
	}
	if err != nil {
		return "", err
	}
	if _, err = architecturereview.CanonicalDigest(data); err != nil {
		return "", err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(data, &fields); err != nil {
		return "", err
	}
	if raw, present := fields["language"]; present && string(raw) == "null" {
		return "", errors.New("saved Marshal language must be a string")
	}
	var intake struct {
		Language    string `json:"language"`
		EarlierWork string `json:"earlier_work"`
	}
	if err = json.Unmarshal(data, &intake); err != nil {
		return "", err
	}
	language := strings.TrimSpace(intake.Language)
	if language == "" {
		return "English", nil
	}
	if len(language) > 100 || strings.ContainsFunc(language, unicode.IsControl) {
		return "", errors.New("saved Marshal language is invalid")
	}
	return language, nil
}

func readArchitecturePrivateFile(directory, name string, limit int64) ([]byte, error) {
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, model.ErrPolicyDenied
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return nil, model.ErrPolicyDenied
	}
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode().Perm()&0077 != 0 || before.Size() > limit {
		return nil, model.ErrPolicyDenied
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(before, actual) {
		return nil, model.ErrPolicyDenied
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	after, err := root.Lstat(name)
	if err != nil || !os.SameFile(before, after) || !after.Mode().IsRegular() || after.Mode().Perm()&0077 != 0 || !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() || len(data) > int(limit) {
		return nil, model.ErrPolicyDenied
	}
	return data, nil
}
