package evidenceplan

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

// SanitizeMemoryForPrompt encodes title and body into safe XML character data to prevent prompt breakout attacks.
func SanitizeMemoryForPrompt(rec model.MemoryRecordV2) string {
	safeTitle := escapeXMLString(rec.Title)
	safeBody := escapeXMLString(rec.Body)

	// Additional hardening: replace dangerous Markdown breakout fences
	safeBody = strings.ReplaceAll(safeBody, "```", "` ` `")

	return fmt.Sprintf("<memory_item id=\"%s\" lifecycle=\"%s\"><title>%s</title><body>%s</body></memory_item>",
		xmlEscapeAttr(rec.ID),
		xmlEscapeAttr(string(rec.Lifecycle)),
		safeTitle,
		safeBody,
	)
}

func escapeXMLString(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

func xmlEscapeAttr(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}
