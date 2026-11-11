package architecturereview

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/architecturecodec"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Config struct {
	Context                           context.Context
	Project, ArchitectureID, StateDir string
	Proposal                          json.RawMessage
	Layouts                           map[string]Layout
	Language                          string
	UI                                json.RawMessage
	Localize                          func(context.Context, string, json.RawMessage) (json.RawMessage, error)
	Feedback                          func(context.Context, json.RawMessage) (json.RawMessage, error)
	OnApprove                         func(json.RawMessage) error
	Builders                          func(context.Context) []BuilderOption
	// AcceptBlocked, when it returns a non-empty reason, is shown on the page
	// and refuses Accept. OnAccepted runs after an acceptance was saved.
	AcceptBlocked func() string
	OnAccepted    func(json.RawMessage)
}

// BuilderOption is one provider the owner may pick for a part, as the
// browser shows it. Limit and Reason are plain English.
type BuilderOption struct {
	ID, Glyph     string
	Available     bool
	Limit, Reason string
}

type Review struct {
	mu           sync.Mutex
	config       Config
	state        map[string]any
	layouts      map[string]Layout
	layoutDigest string
	writeFile    func(string, any) error
	work         sync.WaitGroup
	optionsAt    time.Time
	options      []BuilderOption
}

// Wait blocks until every background feedback worker has finished and written
// its final state. Callers cancel Config.Context first; submissions made after
// that are refused, so Wait cannot race a new worker. It briefly takes the
// review lock so a submission that passed the context check has registered.
func (r *Review) Wait() {
	r.mu.Lock()
	r.mu.Unlock()
	r.work.Wait()
}

