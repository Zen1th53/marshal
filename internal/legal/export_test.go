package legal

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestExportPackReproducibilityAndIntegrity(t *testing.T) {
	repoDir := createTestRepo(t)
	ctx := context.Background()

	outPathA := filepath.Join(t.TempDir(), "export-a.tar.gz")
	outPathB := filepath.Join(t.TempDir(), "export-b.tar.gz")

	resA, err := ExportPack(ctx, repoDir, outPathA)
	if err != nil {
		t.Fatalf("ExportPack A failed: %v", err)
	}

	resB, err := ExportPack(ctx, repoDir, outPathB)
	if err != nil {
		t.Fatalf("ExportPack B failed: %v", err)
	}

	bytesA, err := os.ReadFile(outPathA)
	if err != nil {
		t.Fatal(err)
	}
	bytesB, err := os.ReadFile(outPathB)
	if err != nil {
		t.Fatal(err)
	}

	if resA.ArchiveSHA256 != resB.ArchiveSHA256 {
		t.Errorf("expected identical ArchiveSHA256, got A: %s, B: %s", resA.ArchiveSHA256, resB.ArchiveSHA256)
	}

	if !bytes.Equal(bytesA, bytesB) {
		t.Error("CRITICAL REPRODUCIBILITY FAILURE: Export A and Export B generated different bytes for identical commit!")
	}

	// Verify archive contents
	gr, err := gzip.NewReader(bytes.NewReader(bytesA))
	if err != nil {
		t.Fatalf("failed to open gzip reader: %v", err)
	}
	tr := tar.NewReader(gr)

	hasSHA256SUMS := false
	hasReportMD := false
	hasReportJSON := false

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("error reading tar entry: %v", err)
		}

		if filepath.IsAbs(hdr.Name) {
			t.Errorf("SECURITY RISK: absolute path found in tar header: %s", hdr.Name)
		}
		if bytes.Contains([]byte(hdr.Name), []byte("..")) {
			t.Errorf("SECURITY RISK: path traversal '..' found in tar header: %s", hdr.Name)
		}

		if hdr.Name == "marshal-due-diligence/SHA256SUMS" {
			hasSHA256SUMS = true
		}
		if hdr.Name == "marshal-due-diligence/REPORT.md" {
			hasReportMD = true
		}
		if hdr.Name == "marshal-due-diligence/report.json" {
			hasReportJSON = true
		}
	}

	if !hasSHA256SUMS {
		t.Error("expected archive to contain SHA256SUMS")
	}
	if !hasReportMD {
		t.Error("expected archive to contain REPORT.md")
	}
	if !hasReportJSON {
		t.Error("expected archive to contain report.json")
	}
}
