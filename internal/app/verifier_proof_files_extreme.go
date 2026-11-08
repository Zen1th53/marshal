//go:build !governed

package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/verification"
)

// The independent verifier receives the complete runtime observations as
// read-only files rather than inside its bounded prompt: the full
// RuntimeProof JSON (bounded previews, complete-output artifact references
// and provider lifecycle observations), complete outputs, and the base-to-HEAD diff. The prompt names each file with
// its sha256; the runtime re-hashes every file after the turn and refuses a
// changed one. The prompt itself keeps the criteria, a diff stat and a short
// record of every check that points at its full entry in the proof file.
const (
	verifierProofFileName     = "runtime-proof.json"
	verifierDiffFileName      = "diff.patch"
	verifierChecksFileName    = "approved-checks.json"
	architectureInputFileName = "architecture-input.json"
	// verifierPromptBudget leaves the rest of the required-context envelope
	// for the contract, Goal, binding and memory records.
	verifierPromptBudget = 40 << 10
)

var errVerifierProofFile = errors.New("verifier proof file")

type verifierProofFile struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
	Lines  int    `json:"lines"`
}

// verifierProofFiles is a private read-only directory outside the project and
// its worktrees (a verifier must not dirty the integrated checkout).
type verifierProofFiles struct {
	dir       string
	files     map[string]verifierProofFile
	checkLine map[string]int
}

func newVerifierProofFiles() (*verifierProofFiles, error) {
	// The directory name must not start with "marshal": MARSHAL's own
	// temporary state is never offered to a role turn as an outside read.
	dir, err := os.MkdirTemp("", "verifier-proof-")
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return &verifierProofFiles{dir: dir, files: map[string]verifierProofFile{}, checkLine: map[string]int{}}, nil
}

func (f *verifierProofFiles) write(name string, data []byte) (verifierProofFile, error) {
	path := filepath.Join(f.dir, name)
	if err := os.WriteFile(path, data, 0o400); err != nil {
		return verifierProofFile{}, err
	}
	sum := sha256.Sum256(data)
	file := verifierProofFile{Name: name, Path: path, SHA256: "sha256:" + hex.EncodeToString(sum[:]), Bytes: len(data), Lines: bytes.Count(data, []byte("\n")) + 1}
	f.files[name] = file
	return file, nil
}

// writeProof writes the full proof JSON (one field per line) and the diff, and
// records the line each check's entry starts on.
func (f *verifierProofFiles) writeProof(proof *verification.RuntimeProof) error {
	if proof == nil {
		return fmt.Errorf("%w: runtime proof is missing", errVerifierProofFile)
	}
	raw, err := json.MarshalIndent(proof, "", "  ")
	if err != nil {
		return err
	}
	for _, check := range proof.Checks {
		if check.OutputArtifact == nil {
			continue
		}
		data, err := readRuntimeCheckOutput(check)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(string(data))
		if err != nil {
			return err
		}
		name := runtimeCheckOutputName(check)
		if _, exists := f.files[name]; exists {
			continue // Identical outputs share one content-addressed proof file.
		}
		if _, err = f.write(name, raw); err != nil {
			return err
		}
	}
	lines := strings.Split(string(raw), "\n")
	for _, check := range proof.Checks {
		key, _ := json.Marshal(check.ID)
		needle := `"ID": ` + string(key) + `,`
		for i, line := range lines {
			if strings.TrimSpace(line) == needle {
				f.checkLine[check.ID] = i + 1
				break
			}
		}
		if f.checkLine[check.ID] == 0 {
			return fmt.Errorf("%w: check %s has no entry", errVerifierProofFile, check.ID)
		}
	}
	if _, err = f.write(verifierProofFileName, append(raw, '\n')); err != nil {
		return err
	}
	_, err = f.write(verifierDiffFileName, []byte(proof.Diff))
	return err
}

// verify re-hashes every written file; a changed or missing file is refused.
func (f *verifierProofFiles) verify() error {
	for name, file := range f.files {
		data, err := os.ReadFile(file.Path)
		if err != nil {
			return fmt.Errorf("%w: %s: %v", errVerifierProofFile, name, err)
		}
		sum := sha256.Sum256(data)
		if "sha256:"+hex.EncodeToString(sum[:]) != file.SHA256 {
			return fmt.Errorf("%w: %s changed during the turn", errVerifierProofFile, name)
		}
	}
	return nil
}

