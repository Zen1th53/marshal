package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// A live provider session is captured as one bounded episode per session,
// not one memory per message: a 25-minute session produced 121 separate
// "Imported Session" records, each its own review request, and they crowded
// curated knowledge out of recall.
const (
	// EpisodeMaxBytes bounds one episode record's body.
	EpisodeMaxBytes = 8 << 10
	// EpisodeHeadBytes keeps the session's opening (usually its goal) when
	// older middle turns are trimmed.
	EpisodeHeadBytes = 1536
	// EpisodeMaxParts bounds how many episode records one session can make:
	// a new part starts only after the previous one was decided.
	EpisodeMaxParts = 32
	// EpisodeMaxDigests bounds the remembered message digests used to drop
	// replayed messages.
	EpisodeMaxDigests = 4096
)

const episodeOmission = "[… earlier turns of this session omitted …]\n"

// SessionEpisodeID is the stable ID of a session's episode part. The same
// session always maps to the same record, so re-captures update it instead
// of minting new memories.
func SessionEpisodeID(projectID, provider, sessionID string, part int) string {
	h := sha256.New()
	fmt.Fprintf(h, "episode\x00%s\x00%s\x00%s\x00%d", projectID, provider, sessionID, part)
	return "MEM-IMPORT-" + hex.EncodeToString(h.Sum(nil))[:16]
}

// SessionEpisodeTitle names an episode part for review.
func SessionEpisodeTitle(sessionID, provider string, part int) string {
	if part == 0 {
		return fmt.Sprintf("Imported Session %s (%s)", sessionID, provider)
	}
	return fmt.Sprintf("Imported Session %s (%s), part %d", sessionID, provider, part+1)
}

// EpisodeEntryDigest identifies one rendered message so a replayed message
// is recognized after its text was trimmed from the bounded body.
func EpisodeEntryDigest(entry string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(entry)))
	return hex.EncodeToString(sum[:8])
}

// BoundEpisodeBody keeps an episode within EpisodeMaxBytes: the opening of
// the session, an omission marker, then the most recent whole entries.
func BoundEpisodeBody(body string) string {
	if len(body) <= EpisodeMaxBytes {
		return body
	}
	head := body
	if len(head) > EpisodeHeadBytes {
		head = head[:EpisodeHeadBytes]
		if i := strings.LastIndex(head, "\n["); i > 0 {
			head = head[:i+1]
		} else {
			head = strings.ToValidUTF8(head, "") + "\n"
		}
	}
	if strings.HasSuffix(head, episodeOmission) {
		head = strings.TrimSuffix(head, episodeOmission)
	}
	budget := EpisodeMaxBytes - len(head) - len(episodeOmission)
	tail := body[len(body)-budget:]
	if i := strings.Index(tail, "\n["); i >= 0 {
		tail = tail[i+1:]
	} else {
		tail = strings.ToValidUTF8(tail, "")
	}
	return head + episodeOmission + tail
}
