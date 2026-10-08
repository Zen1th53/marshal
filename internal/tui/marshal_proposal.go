package tui

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/permission"
)

const marshalProposalPrefix = "MARSHAL_PROPOSAL "

type marshalProposal struct {
	occurrence                                                            string
	action, key, value, provider, path, id, reason, language, earlierWork string
}

// Parse a single visible assistant line, never shell source. Reject unknown,
// duplicate, non-string and action-inappropriate fields and trailing data.
func parseMarshalProposal(line string) (marshalProposal, error) {
	var p marshalProposal
	invalid := errors.New("invalid or non-allow-listed Marshal proposal")
	if !strings.HasPrefix(line, marshalProposalPrefix) || len(line) > 4096 {
		return p, invalid
	}
	d := json.NewDecoder(strings.NewReader(strings.TrimPrefix(line, marshalProposalPrefix)))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return p, invalid
	}
	fields := map[string]string{}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return p, invalid
		}
		key, ok := token.(string)
		if !ok {
			return p, invalid
		}
		if _, exists := fields[key]; exists {
			return p, invalid
		}
		var value string
		if d.Decode(&value) != nil || strings.ContainsFunc(value, unicode.IsControl) {
			return p, invalid
		}
		fields[key] = value
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return p, invalid
	}
	if _, err = d.Token(); err != io.EOF {
		return p, invalid
	}
	p = marshalProposal{action: fields["action"], key: fields["key"], value: fields["value"], provider: fields["provider"], path: fields["path"], id: fields["id"], reason: fields["reason"], language: fields["language"], earlierWork: fields["earlier_work"]}
	var keys []string
	switch p.action {
	case "intake":
		keys = []string{"action", "language", "earlier_work"}
		if strings.TrimSpace(p.language) == "" || len(p.language) > 100 || (p.earlierWork != "" && p.earlierWork != "yes" && p.earlierWork != "no") {
			return p, invalid
		}
	case "setting":
		keys = []string{"action", "key", "value"}
		valid := false
		switch p.key {
		case "acceptance-mode":
			valid = p.value == "marshal" || p.value == "marshal-then-user" || p.value == "user"
		case "execution-rights":
			valid = p.value == "none" || p.value == "read-only" || p.value == "small-tasks"
		case "control":
			valid = p.value == "free" || p.value == "strict"
		case "rework-limit", "ultra-concurrency", "task-tokens", "plan-tokens", "task-money", "plan-money", "task-wall-seconds", "plan-wall-seconds":
			n, e := strconv.ParseInt(p.value, 10, 64)
			valid = e == nil && n >= 0 && strconv.FormatInt(n, 10) == p.value
			if p.key == "ultra-concurrency" {
				valid = valid && n > 0
			}
			if p.key == "ultra-concurrency" || p.key == "rework-limit" {
				_, e = strconv.Atoi(p.value)
				valid = valid && e == nil
			}
		}
		if !valid {
			return p, invalid
		}
	case "continue", "read":
		keys = []string{"action", "path"}
		if p.action == "continue" {
			keys = append(keys, "provider")
			if p.provider != "codex" && p.provider != "claude" {
				return p, invalid
			}
		}
		if !filepath.IsAbs(p.path) || filepath.Clean(p.path) != p.path {
			return p, invalid
		}
	case "memory", "accept", "return":
		keys = []string{"action", "id"}
		if p.id == "" || len(p.id) > 200 || strings.ContainsFunc(p.id, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.'
		}) {
			return p, invalid
		}
	case "approve", "close", "resume":
		keys = []string{"action"}
	case "amend":
		keys = []string{"action", "reason"}
	default:
		return p, invalid
	}
	if p.action == "return" {
		keys = append(keys, "reason")
	}
	if p.action == "amend" && (strings.EqualFold(p.reason, "approve") || strings.EqualFold(p.reason, "deny")) {
		return p, invalid
	}
	if p.action == "return" || p.action == "amend" {
		if strings.TrimSpace(p.reason) == "" || len(p.reason) > 500 {
			return p, invalid
		}
	}
	if len(fields) != len(keys) {
		return p, invalid
	}
	for _, key := range keys {
		if _, ok := fields[key]; !ok {
			return p, invalid
		}
	}
	return p, nil
}

