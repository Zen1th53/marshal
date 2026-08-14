package legal

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ExportResult struct {
	OutputPath       string    `json:"output_path"`
	SourceHEAD       string    `json:"source_head"`
	ArchiveSHA256    string    `json:"archive_sha256"`
	OverallStatus    Status    `json:"overall_status"`
	WorkingTreeClean bool      `json:"working_tree_clean"`
	ExportTime       time.Time `json:"export_time"`
}

type fileToArchive struct {
	archivePath string
	data        []byte
}

func ExportPack(ctx context.Context, repoDir string, outputPath string) (*ExportResult, error) {
	if outputPath == "" {
		return nil, fmt.Errorf("output path cannot be empty")
	}

	report, err := RunAudit(ctx, repoDir)
	if err != nil {
		return nil, fmt.Errorf("audit failed prior to export: %w", err)
	}

	headSHA := report.Source.HeadSHA
	commitTime := report.Source.CommitTime

	// HEAD race protection: verify HEAD commit has not changed
	headCheck, err := execGit(ctx, repoDir, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(string(headCheck)) != headSHA {
		return nil, fmt.Errorf("HEAD commit changed during export execution (expected %s, got %s)", headSHA, strings.TrimSpace(string(headCheck)))
	}

	var files []fileToArchive

	addBlob := func(relPath string, archiveRelPath string) {
		if data, err := ReadBlob(ctx, repoDir, headSHA, relPath); err == nil {
			files = append(files, fileToArchive{
				archivePath: filepath.Join("slaves-due-diligence", archiveRelPath),
				data:        data,
			})
		}
	}

	// Licensing files
	addBlob("LICENSE", "licensing/LICENSE")
	addBlob("LICENSING.md", "licensing/LICENSING.md")
	addBlob("COMMERCIAL-LICENSING.md", "licensing/COMMERCIAL-LICENSING.md")
	addBlob("THIRD_PARTY_NOTICES.md", "licensing/THIRD_PARTY_NOTICES.md")
	addBlob("LICENSES/Apache-2.0.txt", "licensing/Apache-2.0.txt")
	addBlob("docs/legal/LICENSE-HISTORY.md", "licensing/LICENSE-HISTORY.md")

	// Ownership & Chain-of-Title files
	addBlob("docs/legal/CHAIN-OF-TITLE.md", "ownership/CHAIN-OF-TITLE.md")
	addBlob("docs/legal/IP-PROVENANCE-AUDIT.md", "ownership/IP-PROVENANCE-AUDIT.md")
	addBlob("docs/legal/OWNER-AND-SUCCESSOR-MODEL.md", "ownership/OWNER-AND-SUCCESSOR-MODEL.md")
	addBlob("docs/legal/CONTRIBUTOR-MODEL-DECISION.md", "ownership/CONTRIBUTOR-MODEL-DECISION.md")
	addBlob("legal/INDIVIDUAL-CONTRIBUTOR-ASSIGNMENT.md", "ownership/INDIVIDUAL-CONTRIBUTOR-ASSIGNMENT.md")
	addBlob("legal/CORPORATE-CONTRIBUTOR-ASSIGNMENT.md", "ownership/CORPORATE-CONTRIBUTOR-ASSIGNMENT.md")
	addBlob("legal/assignment-registry.yml", "ownership/assignment-registry.yml")
	addBlob(".github/CONTRIBUTING-IP.md", "ownership/CONTRIBUTING-IP.md")
	addBlob(".github/workflows/contributor-rights-check.yml", "ownership/contributor-rights-check.yml")

	// Third-Party policies
	addBlob("CODE_OF_CONDUCT.md", "third-party/CODE_OF_CONDUCT.md")
	addBlob("docs/legal/THIRD-PARTY-POLICY.md", "third-party/THIRD-PARTY-POLICY.md")
	addBlob("docs/legal/AI-CONTRIBUTION-POLICY.md", "third-party/AI-CONTRIBUTION-POLICY.md")

	// Integrity files
	addBlob("distribution/PACK-MANIFEST.json", "integrity/PACK-MANIFEST.json")
	addBlob("VERIFICATION.json", "integrity/VERIFICATION.json")
	addBlob("RUNTIME-VERSION.yaml", "integrity/RUNTIME-VERSION.yaml")
	addBlob("PACK-VERSION.yaml", "integrity/PACK-VERSION.yaml")
	addBlob("CHANGELOG.md", "integrity/CHANGELOG.md")

	// Git Source metadata
	commits, authors, err := CollectCommitAncestry(ctx, repoDir, headSHA)
	if err == nil {
		var commitsJSONL bytes.Buffer
		for _, c := range commits {
			b, _ := json.Marshal(c)
			commitsJSONL.Write(b)
			commitsJSONL.WriteByte('\n')
		}
		files = append(files, fileToArchive{
			archivePath: "slaves-due-diligence/source/commits.jsonl",
			data:        commitsJSONL.Bytes(),
		})

		authorsJSON, _ := json.MarshalIndent(authors, "", "  ")
		files = append(files, fileToArchive{
			archivePath: "slaves-due-diligence/source/authors.json",
			data:        authorsJSON,
		})
	}

	sourceStateJSON, _ := json.MarshalIndent(report.Source, "", "  ")
	files = append(files, fileToArchive{
		archivePath: "slaves-due-diligence/source/source-state.json",
		data:        sourceStateJSON,
	})

	// Dependency license files
	for _, dep := range report.Dependency.Dependencies {
		for _, lic := range dep.Licenses {
			// Find and read dependency license
			parts := strings.Split(lic.Path, "third-party/dependencies/")
			if len(parts) == 2 {
				// Mod dir license blob read
				modInfo := parts[1]
				subParts := strings.SplitN(modInfo, "/", 2)
				if len(subParts) == 2 {
					sanitizedPath := subParts[0]
					fileName := subParts[1]
					atIdx := strings.LastIndex(sanitizedPath, "@")
					if atIdx > 0 {
						modPath := strings.ReplaceAll(sanitizedPath[:atIdx], "_", "/")
						modVer := sanitizedPath[atIdx+1:]
						// Read module dir
						mDir := findLocalModuleDir(ctx, repoDir, modPath, modVer)
						if mDir != "" {
							if data, err := os.ReadFile(filepath.Join(mDir, fileName)); err == nil {
								files = append(files, fileToArchive{
									archivePath: filepath.Join("slaves-due-diligence/third-party/dependencies", sanitizedPath, fileName),
									data:        data,
								})
							}
						}
					}
				}
			}
		}
	}

	// Canonical report.json and REPORT.md
	reportJSON, err := report.ToJSON()
	if err != nil {
		return nil, fmt.Errorf("marshal report json: %w", err)
	}
	files = append(files, fileToArchive{
		archivePath: "slaves-due-diligence/report.json",
		data:        reportJSON,
	})

	reportMD := generateMarkdownReport(report)
	files = append(files, fileToArchive{
		archivePath: "slaves-due-diligence/REPORT.md",
		data:        []byte(reportMD),
	})

	// Sort files lexicographically by archive path for deterministic tar creation
	sort.Slice(files, func(i, j int) bool {
		return files[i].archivePath < files[j].archivePath
	})

	// Generate SHA256SUMS file
	var shaSumsBuf bytes.Buffer
	for _, f := range files {
		relSumPath := strings.TrimPrefix(f.archivePath, "slaves-due-diligence/")
		sum := sha256.Sum256(f.data)
		shaSumsBuf.WriteString(fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), relSumPath))
	}
	files = append(files, fileToArchive{
		archivePath: "slaves-due-diligence/SHA256SUMS",
		data:        shaSumsBuf.Bytes(),
	})

	// Final sort including SHA256SUMS
	sort.Slice(files, func(i, j int) bool {
		return files[i].archivePath < files[j].archivePath
	})

	// Validate path traversal safety
	for _, f := range files {
		if strings.Contains(f.archivePath, "..") || strings.HasPrefix(f.archivePath, "/") || strings.Contains(f.archivePath, "\x00") {
			return nil, fmt.Errorf("unsafe archive path traversal rejected: %s", f.archivePath)
		}
	}

	// Create deterministic tar.gz
	var tarGzBuf bytes.Buffer
	gw, err := gzip.NewWriterLevel(&tarGzBuf, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("create gzip writer: %w", err)
	}
	gw.Header.OS = 255 // unknown OS for deterministic output
	gw.Header.ModTime = commitTime

	tw := tar.NewWriter(gw)

	for _, f := range files {
		hdr := &tar.Header{
			Name:     filepath.ToSlash(f.archivePath),
			Mode:     0644,
			Size:     int64(len(f.data)),
			ModTime:  commitTime,
			Uid:      0,
			Gid:      0,
			Uname:    "",
			Gname:    "",
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("write tar header for %s: %w", f.archivePath, err)
		}
		if _, err := tw.Write(f.data); err != nil {
			return nil, fmt.Errorf("write tar content for %s: %w", f.archivePath, err)
		}
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close tar writer: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, fmt.Errorf("close gzip writer: %w", err)
	}

	archiveBytes := tarGzBuf.Bytes()
	archiveSHA := HashBytes(archiveBytes)

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}

	if err := os.WriteFile(outputPath, archiveBytes, 0644); err != nil {
		return nil, fmt.Errorf("write output archive: %w", err)
	}

	return &ExportResult{
		OutputPath:       outputPath,
		SourceHEAD:       headSHA,
		ArchiveSHA256:    archiveSHA,
		OverallStatus:    report.Review.OverallStatus,
		WorkingTreeClean: report.Source.WorkingTreeClean,
		ExportTime:       commitTime,
	}, nil
}

