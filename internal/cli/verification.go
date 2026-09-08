package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/verification"
)

const reviewUsage = "Usage: marshal review start SESSION.json | status VERIFICATION-ID | evaluate VERIFICATION-ID | attest VERIFICATION-ID --bundle ENVELOPE.json --provenance TEXT\n"

func (c command) review(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: %s", model.ErrInvalid, strings.TrimSpace(reviewUsage))
	}
	switch args[0] {
	case "start", "status", "evaluate", "attest":
	default:
		return fmt.Errorf("%w: %s", model.ErrInvalid, strings.TrimSpace(reviewUsage))
	}
	runtime, err := app.Open(ctx, c.root)
	if err != nil {
		return err
	}
	defer runtime.Close()
	service := runtime.Verification()
	switch args[0] {
	case "start":
		if len(args) != 2 {
			return fmt.Errorf("%w: session JSON file required", model.ErrInvalid)
		}
		raw, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		var session verification.Session
		if err := json.Unmarshal(raw, &session); err != nil {
			return fmt.Errorf("decode verification session: %w", err)
		}
		got, err := service.Start(ctx, session)
		if err != nil {
			return err
		}
		return c.print(got, fmt.Sprintf("verification=%s state=%s version=%d", got.ID, got.State, got.Version))
	case "status":
		if len(args) != 2 {
			return fmt.Errorf("%w: verification ID required", model.ErrInvalid)
		}
		got, err := service.Current(ctx, args[1])
		if err != nil {
			return err
		}
		return c.print(got, fmt.Sprintf("verification=%s state=%s version=%d", got.ID, got.State, got.Version))
	case "evaluate":
		if len(args) != 2 {
			return fmt.Errorf("%w: verification ID required", model.ErrInvalid)
		}
		got, err := service.Evaluate(ctx, args[1])
		if err != nil {
			return err
		}
		return c.print(got, fmt.Sprintf("verification=%s state=%s version=%d", got.ID, got.State, got.Version))
	case "attest":
		return c.reviewAttest(ctx, service, args[1:])
	}
	return nil
}

func (c command) reviewAttest(ctx context.Context, service *app.VerificationService, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: verification ID required", model.ErrInvalid)
	}
	id := args[0]
	fs := flag.NewFlagSet("review attest", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	var bundlePath, provenance string
	fs.StringVar(&bundlePath, "bundle", "", "tamper-evident bundle envelope JSON")
	fs.StringVar(&provenance, "provenance", "", "attestation provenance")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if bundlePath == "" || provenance == "" {
		return fmt.Errorf("%w: --bundle and --provenance are required", model.ErrInvalid)
	}
	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		return err
	}
	var envelope verification.BundleEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("decode evidence bundle: %w", err)
	}
	got, err := service.Attest(ctx, id, envelope, provenance)
	if err != nil {
		return err
	}
	return c.print(got, fmt.Sprintf("attestation=%s decision=%s digest=%s", got.ID, got.Decision, got.Digest))
}