func (p marshalProposal) command() string {
	switch p.action {
	case "setting":
		return "/marshal settings " + p.key + " " + p.value
	case "continue":
		return "/continue " + p.provider + " " + p.path
	case "read":
		return "/permission read allow " + p.path
	case "memory":
		return "/memory allow " + p.id
	case "approve", "close", "resume":
		return "/marshal " + p.action
	case "amend-approve":
		return "/marshal amend approve"
	case "accept":
		return "/marshal accept " + p.id
	case "return":
		return "/marshal return " + p.id + " " + p.reason
	case "amend":
		return "/marshal amend " + p.reason
	}
	return ""
}

// Called only by the bound Marshal history consumer, not peer/tool/user output.
// History is polled repeatedly; old message occurrences are not replayed; new emissions may be proposed again.
func (w *Workspace) observeMarshalProposals(tr importer.SessionTranscript) {
	w.observeMarshalProposalOccurrence(tr, "")
}
func (w *Workspace) observeMarshalProposalOccurrence(tr importer.SessionTranscript, occurrence string) {
	for messageIndex, message := range tr.Messages {
		if message.Role != "assistant" || message.Kind != importer.MessageKindText {
			continue
		}
		inFence := false
		for _, line := range strings.Split(message.Content, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
				inFence = !inFence
				continue
			}
			if !strings.HasPrefix(trimmed, "MARSHAL_PROPOSAL") {
				continue
			}
			if inFence {
				w.RecordActivity("Ignored Marshal proposal: fenced example is not a proposal.")
				continue
			}
			p, err := parseMarshalProposal(line)
			if err == nil && (p.action == "read" || p.action == "continue") && !w.marshalEarlierWorkWanted() {
				w.RecordActivity("History proposal deferred until the operator chooses to continue earlier work.")
				continue
			}
			fingerprint := fmt.Sprintf("%s\x00%d\x00%s\x00%s", tr.SessionID, messageIndex, message.Timestamp, line)
			p.occurrence = fmt.Sprintf("%x", sha256.Sum256([]byte(fingerprint)))
			if occurrence != "" {
				p.occurrence = occurrence
				fingerprint = occurrence
			}
			if w.store != nil && w.projectID != "" {
				pending, admitErr := w.store.AdmitMarshalProposal(context.Background(), w.projectID, p.occurrence, line)
				if admitErr != nil || !pending {
					continue
				}
			}

			w.permissions.mu.Lock()
			if w.permissions.proposalsSeen == nil {
				w.permissions.proposalsSeen = map[string]bool{}
			}
			seen := w.permissions.proposalsSeen[p.occurrence]
			w.permissions.proposalsSeen[p.occurrence] = true
			w.permissions.mu.Unlock()
			if seen {
				continue
			}
			if err != nil {
				if w.store != nil {
					_ = w.store.ResolveMarshalProposal(context.Background(), w.projectID, p.occurrence, "rejected")
				}
				w.RecordActivity("Ignored Marshal proposal: malformed or non-allow-listed fields.")
				continue
			}
			switch p.action {
			case "intake":
				if err := w.saveMarshalIntake(marshalIntake{Language: p.language, EarlierWork: p.earlierWork}); err != nil {
					w.permissions.mu.Lock()
					delete(w.permissions.proposalsSeen, p.occurrence)
					w.permissions.mu.Unlock()
					continue
				}
				if w.store != nil {
					_ = w.store.ResolveMarshalProposal(context.Background(), w.projectID, p.occurrence, "applied")
				}
			case "continue":
				w.queuePermission(permission.Request{ProposalID: p.occurrence, Kind: "read", Object: p.path, Scope: "this session only, read-only", Who: "Marshal", Reason: p.command(), ContinuationProvider: p.provider})
			case "read":
				w.queuePermission(permission.Request{ProposalID: p.occurrence, Kind: "read", Object: p.path, Scope: "this session only, read-only", Who: "Marshal", Reason: p.command()})
			case "memory":
				found := false
				if w.runtime != nil {
					for _, rec := range w.runtime.ContinuationCandidates() {
						if rec.ID == p.id {
							req := memoryPermission(rec)
							req.ProposalID = p.occurrence
							w.queuePermission(req)
							found = true
							break
						}
					}
				}
				if !found {
					w.RecordActivity("Ignored Marshal memory proposal: candidate does not exist.")
				}
			case "setting":
				w.queuePermission(permission.Request{ProposalID: p.occurrence, Kind: "marshal-command", Object: p.command(), Scope: "this project, next Marshal run", Who: "Marshal", Reason: "Apply the exact proposed setting"})
			case "approve":
				m := w.marshalSession()
				m.mu.Lock()
				proposalRunID := m.runID
				m.mu.Unlock()
				w.permissions.mu.Lock()
				w.permissions.planApprovalPending = true
				w.permissions.planApprovalRunID = proposalRunID
				w.permissions.planApprovalOccurrence = p.occurrence
				w.permissions.mu.Unlock()
				w.queueMarshalPlanApproval()
			case "accept", "return", "amend", "close", "resume":
				if err := w.queueMarshalRunProposal(p); err != nil {
					w.RecordActivity("Ignored Marshal proposal: no matching active run or task.")
				}
			}
		}
	}
}