func findLocalModuleDir(ctx context.Context, repoDir, modPath, modVer string) string {
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-json", fmt.Sprintf("%s@%s", modPath, modVer))
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), "GOPROXY=off")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return ""
	}
	var m goModuleModule
	if err := json.Unmarshal(stdout.Bytes(), &m); err == nil {
		return m.Dir
	}
	return ""
}

func generateMarkdownReport(r *Report) string {
	var sb strings.Builder
	sb.WriteString("# SLAVES Acquisition Due-Diligence Evidence Report\n\n")
	sb.WriteString("> **Notice**: This report is mechanically generated evidence and is not legal advice or a legal opinion.\n\n")
	sb.WriteString("## Executive Summary\n\n")
	sb.WriteString(fmt.Sprintf("- **Overall Status**: `%s`\n", r.Review.OverallStatus))
	sb.WriteString(fmt.Sprintf("- **Legal Review Required**: `%v`\n", r.Review.LegalReviewRequired))
	sb.WriteString(fmt.Sprintf("- **Source HEAD SHA**: `%s`\n", r.Source.HeadSHA))
	sb.WriteString(fmt.Sprintf("- **Tree SHA**: `%s`\n", r.Source.TreeSHA))
	sb.WriteString(fmt.Sprintf("- **Working Tree Clean**: `%v`\n\n", r.Source.WorkingTreeClean))

	if !r.Source.WorkingTreeClean {
		sb.WriteString("> [!WARNING]\n")
		sb.WriteString("> **Working tree contained uncommitted changes.** The evidence files packaged in this evidence pack represent the committed HEAD commit only.\n\n")
	}

	sb.WriteString("## Audit Status Breakdown\n\n")
	sb.WriteString("| Component | Status |\n")
	sb.WriteString("|---|---|\n")
	sb.WriteString(fmt.Sprintf("| Current License | `%s` |\n", r.Licensing.CurrentLicense.Status))
	sb.WriteString(fmt.Sprintf("| Historical License (Apache-2.0) | `%s` |\n", r.Licensing.HistoricalLicense.Status))
	sb.WriteString(fmt.Sprintf("| Dual-Licensing Policy | `%s` |\n", r.Licensing.DualLicensingPolicy.Status))
	sb.WriteString(fmt.Sprintf("| Commercial Licensing | `%s` |\n", r.Licensing.CommercialLicensing.Status))
	sb.WriteString(fmt.Sprintf("| Chain of Title | `%s` |\n", r.Ownership.ChainOfTitle.Status))
	sb.WriteString(fmt.Sprintf("| Individual Agreement (ICAA) | `%s` |\n", r.Ownership.IndividualAgreementICAA.Status))
	sb.WriteString(fmt.Sprintf("| Corporate Agreement (CCAA) | `%s` |\n", r.Ownership.CorporateAgreementCCAA.Status))
	sb.WriteString(fmt.Sprintf("| Assignment Registry | `%s` |\n", r.Ownership.AssignmentRegistry.Status))
	sb.WriteString(fmt.Sprintf("| Contributor Rights CI Gate | `%s` |\n", r.Ownership.ContributorRightsGate.Status))
	sb.WriteString(fmt.Sprintf("| Dependencies | `%s` |\n", r.Dependency.Status))
	sb.WriteString(fmt.Sprintf("| Integrity Manifest | `%s` |\n\n", r.Integrity.PackManifest.Status))

	if len(r.Review.UnresolvedItems) > 0 {
		sb.WriteString("## Unresolved Items Requiring Attention\n\n")
		for _, item := range r.Review.UnresolvedItems {
			sb.WriteString(fmt.Sprintf("- %s\n", item))
		}
	}

	return sb.String()
}
