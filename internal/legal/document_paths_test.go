package legal

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentRelocationPreservesCommittedAuditAndExport(t *testing.T) {
	for _, layout := range []string{"legacy", "relocated", "both"} {
		t.Run(layout, func(t *testing.T) {
			repo := createTestRepo(t)
			ctx := context.Background()
			documents := []struct{ old, current, archive string }{
				{"LICENSING.md", "docs/legal/LICENSING.md", "licensing/LICENSING.md"},
				{"COMMERCIAL-LICENSING.md", "docs/legal/COMMERCIAL-LICENSING.md", "licensing/COMMERCIAL-LICENSING.md"},
				{"THIRD_PARTY_NOTICES.md", "docs/legal/THIRD_PARTY_NOTICES.md", "licensing/THIRD_PARTY_NOTICES.md"},
				{"CODE_OF_CONDUCT.md", ".github/CODE_OF_CONDUCT.md", "third-party/CODE_OF_CONDUCT.md"},
				{"CHANGELOG.md", "docs/CHANGELOG.md", "integrity/CHANGELOG.md"},
			}
			write := func(path, content string) {
				t.Helper()
				path = filepath.Join(repo, path)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			for _, doc := range documents {
				if layout != "relocated" {
					write(doc.old, "legacy "+doc.old)
				}
				if layout != "legacy" {
					write(doc.current, "current "+doc.old+"\n"+draftMarker)
				}
			}
			for _, args := range [][]string{{"add", "."}, {"commit", "-m", "Document layout fixture"}} {
				if _, err := execGit(ctx, repo, args...); err != nil {
					t.Fatal(err)
				}
			}
			head, err := execGit(ctx, repo, "rev-parse", "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			sha := strings.TrimSpace(string(head))
			// Neither dirty replacements nor uncommitted relocated files may
			// change the evidence bound to this commit.
			write("docs/legal/LICENSING.md", "dirty uncommitted policy")
			licensing, err := CollectLicensingEvidence(ctx, repo, sha)
			if err != nil {
				t.Fatal(err)
			}
			thirdParty, err := CollectThirdPartyEvidence(ctx, repo, sha)
			if err != nil {
				t.Fatal(err)
			}
			evidence := []FileEvidence{licensing.DualLicensingPolicy, licensing.CommercialLicensing, licensing.ThirdPartyNotices, thirdParty.CodeOfConduct}
			for i, file := range evidence {
				doc := documents[i]
				path, content, status := doc.old, "legacy "+doc.old, StatusPass
				if layout != "legacy" {
					path, content, status = doc.current, "current "+doc.old+"\n"+draftMarker, StatusReviewRequired
				}
				if file.Path != path || file.BlobSHA256 != HashBytes([]byte(content)) || file.Status != status {
					t.Fatalf("wrong committed document evidence for %s: %+v", path, file)
				}
			}
			archive := filepath.Join(t.TempDir(), "evidence.tar.gz")
			if _, err := ExportPack(ctx, repo, archive); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(archive)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			zip, err := gzip.NewReader(file)
			if err != nil {
				t.Fatal(err)
			}
			defer zip.Close()
			entries := make(map[string]string)
			tarReader := tar.NewReader(zip)
			for {
				header, err := tarReader.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(tarReader)
				if err != nil {
					t.Fatal(err)
				}
				entries[header.Name] = string(body)
			}
			for _, doc := range documents {
				want := "legacy " + doc.old
				if layout != "legacy" {
					want = "current " + doc.old + "\n" + draftMarker
				}
				if got := entries["marshal-due-diligence/"+doc.archive]; got != want {
					t.Errorf("exported %s = %q, want %q", doc.archive, got, want)
				}
			}
		})
	}
}