func (f *verifierProofFiles) cleanup() {
	if f != nil && f.dir != "" {
		_ = filepath.WalkDir(f.dir, func(path string, _ os.DirEntry, _ error) error {
			_ = os.Chmod(path, 0o700)
			return nil
		})
		_ = os.RemoveAll(f.dir)
	}
}

type verifierDiffStat struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// diffStat lists each changed file with its added and removed line counts.
func diffStat(diff string) []verifierDiffStat {
	var stats []verifierDiffStat
	var current *verifierDiffStat
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			path := line[len("diff --git "):]
			if i := strings.LastIndex(path, " b/"); i >= 0 {
				path = path[i+3:]
			}
			stats = append(stats, verifierDiffStat{Path: path})
			current = &stats[len(stats)-1]
		case current == nil || strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "--- "):
		case strings.HasPrefix(line, "+"):
			current.Added++
		case strings.HasPrefix(line, "-"):
			current.Removed++
		}
	}
	return stats
}

type verifierCheckSummary struct {
	ID         string `json:"id"`
	Task       string `json:"task"`
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	OutputHead string `json:"output_head,omitempty"`
	OutputTail string `json:"output_tail,omitempty"`
	Output     int    `json:"output_bytes"`
	ProofLine  int    `json:"proof_line"`
}

func clip(text string, n int, tail bool) string {
	if n <= 0 || text == "" {
		return ""
	}
	if len(text) <= n {
		return text
	}
	if tail {
		return strings.ToValidUTF8(text[len(text)-n:], "")
	}
	return strings.ToValidUTF8(text[:n], "")
}

// compactVerifierInput is the verifier prompt's data: criteria, the diff stat,
// the proof files and a short record of every check. Output excerpts and long
// commands shrink until the input fits verifierPromptBudget; the full text of
// each is in the proof file at the named line.
func (f *verifierProofFiles) compactVerifierInput(head string, criteria []requiredVerifierCriterion, proof *verification.RuntimeProof) ([]byte, error) {
	stat := diffStat(proof.Diff)
	files := []verifierProofFile{f.files[verifierProofFileName], f.files[verifierDiffFileName]}
	var names []string
	for name := range f.files {
		if strings.HasPrefix(name, "check-output-") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		files = append(files, f.files[name])
	}
	for _, budget := range []struct{ output, command int }{{160, 400}, {80, 240}, {40, 160}, {0, 120}} {
		checks := make([]verifierCheckSummary, 0, len(proof.Checks))
		for _, check := range proof.Checks {
			command := check.Command
			if len(command) > budget.command {
				command = clip(command, budget.command/2, false) + " … " + clip(command, budget.command/2, true)
			}
			outputBytes := len(check.Output)
			if check.OutputArtifact != nil {
				outputBytes = int(check.OutputArtifact.Size)
			}
			checks = append(checks, verifierCheckSummary{ID: check.ID, Task: check.Task, Command: command, ExitCode: check.ExitCode, OutputHead: clip(check.Output, budget.output, false), OutputTail: clip(check.Output, budget.output, true), Output: outputBytes, ProofLine: f.checkLine[check.ID]})
		}
		input, err := json.Marshal(struct {
			Head                 string                      `json:"head"`
			Base                 string                      `json:"base"`
			Tree                 string                      `json:"tree"`
			Criteria             []requiredVerifierCriterion `json:"criteria"`
			ProofFiles           []verifierProofFile         `json:"proof_files"`
			DiffStat             []verifierDiffStat          `json:"diff_stat"`
			Checks               []verifierCheckSummary      `json:"checks"`
			ProviderObservations int                         `json:"provider_observations"`
			HoneypotScans        int                         `json:"honeypot_scans"`
		}{head, proof.Base, proof.Tree, criteria, files, stat, checks, len(proof.ProviderObservations), len(proof.Honeypot)})
		if err != nil {
			return nil, err
		}
		if len(input) <= verifierPromptBudget {
			return input, nil
		}
	}
	return nil, fmt.Errorf("%w: verifier summary of %d checks exceeds %d bytes", errVerifierProofFile, len(proof.Checks), verifierPromptBudget)
}

var verifierCitationPattern = regexp.MustCompile(`(` + regexp.QuoteMeta(verifierProofFileName) + `|` + regexp.QuoteMeta(verifierDiffFileName) + `):([0-9]+)`)