func New(config Config) (*Review, error) {
	if config.Context == nil {
		config.Context = context.Background()
	}
	if strings.TrimSpace(config.Project) == "" || strings.TrimSpace(config.ArchitectureID) == "" || !filepath.IsAbs(config.StateDir) {
		return nil, errors.New("canonical project, architecture ID and absolute state directory are required")
	}
	if err := ensurePrivateDirectory(config.StateDir); err != nil {
		return nil, err
	}
	r := &Review{config: config, writeFile: writePrivate}
	file := filepath.Join(config.StateDir, "state.json")
	raw, err := readPrivate(file)
	if err == nil {
		r.state, err = decodeObject(raw)
		if err != nil {
			return nil, err
		}
		if r.state["protocol_version"] != Version || r.state["project"] != config.Project || r.state["architecture_id"] != config.ArchitectureID {
			return nil, errors.New("stored review belongs to a different project or protocol")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	} else {
		architecture, err := decodeObject(config.Proposal)
		if err != nil {
			return nil, err
		}
		if err = validateArchitecture(architecture); err != nil {
			return nil, err
		}
		r.state = map[string]any{"protocol_version": Version, "project": config.Project, "architecture_id": config.ArchitectureID, "revision": json.Number("1"), "architecture": architecture, "comments": []any{}, "approved": false, "builders": map[string]any{}}
	}
	architecture, ok := r.state["architecture"].(map[string]any)
	if !ok {
		return nil, errors.New("stored architecture is invalid")
	}
	if err = validateArchitecture(architecture); err != nil {
		return nil, err
	}
	if revision, ok := integer(r.state["revision"]); !ok || revision < 1 {
		return nil, errors.New("stored architecture revision is invalid")
	}
	if _, exists := r.state["builders"]; !exists {
		r.state["builders"] = map[string]any{}
	} else {
		builders, ok := r.state["builders"].(map[string]any)
		if !ok {
			return nil, errors.New("stored architecture builders are invalid")
		}
		if err = architecturecodec.ValidateBuilders(builders, architecture); err != nil {
			return nil, err
		}
	}
	var storedLayouts json.RawMessage
	if len(config.Layouts) == 0 {
		if stored, exists := r.state["layout_file"]; exists {
			name, valid := stored.(string)
			if !valid || !strings.HasSuffix(name, ".json") || !digestPattern.MatchString(strings.TrimSuffix(name, ".json")) {
				return nil, errors.New("stored drawing reference is invalid")
			}
			data, readErr := readPrivate(filepath.Join(config.StateDir, "layouts", name))
			if readErr != nil {
				return nil, fmt.Errorf("stored drawing geometry: %w", readErr)
			}
			storedLayouts = data
		} else {
			if _, exists := r.state["layout_digest"]; exists {
				return nil, errors.New("stored exact drawing geometry is missing")
			}
			raw, _ := canonicalJSON(architecture)
			config.Layouts, err = GenericLayouts(raw)
			if err != nil {
				return nil, err
			}
		}
	}
	layoutRaw := storedLayouts
	if layoutRaw == nil {
		layoutRaw, err = json.Marshal(config.Layouts)
		if err != nil {
			return nil, err
		}
	}
	architectureRaw, err := canonicalJSON(architecture)
	if err != nil {
		return nil, err
	}
	config.Layouts, r.layouts, r.layoutDigest, err = BindStoredLayouts(architectureRaw, layoutRaw)
	if err != nil {
		return nil, err
	}
	layoutFile := r.layoutDigest + ".json"
	if previous, exists := r.state["layout_file"]; exists && storedLayouts != nil && previous != layoutFile {
		return nil, errors.New("persisted drawing geometry differs from its content binding")
	}
	if err = r.writeFile(filepath.Join(config.StateDir, "layouts", layoutFile), config.Layouts); err != nil {
		return nil, err
	}
	currentDigest, err := digestValue(architecture)
	if err != nil {
		return nil, err
	}
	if previous, exists := r.state["digest"]; exists && previous != currentDigest {
		return nil, errors.New("stored architecture digest does not match its content")
	}
	comments, ok := r.state["comments"].([]any)
	if !ok {
		return nil, errors.New("stored feedback history is invalid")
	}
	for _, value := range comments {
		entry, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("invalid stored feedback entry")
		}
		id, ok := entry["id"].(string)
		if !ok || !requestIDPattern.MatchString(id) {
			return nil, errors.New("invalid stored feedback identity")
		}
		if entry["status"] == "queued" || entry["status"] == "running" {
			entry["status"] = "failed"
			entry["resolved"] = false
			entry["error"] = "Server restarted before the model result was verified"
		}
	}
	if previous, exists := r.state["layout_digest"]; exists && previous != r.layoutDigest {
		r.state["approved"] = false
		delete(r.state, "approval")
	}
	r.state["layout_digest"] = r.layoutDigest
	r.state["layout_file"] = layoutFile
	var cached map[string]any
	if ui, exists := r.state["ui"]; exists {
		var valid bool
		cached, valid = ui.(map[string]any)
		if !valid {
			return nil, errors.New("stored UI overlay is invalid")
		}
		storedLanguage, _ := r.state["language"].(string)
		if err = validateNormalizedUI(cached, storedLanguage, architecture); err != nil {
			return nil, err
		}
		storedUIDigest, digestErr := digestValue(cached)
		if digestErr != nil || r.state["ui_digest"] != storedUIDigest || r.state["ui_architecture_digest"] != currentDigest {
			return nil, errors.New("stored UI translation digest is stale")
		}
		if storedLanguage != approvedLanguage(config.Language) {
			cached = nil
		}
	}
	ui, err := r.resolveUI(config.Context, architecture, config.UI, cached)
	if err != nil {
		return nil, err
	}
	oldUIDigest := r.state["ui_digest"]
	if err = bindUI(r.state, ui); err != nil {
		return nil, err
	}
	if oldUIDigest != r.state["ui_digest"] {
		r.state["approved"] = false
		delete(r.state, "approval")
	}
	if approval, _ := r.state["approval"].(map[string]any); r.state["approved"] == true && (approval == nil || approval["source"] != "browser_operator") {
		r.state["approved"] = false
		delete(r.state, "approval")
	}
	if r.state["approved"] == true {
		raw, _ := canonicalJSON(r.state)
		if _, err = ValidateStoredState(raw, config.Project); err != nil {
			return nil, err
		}
	}
	if err = r.commit(r.state); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Review) State() (json.RawMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.publicState()
}
func (r *Review) publicState() (json.RawMessage, error) {
	state, err := cloneObject(r.state)
	if err != nil {
		return nil, err
	}
	state["layouts"] = r.layouts
	if r.config.AcceptBlocked != nil {
		if reason := r.config.AcceptBlocked(); reason != "" {
			state["accept_blocked"] = reason
		}
	}
	return canonicalJSON(state)
}
func (r *Review) commit(next map[string]any) error {
	digest, err := digestValue(next["architecture"])
	if err != nil {
		return err
	}
	next["digest"] = digest
	next["layout_digest"] = r.layoutDigest
	buildersDigest, err := architecturecodec.BuildersDigest(next["builders"].(map[string]any))
	if err != nil {
		return err
	}
	next["builders_digest"] = buildersDigest
	if err = r.writeFile(filepath.Join(r.config.StateDir, "state.json"), next); err != nil {
		return err
	}
	r.state = next
	return nil
}