// A draft may be written before its observer imports it. Keep its proposal
// pending until the runtime has a plan identity and version to display and bind.
func (w *Workspace) queueMarshalPlanApproval() {
	w.permissions.mu.Lock()
	pending := w.permissions.planApprovalPending
	proposalRunID := w.permissions.planApprovalRunID
	occurrence := w.permissions.planApprovalOccurrence
	w.permissions.mu.Unlock()
	if !pending {
		return
	}
	m := w.marshalSession()
	m.mu.Lock()
	service, runID, amendment := m.service, m.runID, m.pending
	m.mu.Unlock()
	if service == nil || runID == "" || (proposalRunID != "" && proposalRunID != runID) {
		return
	}
	p := marshalProposal{action: "approve", occurrence: occurrence}
	if amendment != nil {
		p.action = "amend-approve"
	}
	if err := w.queueMarshalRunProposal(p); err != nil {
		return
	}
	w.permissions.mu.Lock()
	w.permissions.planApprovalPending = false
	w.permissions.mu.Unlock()
}

// Bind run actions to the exact stored plan and result state being displayed.
func (w *Workspace) queueMarshalRunProposal(p marshalProposal) error {
	m := w.marshalSession()
	m.mu.Lock()
	service, runID, pending := m.service, m.runID, m.pending
	m.mu.Unlock()
	if service == nil || runID == "" || (pending != nil && p.action != "amend-approve") || (pending == nil && p.action == "amend-approve") {
		return errors.New("no matching active plan")
	}
	record, err := service.Store.GetMarshalRun(context.Background(), service.ProjectID, runID)
	if err != nil {
		return err
	}
	run := record.Value
	if p.action == "approve" && run.State != marshal.Drafting {
		return errors.New("plan is not awaiting approval")
	}
	if p.action == "accept" || p.action == "return" {
		found := false
		for _, task := range run.Tasks {
			if task.PlanTaskID == p.id {
				found = true
				break
			}
		}
		if !found {
			return errors.New("task is not in the active plan")
		}
	}
	binding := marshalRunProposalBinding(run.PlanVersion, record.Revision, pending)
	w.permissions.mu.Lock()
	if w.permissions.runProposals == nil {
		w.permissions.runProposals = map[string]marshalProposal{}
	}
	req := permission.Request{ProposalID: p.occurrence, Kind: "marshal-command", Object: p.command(), Scope: fmt.Sprintf("run %s, plan %s version %d, state revision %d", runID, run.PlanID, run.PlanVersion, record.Revision), Who: "Marshal", Reason: "Apply this exact runtime action; review the plan pack and result first", RunID: runID, TaskID: binding}
	if pending != nil {
		req.Scope += "; proposed amendment " + binding[strings.LastIndex(binding, ":")+1:]
		req.Reason = "Approve proposed amendment: " + pending.reason
	}
	w.permissions.runProposals[req.Key()] = p
	w.permissions.mu.Unlock()
	w.queuePermission(req)
	return nil
}