// validateCitations requires every assessment's reason to cite at least one
// exact line of the proof file or the diff that exists.
func (f *verifierProofFiles) validateCitations(result requiredVerifierResult) error {
	for _, a := range result.Assessments {
		cited := false
		for _, match := range verifierCitationPattern.FindAllStringSubmatch(a.Reason, -1) {
			line, err := strconv.Atoi(match[2])
			file, ok := f.files[match[1]]
			if err != nil || !ok || line < 1 || line > file.Lines {
				return fmt.Errorf("%w: assessment %q cites %s, which does not exist", errRequiredVerifierOutput, a.ID, match[0])
			}
			cited = true
		}
		if !cited {
			return fmt.Errorf("%w: assessment %q cites no line of %s or %s", errRequiredVerifierOutput, a.ID, verifierProofFileName, verifierDiffFileName)
		}
	}
	return nil
}

const verifierProofFilesInstruction = "The complete runtime observations are in the read-only files listed in proof_files, each named with its sha256: " + verifierProofFileName + " is the full runtime proof (full HEAD/base/tree, every integration check rerun with its observed isolation and bounded output preview; the check-output files contain complete outputs bound by digest, security scan dispositions and hand-in provider lifecycle observations) and " + verifierDiffFileName + " is the full base-to-HEAD diff. Read them; the checks list below is a short record of each check and its proof_line in " + verifierProofFileName + ". In every reason cite the exact line(s) your judgement rests on as " + verifierProofFileName + ":<line> or " + verifierDiffFileName + ":<line>; an assessment without such a citation is refused. Keep each reason within 1000 characters: cite lines rather than quoting them. "

type architectureTaskSummary struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Paths []string `json:"paths"`
}

const architectureInputFileInstruction = "The complete runtime input (the verified session with its full runtime proof including every check output and the base-to-HEAD diff, the evidence bundle and every approved task with its instructions) is in the read-only file named in input_file with its sha256; read it for evidence, but never cite that file or its path. The input below is a compact view of it. Every assessment's evidence_refs must contain at least one entry copied exactly from EvidenceRefs, or a workspace-relative path:line of the integrated source (for example knowledge/memory-poc/INDEX.md:3); an absolute path or an input-file line is not evidence and the assessment is refused. Keep each reason within 2000 characters. "

// compactArchitectureInput writes the architecture review's complete input to
// a read-only file and returns the prompt's compact view: identity, the file,
// each task's id, title and paths, and the obligations and evidence refs to
// assess (moved to the file only when they alone exceed the budget).
func (f *verifierProofFiles) compactArchitectureInput(head, tree, contract string, tasks []plan.Task, obligations, evidenceRefs []string, full []byte) ([]byte, error) {
	file, err := f.write(architectureInputFileName, append(full, '\n'))
	if err != nil {
		return nil, err
	}
	summaries := make([]architectureTaskSummary, 0, len(tasks))
	for _, task := range tasks {
		summaries = append(summaries, architectureTaskSummary{ID: task.ID, Title: task.Title, Paths: task.Paths})
	}
	type view struct {
		Head           string                    `json:"Head"`
		Tree           string                    `json:"Tree"`
		ContractDigest string                    `json:"ContractDigest"`
		InputFile      verifierProofFile         `json:"input_file"`
		Tasks          []architectureTaskSummary `json:"tasks"`
		Obligations    []string                  `json:"Obligations,omitempty"`
		EvidenceRefs   []string                  `json:"EvidenceRefs,omitempty"`
		InFile         string                    `json:"obligations_and_evidence_refs,omitempty"`
	}
	data, err := json.Marshal(view{head, tree, contract, file, summaries, obligations, evidenceRefs, ""})
	if err != nil || len(data) <= verifierPromptBudget {
		return data, err
	}
	data, err = json.Marshal(view{head, tree, contract, file, summaries, nil, nil, fmt.Sprintf("%d obligations and %d evidence refs are listed in the input file's Obligations and EvidenceRefs; assess every one", len(obligations), len(evidenceRefs))})
	if err != nil || len(data) <= verifierPromptBudget {
		return data, err
	}
	return nil, fmt.Errorf("%w: architecture review summary of %d tasks exceeds %d bytes", errVerifierProofFile, len(tasks), verifierPromptBudget)
}