// builderOptions returns the providers the owner may pick for a part, or nil
// when this build has no per-part builder. The callback result is cached for
// 10 seconds. Callers hold r.mu; the callback must not take r.mu.
func (r *Review) builderOptions(ctx context.Context) []BuilderOption {
	if r.config.Builders == nil {
		return nil
	}
	now := time.Now()
	if r.options != nil && now.Sub(r.optionsAt) < 10*time.Second {
		return r.options
	}
	r.options = r.config.Builders(ctx)
	r.optionsAt = now
	return r.options
}

// setBuilder records the owner's builder for one component. The caller holds
// r.mu. Choosing a builder never changes the architecture, its digest or its
// revision, and is refused once the architecture is accepted.
func (r *Review) setBuilder(ctx context.Context, next, q map[string]any) (json.RawMessage, error) {
	options := r.builderOptions(ctx)
	if options == nil {
		return nil, errors.New("Choosing a builder is not available in this edition.")
	}
	if next["approved"] == true {
		return nil, errors.New("The architecture is accepted; builders are fixed for this run.")
	}
	componentID, _ := q["component_id"].(string)
	architecture := next["architecture"].(map[string]any)
	known := false
	for _, value := range architecture["components"].([]any) {
		if value.(map[string]any)["id"] == componentID {
			known = true
			break
		}
	}
	if !known {
		return nil, errors.New("That part is no longer in the architecture. Select it again.")
	}
	builder, _ := q["builder"].(string)
	builders := next["builders"].(map[string]any)
	if builder == "auto" {
		delete(builders, componentID)
		if err := r.commit(next); err != nil {
			return nil, err
		}
		return r.publicState()
	}
	var chosen *BuilderOption
	for i := range options {
		if options[i].ID == builder {
			chosen = &options[i]
			break
		}
	}
	if chosen == nil {
		return nil, errors.New("Unknown builder.")
	}
	if !chosen.Available {
		return nil, errors.New(builder + " is unavailable: " + chosen.Reason)
	}
	builders[componentID] = builder
	if err := r.commit(next); err != nil {
		return nil, err
	}
	return r.publicState()
}