func (w *Workspace) applyMarshalProposal(ctx context.Context, req permission.Request) error {
	if req.RunID != "" {
		w.permissions.mu.Lock()
		p, ok := w.permissions.runProposals[req.Key()]
		w.permissions.mu.Unlock()
		if !ok || p.command() != req.Object {
			return errors.New("invalid Marshal run proposal")
		}
		m := w.marshalSession()
		m.mu.Lock()
		service, runID, pending := m.service, m.runID, m.pending
		m.mu.Unlock()
		if service == nil || runID != req.RunID || (pending != nil && p.action != "amend-approve") || (pending == nil && p.action == "amend-approve") {
			return errors.New("Marshal proposal expired")
		}
		record, err := service.Store.GetMarshalRun(ctx, service.ProjectID, runID)
		if err != nil || marshalRunProposalBinding(record.Value.PlanVersion, record.Revision, pending) != req.TaskID {
			return errors.New("Marshal proposal expired")
		}
		// Dispatch arguments directly so spaces in free-text reasons are exact.
		_, err = w.cmd.handleMarshal(ctx, marshalProposalArgs(p))
		return err
	}
	parts := strings.Fields(req.Object)
	if len(parts) != 4 || parts[0] != "/marshal" || parts[1] != "settings" {
		return errors.New("invalid Marshal command proposal")
	}
	data, _ := json.Marshal(map[string]string{"action": "setting", "key": parts[2], "value": parts[3]})
	if _, err := parseMarshalProposal(marshalProposalPrefix + string(data)); err != nil {
		return err
	}
	_, err := w.cmd.Handle(ctx, req.Object)
	return err
}

func marshalRunProposalBinding(planVersion, revision int64, pending *marshalAmendment) string {
	binding := fmt.Sprintf("%d:%d", planVersion, revision)
	if pending != nil {
		data, _ := json.Marshal([]any{pending.planVersion, pending.reason, pending.draft})
		binding += fmt.Sprintf(":%x", sha256.Sum256(data))
	}
	return binding
}

func marshalProposalArgs(p marshalProposal) []string {
	switch p.action {
	case "amend-approve":
		return []string{"amend", "approve"}
	case "accept":
		return []string{p.action, p.id}
	case "return":
		return []string{p.action, p.id, p.reason}
	case "amend":
		return []string{p.action, p.reason}
	default:
		return []string{p.action}
	}
}

func (w *Workspace) marshalProposalDecisionNotice(req permission.Request, allow bool, err error) {
	if req.Who != "Marshal" {
		return
	}
	decision := "declined"
	if allow && err == nil {
		decision = "applied"
	}
	text := fmt.Sprintf("## MARSHAL operator popup decision\n\n%s: %s\n", decision, req.Object)
	if err != nil {
		text = "## MARSHAL operator popup decision\n\nApplication failed; do not continue as approved.\n"
	}
	if w.runtime == nil {
		return
	}
	view, e := openInboxView(w.runtime.ProjectRoot(), "marshal", false)
	if e == nil {
		e = view.write(text)
	}
	if e != nil {
		w.RecordActivity("Marshal popup decision delivery failed: " + e.Error())
	}
}

