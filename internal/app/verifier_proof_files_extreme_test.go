//go:build !governed

package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/verification"
)

func largeRuntimeProof(checks int) *verification.RuntimeProof {
	var diff strings.Builder
	for i := 0; i < 74; i++ {
		fmt.Fprintf(&diff, "diff --git a/knowledge/f%d.md b/knowledge/f%d.md\nnew file mode 100644\n--- /dev/null\n+++ b/knowledge/f%d.md\n@@ -0,0 +1,40 @@\n", i, i, i)
		for j := 0; j < 40; j++ {
			fmt.Fprintf(&diff, "+- F%d: a finding line long enough to resemble the real findings (internal/app/file.go:%d)\n", j, j)
		}
	}
	proof := &verification.RuntimeProof{Head: strings.Repeat("a", 40), Base: strings.Repeat("b", 40), Tree: strings.Repeat("c", 40), Diff: diff.String(), Provenance: "runtime"}
	for i := 0; i < checks; i++ {
		proof.Checks = append(proof.Checks, verification.RuntimeCheckProof{ID: fmt.Sprintf("check-%d", i), Task: fmt.Sprintf("task-%d", i), Head: proof.Head, Tree: proof.Tree, Command: "python3 -I -c '" + strings.Repeat("x", 420) + fmt.Sprintf("' knowledge/f%d.md", i), Output: strings.Repeat("ok line\n", 300), Criteria: []string{"criterion text " + strings.Repeat("y", 200)}})
	}
	return proof
}