func (r *Review) Apply(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	q, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if q["protocol_version"] != Version || !onlyKeys(q, "protocol_version", "action", "revision", "digest", "ui_digest", "language", "target", "request_type", "comment", "followup_to", "request_id", "reason", "component_id", "builder", "builders_digest", "design_digest") {
		return nil, errors.New("review protocol or request fields are invalid")
	}
	revision, valid := integer(q["revision"])
	current, _ := integer(r.state["revision"])
	if !valid || revision != current || q["digest"] != r.state["digest"] || q["ui_digest"] != r.state["ui_digest"] || q["language"] != r.state["language"] {
		return nil, errors.New("architecture revision changed; select the target again")
	}
	action, _ := q["action"].(string)
	if action != "withdraw" {
		if _, ok := q["request_id"]; ok {
			return nil, errors.New("request ID is server-owned")
		}
		if _, ok := q["reason"]; ok {
			return nil, errors.New("withdrawal fields are not valid here")
		}
	}
	next, err := cloneObject(r.state)
	if err != nil {
		return nil, err
	}
	if action == "builder" {
		return r.setBuilder(ctx, next, q)
	}
	if action == "approve" {
		if q["builders_digest"] != next["builders_digest"] {
			return nil, errors.New("Builders changed. Check them and press Accept again.")
		}
		if r.config.AcceptBlocked != nil {
			if reason := r.config.AcceptBlocked(); reason != "" {
				return nil, errors.New(reason)
			}
		}
		for _, value := range next["comments"].([]any) {
			if value.(map[string]any)["resolved"] != true {
				return nil, errors.New("unresolved feedback prevents approval")
			}
		}
		next["approved"] = true
		next["approval"] = map[string]any{"source": "browser_operator", "revision": next["revision"], "digest": next["digest"], "architecture_id": next["architecture_id"], "view_version": "kiss-v1", "layout_digest": r.layoutDigest, "language": next["language"], "ui_digest": next["ui_digest"], "builders_digest": next["builders_digest"]}
		if r.config.OnApprove != nil {
			accepted, _ := canonicalJSON(next)
			if err = r.config.OnApprove(accepted); err != nil {
				return nil, err
			}
		}
		if err = r.commit(next); err != nil {
			return nil, err
		}
		if r.config.OnAccepted != nil {
			accepted, _ := r.publicState()
			r.work.Add(1)
			go func() { defer r.work.Done(); r.config.OnAccepted(accepted) }()
		}
		return r.publicState()
	}
	if action == "withdraw" {
		id, _ := q["request_id"].(string)
		entry := findEntry(next, id)
		if !requestIDPattern.MatchString(id) || entry == nil || entry["resolved"] == true || (entry["status"] != "failed" && entry["status"] != "needs_clarification") {
			return nil, errors.New("only an open stopped request can be withdrawn")
		}
		reason, ok := q["reason"].(string)
		if !ok || strings.TrimSpace(reason) == "" || utf8.RuneCountInString(reason) > 5000 {
			return nil, errors.New("withdrawal reason is invalid")
		}
		entry["resolved"] = true
		entry["withdrawn"] = true
		entry["closed_reason"] = reason
		entry["closed_by"] = "browser_operator"
		entry["closed_at"] = now()
		if err = r.commit(next); err != nil {
			return nil, err
		}
		return r.publicState()
	}
	if action != "submit" {
		return nil, errors.New("view mode permits only explicit feedback submission")
	}
	if r.config.Feedback == nil {
		return nil, errors.New("actual Marshal feedback adapter is unavailable")
	}
	if err = r.config.Context.Err(); err != nil {
		return nil, err
	}
	for _, value := range next["comments"].([]any) {
		entry := value.(map[string]any)
		if entry["status"] == "queued" || entry["status"] == "running" {
			return nil, errors.New("wait for the previous Marshal reply")
		}
	}
	comment, ok := q["comment"].(string)
	if !ok || strings.TrimSpace(comment) == "" || utf8.RuneCountInString(comment) > 5000 {
		return nil, errors.New("feedback text is empty or too long")
	}
	kind, _ := q["request_type"].(string)
	if kind != "comment" && kind != "change" {
		return nil, errors.New("invalid feedback request type")
	}
	unbound, ok := q["target"].(map[string]any)
	if !ok {
		return nil, errors.New("exact feedback target is missing")
	}
	target, err := bindTarget(r.layouts, unbound)
	if err != nil {
		return nil, err
	}
	architecture := next["architecture"].(map[string]any)
	selection := selectionFor(architecture, r.layouts, target)
	prior := []any{}
	followup, hasFollowup := q["followup_to"]
	if hasFollowup {
		id, ok := followup.(string)
		previous := findEntry(next, id)
		if !ok || !requestIDPattern.MatchString(id) || previous == nil || previous["resolved"] == true || (previous["status"] != "failed" && previous["status"] != "needs_clarification") {
			return nil, errors.New("previous feedback is not open for a followup")
		}
		for _, v := range next["comments"].([]any) {
			if v.(map[string]any)["followup_to"] == id {
				return nil, errors.New("continue the most recent followup; branching is forbidden")
			}
		}
		if !equalJSON(previous["target"], target) || previous["request_type"] != kind {
			return nil, errors.New("followup must address the exact previous target and request type")
		}
		stored, err := readPrivate(filepath.Join(r.config.StateDir, "requests", id+".json"))
		if err != nil {
			return nil, errors.New("previous exact feedback context is unavailable")
		}
		parent, err := decodeObject(stored)
		if err != nil || !equalJSON(parent["selection"], selection) {
			return nil, errors.New("previous selected content changed; a new review is required")
		}
		seen := map[string]bool{}
		for cursor := previous; cursor != nil; {
			cursorID := cursor["id"].(string)
			if seen[cursorID] {
				return nil, errors.New("cyclic feedback history")
			}
			seen[cursorID] = true
			copy := map[string]any{}
			for _, key := range []string{"id", "revision", "digest", "target", "request_type", "comment", "status", "reply", "questions", "error"} {
				if value, exists := cursor[key]; exists {
					copy[key] = value
				}
			}
			prior = append(prior, copy)
			ancestor, _ := cursor["followup_to"].(string)
			if ancestor == "" {
				break
			}
			cursor = findEntry(next, ancestor)
		}
	}
	var nonce [12]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(nonce[:])
	entry := map[string]any{"id": id, "revision": next["revision"], "digest": next["digest"], "target": target, "request_type": kind, "comment": comment, "status": "queued", "resolved": false, "submitted_at": now()}
	if hasFollowup {
		entry["followup_to"] = followup
	}
	envelope := map[string]any{"protocol_version": Version, "request_id": id, "project": next["project"], "architecture_id": next["architecture_id"], "revision": next["revision"], "digest": next["digest"], "architecture": architecture, "selection": selection, "request_type": kind, "request": comment}
	envelope["language"] = next["language"]
	envelope["ui_digest"] = next["ui_digest"]
	envelope["display"] = displayFor(next["ui"].(map[string]any))
	if len(prior) > 0 {
		envelope["prior_requests"] = prior
	}
	if err = r.writeFile(filepath.Join(r.config.StateDir, "requests", id+".json"), envelope); err != nil {
		return nil, err
	}
	next["comments"] = append(next["comments"].([]any), entry)
	next["approved"] = false
	delete(next, "approval")
	if err = r.commit(next); err != nil {
		return nil, err
	}
	result, err := r.publicState()
	if err != nil {
		return nil, err
	}
	r.work.Add(1)
	go func() {
		defer r.work.Done()
		r.process(id, envelope)
	}()
	return result, nil
}

