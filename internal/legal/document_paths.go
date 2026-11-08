package legal

import "context"

// readDocumentBlob supports relocated repository documents and historical
// commits that kept them at the root. Evidence always comes from the pinned
// commit; an existing current document takes precedence over a legacy copy.
func readDocumentBlob(ctx context.Context, repoDir, headSHA, relPath string) ([]byte, string, error) {
	current := relPath
	switch relPath {
	case "LICENSING.md", "COMMERCIAL-LICENSING.md", "THIRD_PARTY_NOTICES.md":
		current = "docs/legal/" + relPath
	case "CODE_OF_CONDUCT.md":
		current = ".github/" + relPath
	case "CHANGELOG.md":
		current = "docs/" + relPath
	}
	data, err := ReadBlob(ctx, repoDir, headSHA, current)
	if err != nil && current != relPath {
		if legacy, legacyErr := ReadBlob(ctx, repoDir, headSHA, relPath); legacyErr == nil {
			return legacy, relPath, nil
		}
	}
	return data, current, err
}
