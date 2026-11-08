package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/Zen1th53/marshal/internal/statepath"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/architecturereview"
	"github.com/Zen1th53/marshal/internal/model"
)

func (c *command) architecture(ctx context.Context, args []string) error {
	action := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action, args = args[0], args[1:]
	}
	if action != "serve" && action != "draft" && action != "feedback" {
		return fmt.Errorf("%w: architecture serve | draft | feedback", model.ErrInvalid)
	}
	flags := flag.NewFlagSet("architecture "+action, flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	provider := flags.String("provider", "codex", "Marshal provider")
	proposalFile := flags.String("proposal", "", "existing architecture JSON")
	layoutFile := flags.String("layout", "", "explicit diagram geometry JSON")
	request := flags.String("request", "", "owner architecture request")
	output := flags.String("output", "", "draft JSON output path")
	listen := flags.String("listen", "127.0.0.1:0", "loopback address")
	noOpen := flags.Bool("no-open", false, "do not open the local browser")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%w: unexpected architecture arguments", model.ErrInvalid)
	}
	var input []byte
	var err error
	if action == "feedback" {
		input, err = io.ReadAll(io.LimitReader(c.stdin, 1_000_001))
		if err != nil || len(input) > 1_000_000 || !json.Valid(input) {
			return fmt.Errorf("%w: invalid architecture feedback input", model.ErrInvalid)
		}
	}
	root, err := c.projectRoot(ctx)
	if err != nil {
		return err
	}
	rt, err := app.Open(ctx, root)
	if err != nil {
		return err
	}
	defer rt.Close()
	control, err := rt.OpenLocalControl(ctx)
	if err != nil {
		return err
	}
	ctx = control.Context(ctx)
	service, err := rt.MarshalWired(app.MarshalWiring{Provider: *provider, Approver: func(context.Context, string, string) (string, error) {
		return "", errors.New("architecture review does not approve execution")
	}})
	if err != nil {
		return err
	}
	marshalModel, ok := service.Model.(*app.MarshalCLI)
	if !ok {
		return model.ErrUnavailable
	}
	marshalModel.ProjectContext = rt.MarshalArchitectureDiscoveryContext
	language, err := rt.ApprovedMarshalLanguage()
	if err != nil {
		return err
	}
	feedback := func(parent context.Context, raw json.RawMessage) (json.RawMessage, error) {
		turn, cancel := context.WithTimeout(parent, 600*time.Second)
		defer cancel()
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, err
		}
		envelope["language"], _ = json.Marshal(language)
		encoded, err := json.Marshal(envelope)
		if err != nil {
			return nil, err
		}
		result, err := marshalModel.ArchitectureFeedback(turn, encoded)
		if err != nil {
			return nil, err
		}
		var receipt map[string]any
		if err := json.Unmarshal(result, &receipt); err != nil {
			return nil, err
		}
		receipt["provider"], receipt["session_id"] = *provider, marshalModel.MarshalConversationID()
		receipt["role"] = "marshal_architecture_feedback"
		return json.Marshal(receipt)
	}
	if action == "feedback" {
		result, err := feedback(ctx, input)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(c.stdout, string(result))
		return err
	}
	directory := filepath.Join(root, statepath.DirName, "architecture", "review")
	if action == "serve" {
		host, _, addressErr := net.SplitHostPort(*listen)
		if addressErr != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return fmt.Errorf("%w: architecture view requires a loopback listen address", model.ErrInvalid)
		}
		lease, err := rt.AcquireArchitectureReviewLease()
		if err != nil {
			return err
		}
		defer lease.Close()
	}
	var proposal json.RawMessage
	if *proposalFile != "" {
		proposal, err = readArchitectureFile(*proposalFile)
		if err != nil {
			return err
		}
	} else if _, statErr := os.Stat(filepath.Join(directory, "state.json")); action == "draft" || errors.Is(statErr, os.ErrNotExist) {
		goal := strings.TrimSpace(*request)
		if goal == "" {
			goal = "Derive the project's current architecture from the current source, project purpose and saved memory for browser review. Distinguish observed code from proposals. Unknown facts must remain explicit unanswered questions; do not invent topology or accepted decisions."
		}
		turn, cancel := context.WithTimeout(ctx, 600*time.Second)
		proposal, err = marshalModel.DraftArchitecture(turn, goal+"\nOwner-approved interface and response language: "+language)
		cancel()
		if err != nil {
			return err
		}
	}
	if action == "draft" {
		if *output != "" {
			return os.WriteFile(*output, proposal, 0600)
		}
		_, err = fmt.Fprintln(c.stdout, string(proposal))
		return err
	}
	var geometry map[string]architecturereview.Layout
	if *layoutFile != "" {
		data, err := readArchitectureFile(*layoutFile)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &geometry); err != nil {
			return err
		}
	}
	localize := func(parent context.Context, approved string, architecture json.RawMessage) (json.RawMessage, error) {
		turn, cancel := context.WithTimeout(parent, 600*time.Second)
		defer cancel()
		return marshalModel.GenerateArchitectureUI(turn, approved, architecture)
	}
	var display json.RawMessage
	_, existingReview := os.Stat(filepath.Join(directory, "state.json"))
	if len(proposal) > 0 && errors.Is(existingReview, os.ErrNotExist) {
		display, err = localize(ctx, language, proposal)
		if err != nil {
			return err
		}
	}
	review, err := architecturereview.New(architecturereview.Config{Context: ctx, Project: rt.ProjectIdentity(),
		ArchitectureID: "architecture-" + rt.ProjectIdentity(), StateDir: directory, Proposal: proposal, Layouts: geometry,
		Feedback: feedback, Language: language, UI: display, Localize: localize})
	if err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("%w: architecture view requires a loopback listen address", model.ErrInvalid)
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	token := hex.EncodeToString(secret)
	url := "http://" + listener.Addr().String() + "/?token=" + token
	if err := os.WriteFile(filepath.Join(directory, "url.txt"), []byte(url), 0600); err != nil {
		return err
	}
	server := &http.Server{Handler: review.Handler(token), ReadHeaderTimeout: 5 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	if err := c.print(map[string]any{"url": url, "protocol_version": architecturereview.Version}, url); err != nil {
		return err
	}
	if !*noOpen {
		browser := exec.CommandContext(ctx, "xdg-open", url)
		if err := browser.Run(); err != nil {
			fmt.Fprintln(c.stderr, "Open the architecture URL in your browser.")
		}
	}
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func readArchitectureFile(path string) (json.RawMessage, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1_000_001))
	if err != nil || len(data) > 1_000_000 || !json.Valid(data) {
		return nil, fmt.Errorf("%w: invalid or oversized architecture JSON", model.ErrInvalid)
	}
	return data, nil
}
