package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

const (
	// marshalModelEnvelopeBytes bounds one encoded Marshal model request.
	marshalModelEnvelopeBytes = projectcontext.MaximumPacketBytes
	// marshalWorkerBriefBytes bounds one rendered worker brief.
	marshalWorkerBriefBytes = projectcontext.MaximumPacketBytes
	// marshalKnowledgePacketReserve is kept free for mandatory project context
	// when deciding whether optional observed knowledge still fits inline.
	marshalKnowledgePacketReserve = 16 << 10
	// marshalKnowledgeViaRecall moves observed project knowledge to scoped MCP
	// recall when including it inline would crowd out the mandatory packet or
	// push the model request past its byte cap.
	marshalKnowledgeViaRecall = true
)

func marshalEnvelopeSize(provider, prompt, schema string) (int, error) {
	wire, err := json.Marshal(struct{ Provider, Prompt, Schema string }{provider, prompt, schema})
	if err != nil {
		return 0, err
	}
	return len(wire), nil
}

// marshalKnowledgeFits reports whether a prompt carrying optional observed
// knowledge, measured as JSON the same way the model envelope check does,
// leaves room for the mandatory packet reserve.
func (m *MarshalCLI) marshalKnowledgeFits(prompt, schema string) bool {
	size, err := marshalEnvelopeSize(m.Provider, marshalStructuredResultPrompt(m.Provider, prompt, schema), schema)
	return err == nil && size+marshalKnowledgePacketReserve <= marshalModelEnvelopeBytes
}

// validateRequiredMarshalModelEnvelope measures the encoded model request
// envelope (provider, prompt and schema after JSON escaping) and refuses with
// a typed limit when it exceeds the cap.
func (m *MarshalCLI) validateRequiredMarshalModelEnvelope(prompt, schema string) error {
	size, err := marshalEnvelopeSize(m.Provider, prompt, schema)
	if err != nil {
		return err
	}
	if size > marshalModelEnvelopeBytes {
		return model.NewLimitError(projectcontext.ErrBudget, model.Limit{
			Kind:        "provider_envelope",
			Subject:     "Marshal model request",
			ActualBytes: int64(size),
			CapBytes:    marshalModelEnvelopeBytes,
			Action:      "Split this step into smaller parts (fewer tasks or shorter text), then try it again.",
		})
	}
	return nil
}

// fitWorkerBrief renders a worker brief within capBytes by omitting or
// compacting optional context sections first, each with a visible notice naming
// what was omitted and how to fetch it. Required task instructions, criteria,
// scope, checks, approved task note, architecture slice and the newest return
// reason are never cut; if they alone exceed capBytes, dispatch refuses with
// a typed limit.
func fitWorkerBrief(t marshal.Task, bc BriefContext, builder MarshalBrief, capBytes int) (string, error) {
	brief := builder(t, bc)
	if len(brief) <= capBytes {
		return brief, nil
	}
	trimmed := bc
	// 1. Optional recalled project memory moves to MCP recall.
	if len(trimmed.Memory) > 0 || strings.TrimSpace(trimmed.MemoryContext) != "" {
		trimmed.Memory = nil
		trimmed.MemoryContext = "Recalled project memory omitted to stay within the worker brief limit; read it through MARSHAL MCP tools (project_context, memory_recall) if needed.\n"
		if brief = builder(t, trimmed); len(brief) <= capBytes {
			return brief, nil
		}
	}
	// 2. Optional plan index overview moves to an artifact pointer.
	if strings.TrimSpace(trimmed.Index) != "" {
		trimmed.Index = fmt.Sprintf("[omitted: %d bytes of approved plan index; inspect the approved plan pack index artifact for how other tasks fit together]", len(trimmed.Index))
		if brief = builder(t, trimmed); len(brief) <= capBytes {
			return brief, nil
		}
	}
	// 3. Optional full approved requirements summary moves to an artifact pointer.
	if strings.TrimSpace(trimmed.Requirements) != "" {
		trimmed.Requirements = fmt.Sprintf("[omitted: %d bytes of approved plan requirements; inspect the approved plan pack requirements artifact for full background]", len(trimmed.Requirements))
		if brief = builder(t, trimmed); len(brief) <= capBytes {
			return brief, nil
		}
	}
	// 4. Older return reasons compact to a pointer while keeping the newest return inline.
	if len(trimmed.Returned) > 1 {
		orig := trimmed.Returned
		for keep := len(orig) - 1; keep >= 1; keep-- {
			omitted := len(orig) - keep
			pointer := fmt.Sprintf("(%d older return reasons omitted; inspect task %s durable review history for earlier attempts)", omitted, t.PlanTaskID)
			compacted := make([]string, 0, keep+1)
			compacted = append(compacted, pointer)
			compacted = append(compacted, orig[len(orig)-keep:]...)
			trimmed.Returned = compacted
			if brief = builder(t, trimmed); len(brief) <= capBytes {
				return brief, nil
			}
		}
	}
	return "", model.NewLimitError(
		fmt.Errorf("%w: worker brief for task %q needs %d bytes (cap %d)", projectcontext.ErrBudget, t.PlanTaskID, len(brief), capBytes),
		model.Limit{
			Kind:        "worker_brief",
			Subject:     "worker brief for task " + t.PlanTaskID,
			ActualBytes: int64(len(brief)),
			CapBytes:    int64(capBytes),
			Action:      "Split task " + t.PlanTaskID + " or shorten its approved note or instructions, approve the amended plan, then /marshal use-plan.",
		},
	)
}

// appendOptionalWorkerKnowledge appends optional observed project knowledge to
// a worker brief when it fits within capBytes, or replaces it with a scoped
// MCP recall note so optional knowledge never overflows the worker brief.
func appendOptionalWorkerKnowledge(brief, knowledge string, capBytes int) string {
	if strings.TrimSpace(knowledge) == "" {
		return brief
	}
	if len(brief)+1+len(knowledge) <= capBytes {
		return brief + "\n" + knowledge
	}
	if len(brief)+1+len(marshalKnowledgeRecallNote) <= capBytes {
		return brief + "\n" + marshalKnowledgeRecallNote
	}
	return brief
}

// trimUTF8Prefix returns the longest prefix of s of at most maxBytes bytes
// that does not split a valid UTF-8 rune.
func trimUTF8Prefix(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	if maxBytes <= 0 {
		return ""
	}
	end := maxBytes
	for back := 0; back < utf8.UTFMax && end > 0 && !utf8.RuneStart(s[end]); back++ {
		end--
	}
	if end < maxBytes {
		r, size := utf8.DecodeRuneInString(s[end:])
		if r != utf8.RuneError && end+size > maxBytes {
			return s[:end]
		}
	}
	return s[:maxBytes]
}