// The full proof and diff are read-only files named with their sha256; the
// prompt input stays within its budget for a large plan, every check points
// at its entry in the proof file, and a changed file is refused.
func TestVerifierProofFilesCarryTheFullProofAndAreHashChecked(t *testing.T) {
	files, err := newVerifierProofFiles()
	if err != nil {
		t.Fatal(err)
	}
	defer files.cleanup()
	if strings.HasPrefix(filepath.Base(files.dir), "marshal") || outsideReadExcluded(files.dir, "", "") {
		t.Fatalf("proof directory %s cannot be read by a role turn", files.dir)
	}
	proof := largeRuntimeProof(74)
	if err := files.writeProof(proof); err != nil {
		t.Fatal(err)
	}
	proofFile := files.files[verifierProofFileName]
	raw, err := os.ReadFile(proofFile.Path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	for _, check := range proof.Checks {
		line := files.checkLine[check.ID]
		if line < 1 || !strings.Contains(lines[line-1], `"ID": "`+check.ID+`"`) {
			t.Fatalf("check %s proof_line %d does not start its entry", check.ID, line)
		}
	}
	if diff, err := os.ReadFile(files.files[verifierDiffFileName].Path); err != nil || string(diff) != proof.Diff {
		t.Fatal("diff file differs from the proof's diff")
	}
	if info, err := os.Stat(proofFile.Path); err != nil || info.Mode().Perm() != 0o400 {
		t.Fatalf("proof file is not read-only: %v", err)
	}
	criteria := []requiredVerifierCriterion{{ID: "C1", Text: "every findings file resolves", CheckIDs: []string{"check-0"}}}
	input, err := files.compactVerifierInput(proof.Head, criteria, proof)
	if err != nil {
		t.Fatal(err)
	}
	if len(input) > verifierPromptBudget || !strings.Contains(string(input), proofFile.SHA256) || !strings.Contains(string(input), `"proof_line"`) || !strings.Contains(string(input), `"path":"knowledge/f73.md","added":40`) {
		t.Fatalf("compact input: %d bytes", len(input))
	}
	if strings.Contains(string(input), strings.Repeat("ok line\n", 50)) || strings.Contains(string(input), "+- F39") {
		t.Fatal("full check output or diff leaked into the prompt")
	}
	if err := files.verify(); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(proofFile.Path, 0o600)
	if err := os.WriteFile(proofFile.Path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := files.verify(); !errors.Is(err, errVerifierProofFile) {
		t.Fatalf("a changed proof file was accepted: %v", err)
	}
	dir := files.dir
	files.cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("proof directory was not removed")
	}
}

// Every assessment must cite an existing line of the proof file or the diff.
func TestVerifierCitationsMustNameExistingProofLines(t *testing.T) {
	files, err := newVerifierProofFiles()
	if err != nil {
		t.Fatal(err)
	}
	defer files.cleanup()
	if err := files.writeProof(largeRuntimeProof(2)); err != nil {
		t.Fatal(err)
	}
	result := func(reason string) requiredVerifierResult {
		return requiredVerifierResult{Assessments: []requiredVerifierAssessment{{ID: "C1", Status: verification.StatusPass, Reason: reason}}}
	}
	if err := files.validateCitations(result("check-0 exits 0 (runtime-proof.json:12) and adds the file (diff.patch:3)")); err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{"the checks pass", "see runtime-proof.json:999999", "see diff.patch:0"} {
		if err := files.validateCitations(result(reason)); !errors.Is(err, errRequiredVerifierOutput) {
			t.Fatalf("reason %q was accepted: %v", reason, err)
		}
	}
}

func TestDiffStatCountsEachFile(t *testing.T) {
	stat := diffStat("diff --git a/x.md b/x.md\n--- a/x.md\n+++ b/x.md\n@@\n-old\n+new\n+more\ndiff --git a/y.md b/y.md\n--- /dev/null\n+++ b/y.md\n+one\n")
	if len(stat) != 2 || stat[0] != (verifierDiffStat{"x.md", 2, 1}) || stat[1] != (verifierDiffStat{"y.md", 1, 0}) {
		t.Fatalf("diff stat %+v", stat)
	}
}

// The independent verifier may read its proof files with read-only tools.
func TestRequiredVerifierTurnMayReadFiles(t *testing.T) {
	if !verifierSchemaReadsFiles(requiredVerifierSchema) || !verifierSchemaReadsFiles(marshalVerifySchema) || verifierSchemaReadsFiles(marshalReviewSchema) {
		t.Fatal("verifier read permission differs")
	}
}

// The architecture review is told to cite only offered references or
// workspace-relative lines, never the input file it reads.
func TestArchitectureInputInstructionNamesAcceptedEvidence(t *testing.T) {
	for _, want := range []string{"never cite that file", "copied exactly from EvidenceRefs", "workspace-relative path:line", "absolute path"} {
		if !strings.Contains(architectureInputFileInstruction, want) {
			t.Fatalf("instruction lacks %q", want)
		}
	}
}

// Reasons are bounded where the verdict is accepted (1024 for the verifier,
// 2048 for the architecture review); the schemas and instructions say so.
func TestReviewSchemasBoundReasonLength(t *testing.T) {
	if !strings.Contains(requiredVerifierSchema, `"reason":{"type":"string","maxLength":1000}`) || !strings.Contains(verifierProofFilesInstruction, "within 1000 characters") {
		t.Fatal("verifier reason is not bounded below its 1024-character acceptance limit")
	}
	if !strings.Contains(requiredArchitectureSchema, `"reason":{"type":"string","maxLength":2000}`) || !strings.Contains(architectureInputFileInstruction, "within 2000 characters") {
		t.Fatal("architecture reason is not bounded below its 2048-character acceptance limit")
	}
}

// A limit the integrated result honours is PASS, never N/A: the review is told
// how to judge constraints, explicit limits and do-not-do items.
func TestArchitectureRuleJudgesHonouredLimitsAsPass(t *testing.T) {
	for _, want := range []string{"explicit limit", "PASS when the integrated result does not violate it", "FAIL when the result violates it", "a limit the result honours is never N/A"} {
		if !strings.Contains(requiredArchitectureProcessRule, want) {
			t.Fatalf("rule lacks %q", want)
		}
	}
}