// Files are suggestions, never authority. Only the live Marshal monitor consumes
// them; parsing and the ordinary operator popup remain mandatory.
func (w *Workspace) observeMarshalProposalFiles(root string) {
	w.recoverMarshalProposals()
	project, err := os.OpenRoot(root)
	if err != nil {
		return
	}
	defer project.Close()
	info, err := project.Lstat(".marshal/proposals")
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return
	}
	fs, err := project.OpenRoot(".marshal/proposals")
	if err != nil {
		return
	}
	defer fs.Close()
	directory, err := fs.Open(".")
	if err != nil {
		return
	}
	opened, err := directory.Stat()
	if err != nil || !os.SameFile(info, opened) {
		directory.Close()
		return
	}
	entries, err := directory.ReadDir(-1)
	directory.Close()
	if err != nil {
		return
	}

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || !entry.Type().IsRegular() {
			continue
		}
		info, err := fs.Lstat(name)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
			continue
		}
		file, err := openProposalFile(fs, name)
		if err != nil {
			continue
		}
		current, err := file.Stat()
		if err != nil || !current.Mode().IsRegular() || !os.SameFile(info, current) {
			file.Close()
			continue
		}
		data, err := io.ReadAll(io.LimitReader(file, 4097))
		file.Close()
		if err != nil || len(data) > 4096 {
			continue
		}
		line := marshalProposalPrefix + strings.TrimSpace(string(data))
		_, parseErr := parseMarshalProposal(line)
		if parseErr != nil {
			continue
		}
		sessionID := fmt.Sprintf("proposal-file:%s:%d:%x", name, info.ModTime().UnixNano(), sha256.Sum256(data))
		fingerprint := fmt.Sprintf("%s\x00%d\x00%s\x00%s", sessionID, 0, "", line)
		id := fmt.Sprintf("%x", sha256.Sum256([]byte(fingerprint)))
		if w.store == nil {
			continue
		}
		pending, admitErr := w.store.AdmitMarshalProposal(context.Background(), w.projectID, id, line)
		if admitErr != nil {
			continue
		}
		// Durable admission precedes source consumption. A crash on either
		// side of the rename replays the same occurrence, not a new request.
		if err := fs.Rename(name, name+".consumed"); err != nil {
			continue
		}
		if !pending {
			continue
		}
		w.observeMarshalProposalOccurrence(importer.SessionTranscript{SessionID: sessionID, Messages: []importer.Message{{Role: "assistant", Content: line}}}, id)
	}
}

// Recovery queues the original bound request. Reconstructing a fresh binding
// would authorize a different plan/result than the one originally displayed.
func (w *Workspace) recoverMarshalProposals() {
	if w.store == nil || w.projectID == "" {
		return
	}
	pending, err := w.store.PendingMarshalProposals(context.Background(), w.projectID)
	if err != nil {
		return
	}
	for _, record := range pending {
		if record.RequestJSON == "" {
			// Admissions interrupted before binding still need parsing and a popup.
			w.observePendingMarshalProposal(record.ID, record.Line)
			continue
		}
		var req permission.Request
		if json.Unmarshal([]byte(record.RequestJSON), &req) != nil {
			continue
		}
		if req.RunID != "" {
			p, err := parseMarshalProposal(record.Line)
			if err != nil {
				continue
			}
			p.occurrence = record.ID
			w.permissions.mu.Lock()
			if w.permissions.runProposals == nil {
				w.permissions.runProposals = map[string]marshalProposal{}
			}
			w.permissions.runProposals[req.Key()] = p
			w.permissions.mu.Unlock()
		}
		w.queuePermission(req)
	}
}

func (w *Workspace) observePendingMarshalProposal(id, line string) {
	// Supply the persisted occurrence ID while reusing the normal validator.
	w.observeMarshalProposalOccurrence(importer.SessionTranscript{SessionID: "recovered:" + id, Messages: []importer.Message{{Role: "assistant", Content: line}}}, id)
}