func (r *Review) process(id string, envelope map[string]any) {
	fail := func(err error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		next, cloneErr := cloneObject(r.state)
		if cloneErr != nil {
			return
		}
		entry := findEntry(next, id)
		if entry == nil {
			return
		}
		entry["status"] = "failed"
		entry["resolved"] = false
		entry["error"] = err.Error()
		_ = r.commit(next)
	}
	r.mu.Lock()
	next, err := cloneObject(r.state)
	if err == nil {
		entry := findEntry(next, id)
		if entry == nil {
			err = errors.New("queued request disappeared")
		} else {
			entry["status"] = "running"
			err = r.commit(next)
		}
	}
	r.mu.Unlock()
	if err != nil {
		fail(err)
		return
	}
	ctx, cancel := context.WithTimeout(r.config.Context, 600*time.Second)
	defer cancel()
	rawEnvelope, err := canonicalJSON(envelope)
	if err != nil {
		fail(err)
		return
	}
	raw, err := r.config.Feedback(ctx, rawEnvelope)
	if err != nil {
		fail(err)
		return
	}
	result, err := decodeObject(raw)
	if err != nil {
		fail(err)
		return
	}
	if err = checkResult(envelope, result, r.layouts); err != nil {
		fail(err)
		return
	}
	if !equalJSON(envelope["architecture"], result["architecture"]) {
		ui, localizeErr := r.resolveUI(ctx, result["architecture"].(map[string]any), nil, nil)
		if localizeErr != nil {
			fail(localizeErr)
			return
		}
		result["display_overlay"] = ui
	}
	r.mu.Lock()
	err = r.applyResult(id, envelope, result)
	r.mu.Unlock()
	if err != nil {
		fail(err)
	}
}

func (r *Review) applyResult(id string, envelope, result map[string]any) error {
	if !equalJSON(r.state["revision"], envelope["revision"]) || r.state["digest"] != envelope["digest"] || r.state["ui_digest"] != envelope["ui_digest"] || r.state["language"] != envelope["language"] {
		return errors.New("Marshal response belongs to a stale architecture revision")
	}
	if err := r.writeFile(filepath.Join(r.config.StateDir, "results", id+".json"), result); err != nil {
		return err
	}
	next, err := cloneObject(r.state)
	if err != nil {
		return err
	}
	changed := !equalJSON(next["architecture"], result["architecture"])
	if changed {
		// Preserve unresolved selections outside this request's explicit lineage.
		lineage := map[string]bool{id: true}
		if prior, ok := envelope["prior_requests"].([]any); ok {
			for _, v := range prior {
				lineage[v.(map[string]any)["id"].(string)] = true
			}
		}
		proposed := result["architecture"].(map[string]any)
		current := next["architecture"].(map[string]any)
		for _, value := range next["comments"].([]any) {
			entry := value.(map[string]any)
			if entry["resolved"] == true || lineage[entry["id"].(string)] {
				continue
			}
			target, ok := entry["target"].(map[string]any)
			if !ok {
				return errors.New("unresolved feedback target is invalid")
			}
			if !equalJSON(selectionFor(current, r.layouts, target), selectionFor(proposed, r.layouts, target)) {
				return errors.New("change would orphan unresolved feedback; answer or withdraw it first")
			}
		}
		revision, _ := integer(next["revision"])
		if err = r.writeFile(filepath.Join(r.config.StateDir, "revisions", fmt.Sprintf("%d.json", revision)), r.state); err != nil {
			return err
		}
		next["architecture"] = result["architecture"]
		if ui, ok := result["display_overlay"].(map[string]any); ok {
			if err = bindUI(next, ui); err != nil {
				return err
			}
		} else {
			return errors.New("revised architecture display translation is missing")
		}
		next["revision"] = json.Number(fmt.Sprintf("%d", revision+1))
		next["approved"] = false
		delete(next, "approval")
	}
	entry := findEntry(next, id)
	if entry == nil {
		return errors.New("request identity disappeared")
	}
	entry["status"] = result["status"]
	entry["reply"] = result["reply"]
	entry["questions"] = result["questions"]
	entry["resolved"] = result["status"] != "needs_clarification"
	entry["receipt"] = map[string]any{"provider": result["provider"], "session_id": result["session_id"]}
	if entry["resolved"] == true {
		if prior, ok := envelope["prior_requests"].([]any); ok {
			for _, value := range prior {
				previous := findEntry(next, value.(map[string]any)["id"].(string))
				if previous != nil && previous["resolved"] != true {
					previous["resolved"] = true
					previous["superseded_by"] = id
				}
			}
		}
	}
	return r.commit(next)
}

