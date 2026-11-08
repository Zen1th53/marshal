//go:build !governed

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/artifact"
	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/verification"
)

// marshalProofLimit bounds a runtime Git observation and the whole proof. The
// independent verifier reads the full proof and diff from read-only files,
// not its prompt, so a large plan's diff (r57: 360 KB) is admitted; a check's
// own output keeps its separate bound.
const marshalProofLimit = 8 << 20
const marshalProofCheckLimit = 8 << 10

// Complete check output has its own artifact bound; previews remain 8 KiB.
const marshalProofOutputLimit = 8 << 20

type marshalProofWriter struct {
	buffer bytes.Buffer
	limit  int
	cancel context.CancelFunc
}

func (w *marshalProofWriter) Write(b []byte) (int, error) {
	if len(b) > w.limit-w.buffer.Len() {
		w.cancel()
		return 0, errors.New("runtime verifier proof exceeds bound")
	}
	return w.buffer.Write(b)
}
func runtimeProofGit(ctx context.Context, dir string, args ...string) (string, error) {
	if !runtimeProofGitArgs(args) {
		return "", errors.New("runtime verifier Git query not admitted")
	}
	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd, err := hostgit.Command(readCtx, dir, args...)
	if err != nil {
		return "", err
	}
	stdout := &marshalProofWriter{limit: marshalProofLimit, cancel: cancel}
	stderr := &marshalProofWriter{limit: marshalProofCheckLimit, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err = cmd.Run(); err != nil {
		return "", fmt.Errorf("runtime verifier git observation failed: %w", err)
	}
	data := stdout.buffer.Bytes()
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", errors.New("runtime verifier git observation is not text")
	}
	return string(data), nil
}
func fullProofObject(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}
func proofCurrent(ctx context.Context, dir, head, tree string) error {
	for _, q := range []struct {
		args []string
		want string
	}{{[]string{"rev-parse", "HEAD"}, head}, {[]string{"rev-parse", "HEAD^{tree}"}, tree}} {
		got, err := runtimeProofGit(ctx, dir, q.args...)
		if err != nil {
			return err
		}
		if strings.TrimSpace(got) != q.want {
			return errors.New("runtime verifier checkout changed or is dirty")
		}
	}
	status, err := runtimeProofGit(ctx, dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return err
	}
	if len(hostgit.SourceStatusChanges(status)) != 0 {
		return errors.New("runtime verifier checkout changed or is dirty")
	}
	return nil
}
func (s *MarshalService) beginMarshalRuntimeProof(ctx context.Context, dir string, run marshal.Run, head, tree string, governed bool) (*verification.RuntimeProof, error) {
	if !governed {
		return nil, nil
	}
	if !fullProofObject(head) || !fullProofObject(run.BaseCommit) || !fullProofObject(tree) {
		return nil, errors.New("runtime verifier requires full canonical base/head/tree")
	}
	if err := proofCurrent(ctx, dir, head, tree); err != nil {
		return nil, err
	}
	base, err := runtimeProofGit(ctx, dir, "rev-parse", run.BaseCommit+"^{commit}")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(base) != run.BaseCommit {
		return nil, errors.New("runtime verifier base mismatch")
	}
	diff, err := runtimeProofGit(ctx, dir, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--no-color", run.BaseCommit, head, "--")
	if err != nil {
		return nil, err
	}
	if strings.Contains(diff, "GIT binary patch") || strings.Contains(diff, "Binary files ") {
		return nil, errors.New("binary runtime verifier diff is unsupported")
	}
	if err := proofCurrent(ctx, dir, head, tree); err != nil {
		return nil, err
	}
	proof := &verification.RuntimeProof{Head: head, Base: run.BaseCommit, Tree: tree, Diff: diff, Provenance: "Runtime-observed hardened Git snapshot and integration check reruns. Isolation is recorded per check; Marshal runs on the host with process-only isolation. Not worker claims or independently executed model commands."}
	observations, err := s.runtimeProviderObservationProof(ctx, run)
	if err != nil {
		return nil, err
	}
	proof.ProviderObservations = observations
	if err := s.addRuntimeHoneypotProof(ctx, run, proof); err != nil {
		return nil, err
	}
	return proof, nil
}
func (s *MarshalService) appendMarshalRuntimeCheck(ctx context.Context, proof *verification.RuntimeProof, id string, task marshal.Task, c marshal.Check, record *marshal.CommandRecord, checkErr error) error {
	if proof == nil {
		return nil
	}
	if record == nil || record.Command != c.Command || len(record.Output) > marshalProofOutputLimit || !utf8.ValidString(record.Output) || strings.IndexByte(record.Output, 0) >= 0 || strings.Contains(record.Output, "[truncated:") {
		return fmt.Errorf("runtime verifier check %s: observation missing or malformed (truncated or exceeds %d-byte artifact bound)", id, marshalProofOutputLimit)
	}
	if record.ExitCode == 0 && checkErr != nil {
		return errors.New("runtime verifier check changed checkout or was refused")
	}
	for _, prior := range proof.Checks {
		if prior.ID == id {
			return errors.New("duplicate runtime verifier check ID")
		}
	}
	if s.Store == nil {
		return errors.New("runtime verifier output artifact store missing")
	}
	root := filepath.Join(filepath.Dir(s.Worktrees), "artifacts")
	if s.runtime != nil {
		root = s.runtime.layout.Artifacts
	}
	retained, err := artifact.New(root, s.Store).Put(ctx, model.ArtifactInput{ProjectID: s.ProjectID, Kind: "report", SourceCommit: proof.Head, VerificationRefs: []string{task.Branch, id}, Data: strings.NewReader(record.Output)})
	if err != nil {
		// Artifact digests are unique in the store. Identical output from
		// another check or verification attempt reuses the immutable bytes;
		// this proof, rather than the blob's first source commit, binds the
		// new observation to its exact head, tree, command and check ID.
		existing, lookupErr := s.Store.ListArtifacts(ctx)
		if lookupErr != nil {
			return errors.Join(err, lookupErr)
		}
		sum := sha256.Sum256([]byte(record.Output))
		digest := "sha256:" + hex.EncodeToString(sum[:])
		found := false
		for _, item := range existing {
			if item.ProjectID == s.ProjectID && item.Digest == digest && item.Size == int64(len(record.Output)) {
				retained, found = item, true
				break
			}
		}
		if !found {
			return fmt.Errorf("runtime verifier check %s output artifact: %w", id, err)
		}
	}
	// Extreme's production governed runner always executes directly on the host.
	// A custom runner without a runtime binding supplies no isolation observation.
	isolation := "unknown"
	if s.runtime != nil {
		isolation = "host-process-only"
	}
	proof.Checks = append(proof.Checks, verification.RuntimeCheckProof{ID: id, Task: task.PlanTaskID, Head: proof.Head, Tree: proof.Tree, Command: record.Command, Output: clip(record.Output, marshalProofCheckLimit, false), OutputArtifact: &verification.RuntimeCheckOutput{ID: retained.ID, Digest: retained.Digest, Path: retained.Path, Size: retained.Size}, Isolation: isolation, ExitCode: record.ExitCode, Criteria: append([]string(nil), c.Criteria...)})
	return nil
}
func finishMarshalRuntimeProof(ctx context.Context, dir string, proof *verification.RuntimeProof, checks map[string]verification.Status) error {
	if proof == nil {
		return nil
	}
	if len(proof.Checks) != len(checks) || len(checks) == 0 {
		return errors.New("incomplete runtime verifier check proof")
	}
	for _, record := range proof.Checks {
		status, ok := checks[record.ID]
		if !ok || (record.ExitCode == 0) != (status == verification.StatusPass) {
			return errors.New("runtime verifier check proof disagrees with status")
		}
	}
	data, err := json.Marshal(proof)
	if err != nil || len(data) > marshalProofLimit {
		return errors.New("runtime verifier proof exceeds bound")
	}
	return proofCurrent(ctx, dir, proof.Head, proof.Tree)
}

func marshalRuntimeProofInstruction(session verification.Session) string {
	if session.RuntimeProof == nil {
		return ""
	}
	return "The supplied Session.runtime_proof contains runtime-observed full HEAD/base/tree, base-to-integrated diff, exact integration check reruns with per-check isolation observations (Marshal executes on the host) retained security scan dispositions and exact hand-in provider lifecycle observations. Provider process/native lifecycle intervals include setup, tools and approval waits; paid inference intervals are not recorded. Independently assess those supplied observations against task criteria and the exact commit; do not claim you executed Git/checks or rescanned traps yourself. You may read current in-worktree files for corroboration. Return the full supplied head, never a short prefix. Fail if observations are insufficient or inconsistent; no evidence grants execution authority. "
}
func (s *MarshalService) validateVerifierRuntimeProof(ctx context.Context, run marshal.Run, head string, session verification.Session) error {
	p := session.RuntimeProof
	if p == nil {
		return nil
	}
	if p.Head != head || p.Base != run.BaseCommit || p.Tree != session.Binding.TreeDigest {
		return errors.New("runtime verifier proof binding mismatch")
	}
	if err := proofCurrent(ctx, filepath.Join(s.Worktrees, integrationTaskID(marshalRunID(run), run)), p.Head, p.Tree); err != nil {
		return err
	}
	observations, err := s.runtimeProviderObservationProof(ctx, run)
	if err != nil {
		return err
	}
	current, _ := json.Marshal(observations)
	retained, _ := json.Marshal(p.ProviderObservations)
	if !bytes.Equal(current, retained) {
		return errors.New("runtime verifier provider observations changed")
	}
	for _, check := range p.Checks {
		ref := check.OutputArtifact
		if ref == nil {
			return errors.New("runtime verifier output artifact missing; rerun verification")
		}
		retained, err := s.Store.GetArtifact(ctx, ref.ID)
		if err != nil {
			return err
		}
		if retained.ProjectID != s.ProjectID || retained.Digest != ref.Digest || retained.Path != ref.Path || retained.Size != ref.Size {
			return errors.New("runtime verifier output artifact binding mismatch")
		}
		if _, err := readRuntimeCheckOutput(check); err != nil {
			return err
		}
	}
	for _, task := range run.Tasks {
		if task.HoneypotRequired || task.Mode == marshal.Governed && s.HandInGuard != nil {
			if err := s.checkHoneypotAdmission(ctx, task, task.ResultCommit); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *MarshalService) addRuntimeHoneypotProof(ctx context.Context, run marshal.Run, proof *verification.RuntimeProof) error {
	for _, task := range run.Tasks {
		if !task.HoneypotRequired && !(task.Mode == marshal.Governed && s.HandInGuard != nil) {
			continue
		}
		if s.Store == nil {
			return errors.New("durable honeypot proof store missing")
		}
		if err := s.checkHoneypotAdmission(ctx, task, task.ResultCommit); err != nil {
			return err
		}
		scans, err := s.Store.HoneypotScanProofs(ctx, task.HoneypotScanID, task.ResultCommit)
		if err != nil {
			return err
		}
		project, err := s.Store.Project(ctx)
		if err != nil {
			return err
		}
		found := false
		for _, scan := range scans {
			if scan.Outcome != "clean" {
				continue
			}
			if scan.EventID == "" || (scan.TaskID != task.PlanTaskID && scan.TaskID != canonicalPlanTaskID(run.PlanID, run.PlanVersion, task.PlanTaskID)) || scan.ProjectID != project.ID || scan.ScanID != task.HoneypotScanID || scan.ResultCommit != task.ResultCommit {
				return errors.New("durable honeypot proof mismatch")
			}
			proof.Honeypot = append(proof.Honeypot, verification.RuntimeHoneypotProof{Task: task.PlanTaskID, EventID: scan.EventID, ProjectID: scan.ProjectID, SourceTaskID: scan.TaskID, ScanID: scan.ScanID, ResultCommit: scan.ResultCommit, Outcome: scan.Outcome, Timestamp: scan.Timestamp})
			found = true
			break
		}
		if !found {
			return errors.New("durable clean honeypot proof missing")
		}
	}
	return nil
}

func runtimeProofGitArgs(args []string) bool {
	if len(args) == 4 && args[0] == "merge-base" && args[1] == "--is-ancestor" {
		return fullProofObject(args[2]) && fullProofObject(args[3])
	}
	if len(args) == 2 && args[0] == "rev-parse" {
		v := args[1]
		return v == "HEAD" || v == "HEAD^{tree}" || strings.HasSuffix(v, "^{commit}") && fullProofObject(strings.TrimSuffix(v, "^{commit}"))
	}
	if len(args) == 4 && args[0] == "status" {
		return args[1] == "--porcelain" && args[2] == "--untracked-files=all" && args[3] == "--ignored"
	}
	if len(args) == 3 && args[0] == "status" {
		return args[1] == "--porcelain" && args[2] == "--untracked-files=all"
	}
	return len(args) == 8 && args[0] == "diff" && args[1] == "--no-ext-diff" && args[2] == "--no-textconv" && args[3] == "--no-renames" && args[4] == "--no-color" && fullProofObject(args[5]) && fullProofObject(args[6]) && args[7] == "--"
}

// verifierSchemaReadsFiles reports a verification turn whose provider may use
// read-only tools to inspect the repository and its evidence files, including
// the independent verifier's read-only proof files.
func verifierSchemaReadsFiles(schema string) bool {
	return schema == marshalVerifySchema || schema == requiredVerifierSchema
}

// readRuntimeCheckOutput checks the complete artifact before a verifier reads it.
func readRuntimeCheckOutput(check verification.RuntimeCheckProof) ([]byte, error) {
	ref := check.OutputArtifact
	if ref == nil {
		return []byte(check.Output), nil
	} // historical inline evidence
	if ref.Size < 0 || ref.Size > marshalProofOutputLimit {
		return nil, errors.New("runtime verifier output artifact size invalid")
	}
	file, err := os.Open(ref.Path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, marshalProofOutputLimit+1))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != ref.Size || "sha256:"+hex.EncodeToString(sum[:]) != ref.Digest {
		return nil, errors.New("runtime verifier output artifact changed")
	}
	return data, nil
}