func checkResult(envelope, result map[string]any, layouts map[string]Layout) error {
	provider, _ := result["provider"].(string)
	if provider != "codex" && provider != "claude" && provider != "agy" && provider != "opencode" {
		return errors.New("Marshal provider receipt is invalid")
	}
	if !nonempty(result["session_id"]) || !nonempty(result["reply"]) {
		return errors.New("Marshal reply or actual session receipt is missing")
	}
	status, _ := result["status"].(string)
	if status != "answered" && status != "needs_clarification" && status != "proposed_change" {
		return errors.New("invalid Marshal feedback status")
	}
	questions, ok := result["questions"].([]any)
	if !ok {
		return errors.New("clarification questions must be an array")
	}
	for _, question := range questions {
		if !nonempty(question) {
			return errors.New("clarification questions must be nonempty strings")
		}
	}
	if (status == "needs_clarification") != (len(questions) > 0) {
		return errors.New("open clarification questions prohibit applying a change")
	}
	before := envelope["architecture"].(map[string]any)
	after, ok := result["architecture"].(map[string]any)
	if !ok {
		return errors.New("Marshal architecture result is missing")
	}
	if err := validateArchitecture(after); err != nil {
		return err
	}
	if err := validateLayouts(after, layouts); err != nil {
		return err
	}
	changed := !equalJSON(before, after)
	if status != "proposed_change" || envelope["request_type"] == "comment" {
		if changed {
			return errors.New("a comment or clarification cannot change architecture")
		}
		if status == "proposed_change" {
			return errors.New("comment cannot propose an architecture change")
		}
		return nil
	}
	if !changed {
		return errors.New("proposed change did not change the selected architecture")
	}
	selection := envelope["selection"].(map[string]any)
	for _, collection := range []string{"components", "connections"} {
		allowed := map[string]bool{}
		for _, value := range selection[collection].([]any) {
			allowed[value.(map[string]any)["id"].(string)] = true
		}
		old, next := before[collection].([]any), after[collection].([]any)
		if len(old) != len(next) {
			return errors.New("change added or removed architecture objects")
		}
		for i, value := range old {
			original, proposed := value.(map[string]any), next[i].(map[string]any)
			id := original["id"].(string)
			if id != proposed["id"] || (!allowed[id] && !equalJSON(original, proposed)) {
				return errors.New("change edited an unselected object or stable object order")
			}
			if collection == "connections" && (original["source"] != proposed["source"] || original["target"] != proposed["target"]) {
				return errors.New("changing endpoints requires a separate topology review")
			}
		}
	}
	if len(before) != len(after) {
		return errors.New("change added or removed project fields")
	}
	for key, value := range before {
		if key != "components" && key != "connections" && !equalJSON(value, after[key]) {
			return errors.New("change edited unselected project fields")
		}
	}
	return nil
}

func findEntry(state map[string]any, id string) map[string]any {
	for _, value := range state["comments"].([]any) {
		entry := value.(map[string]any)
		if entry["id"] == id {
			return entry
		}
	}
	return nil
}
func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return errors.New("review state directory must be private and nonsymlink")
	}
	return nil
}
func readPrivate(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1_000_000 {
		return nil, errors.New("review file must be private, regular and bounded")
	}
	return os.ReadFile(path)
}
func writePrivate(path string, value any) error {
	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	data, err := canonicalJSON(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".review-*.tmp")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
