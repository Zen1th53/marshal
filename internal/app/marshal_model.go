package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/verification"
)

// MarshalCLI runs one structured, noninteractive model turn at a time.
type MarshalCLI struct {
	designReview               bool // runtime-owned pre-approval read-only critique
	ProjectContext             func(context.Context, string, string) (string, error)
	runtime                    *Runtime
	service                    *MarshalService
	Gate                       marshal.CapabilityGate
	Settings                   marshal.Settings
	requiredTurnPreparation    *requiredMarshalTurnPreparation
	requiredTurnCapture        *requiredMarshalTurnCapture
	requiredArchitectureTarget *requiredArchitectureTurnTarget
	requiredGoalSession        string
	Provider                   string
	Binary                     string
	Dir                        string
	ProjectID                  string
	ConversationID             string
	AllowedModes               []marshal.WorkerMode
	AllowNativeException       bool
	ConfigureKnowledgeMCP      func(context.Context, string, string, []string, []string) ([]string, []string, func(), error)
}

func (m *MarshalCLI) MarshalConversationID() string {
	if m == nil {
		return ""
	}
	return m.ConversationID
}
func (m *MarshalCLI) SetMarshalConversationID(id string) {
	if m != nil {
		m.ConversationID = id
	}
}

const marshalDraftSchema = `{"type":"object","additionalProperties":false,"properties":{"tasks":{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string"},"title":{"type":"string"},"criteria":{"type":"array","items":{"type":"string"}},"paths":{"type":"array","items":{"type":"string"}},"depends_on":{"type":"array","items":{"type":"string"}},"components":{"type":"array","items":{"type":"string"}},"worker":{"type":"string"},"mode":{"type":"string","enum":["native","governed"]},"type":{"type":"string","enum":["change","inspection","verification"]},"checks":{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"command":{"type":"string"},"criteria":{"type":"array","items":{"type":"string"}}},"required":["command","criteria"]}},"instructions":{"type":"string"},"expected_output":{"type":"string"},"control":{"type":"string","enum":["strict","free"]}},"required":["id","title","criteria","paths","depends_on","components","worker","mode","type","checks","instructions","expected_output","control"]}}},"required":["tasks"]}`
const marshalReviewSchema = `{"type":"object","additionalProperties":false,"properties":{"ArchitectureVerdict":{"type":"string"},"ArchitectureMismatches":{"type":"array","items":{"type":"string"}},"Verdict":{"type":"string","enum":["accept","return","reassign","escalate"]},"Reviewer":{"type":"string"},"Reasons":{"type":"array","items":{"type":"string"}},"EvidenceRefs":{"type":"array","items":{"type":"string"}}},"required":["ArchitectureVerdict","ArchitectureMismatches","Verdict","Reviewer","Reasons","EvidenceRefs"]}`
const marshalVerifySchema = `{"type":"object","additionalProperties":false,"properties":{"head":{"type":"string"},"verdict":{"type":"string","enum":["pass","fail"]},"findings":{"type":"array","items":{"type":"string"}}},"required":["head","verdict","findings"]}`

const marshalPermissionSchema = `{"type":"object","additionalProperties":false,"properties":{"action":{"type":"string","enum":["decide"]},"id":{"type":"string"},"allow":{"type":"boolean"},"reason":{"type":"string"}},"required":["action","id","allow","reason"]}`

// marshalKnowledgeRecallNote replaces observed project knowledge that does not
// fit one Marshal request. The knowledge is unchanged and still current; the
// Marshal reads it through its MARSHAL MCP tools.
const marshalKnowledgeRecallNote = "PROJECT KNOWLEDGE (current observations): not inlined because this request is large. Read it through the MARSHAL MCP tools (project_context, memory_recall) before relying on project facts it does not state."

// marshalRoleTurnMarker opens every noninteractive Marshal role turn
// (planning, review, verification, permission decisions). The provider
// records the turn in its own session history in the project folder; the
// marker lets continuation capture decline that session deterministically
// instead of proposing it to the Marshal as a memory candidate.
const marshalRoleTurnMarker = "[MARSHAL noninteractive role turn: runtime-owned, not session memory]\n"

// marshalRoleTurnAgyPreamble opens Antigravity role turns, which precede the
// marker with their own instruction.
const marshalRoleTurnAgyPreamble = "This is a noninteractive Marshal role turn. "

func (m *MarshalCLI) turn(ctx context.Context, prompt, schema string, out any) error {
	if m == nil {
		return errors.New("Marshal CLI is unavailable")
	}
	knowledge := ""
	if m.ProjectContext != nil && schema != marshalPermissionSchema {
		var err error
		knowledge, err = m.ProjectContext(ctx, m.Dir, prompt)
		if err != nil {
			return fmt.Errorf("Marshal project knowledge: %w", err)
		}
	}
	// The marker opens the provider session's first prompt, so history capture
	// recognizes this runtime-owned turn and never asks the Marshal to review
	// it as session memory.
	base := marshalRoleTurnMarker + prompt
	priorConversation := m.ConversationID
	// Observed project knowledge is not mandatory context. When it would push
	// the request past the required envelope, the private edition hands it to
	// MCP recall instead of refusing the turn; the mandatory packet is never
	// shortened. The size is judged before any admission, because a one-shot
	// review or verification target cannot be prepared twice.
	if marshalKnowledgeViaRecall && knowledge != "" && !m.marshalKnowledgeFits(base+"\n"+knowledge, schema) {
		knowledge = marshalKnowledgeRecallNote
	}
	// Resolve and record outside folders before the sealed turn starts: the
	// evidence write must not happen inside the required-context window.
	var outsideArgs []string
	if m.Provider == "claude" {
		outsideArgs = m.outsideReadDirArgs(ctx, base)
	}
	var finishContext func() error
	for {
		prompt = base
		if knowledge != "" {
			prompt += "\n" + knowledge
		}
		prompt = marshalStructuredResultPrompt(m.Provider, prompt, schema)
		m.resetRequiredMarshalTurn()
		oneShot := m.requiredArchitectureTarget != nil
		turnCtx, admitted, finish, err := m.prepareRequiredMarshalModelTurn(ctx, prompt)
		if err == nil {
			if err = m.validateRequiredMarshalModelEnvelope(admitted, schema); err != nil {
				_ = finish()
			}
		}
		if err != nil && !oneShot && marshalKnowledgeViaRecall && errors.Is(err, projectcontext.ErrBudget) && knowledge != "" && knowledge != marshalKnowledgeRecallNote {
			knowledge = marshalKnowledgeRecallNote
			continue
		}
		if err != nil {
			return err
		}
		ctx, prompt, finishContext = turnCtx, admitted, finish
		break
	}
	defer finishContext()
	binary := m.Binary
	if binary == "" {
		binary = m.Provider
	}

	var args []string
	switch m.Provider {
	case "claude":
		args = []string{"-p", prompt, "--output-format", "json", "--json-schema", schema, "--restricted"}
		if m.ConversationID != "" {
			args = append(args, "--resume", m.ConversationID)
		}
		// A reviewer or verifier judging "copy the first line of <outside
		// file>" must be able to read that file; Claude's print mode refuses
		// reads outside its directory unless it is added.
		args = append(args, outsideArgs...)
	case "agy":
		role := "Use only the supplied task and evidence. Do not invoke any tools, including MCP tools."
		if verifierSchemaReadsFiles(schema) {
			role = "Use read-only tools to independently inspect the assigned repository and exact commit. Do not invoke MCP mutation tools."
		}
		prompt = marshalRoleTurnAgyPreamble + role + " Do not run project-startup routines or write files. Return exactly one JSON object directly in your final response, matching this schema: " + schema + "\n\n" + prompt
		args = []string{"-p", prompt, "--output-format", "json", "--json-schema", schema, "--mode", "plan"}
		if m.ConversationID != "" {
			args = append(args, "--conversation", m.ConversationID)
		}
	case "codex":
		file, err := os.CreateTemp("", "marshal-model-schema-*.json")
		if err != nil {
			return err
		}
		defer os.Remove(file.Name())
		if _, err = file.WriteString(schema); err != nil {
			file.Close()
			return err
		}
		if err = file.Close(); err != nil {
			return err
		}
		if m.ConversationID == "" {
			args = []string{"exec", "--json", "-s", "read-only", "--output-schema", file.Name(), "-"}
		} else {
			args = []string{"exec", "resume", "--json", "-c", `sandbox_mode="read-only"`, "--output-schema", file.Name(), m.ConversationID, "-"}
		}
	default:
		var ok bool
		args, ok = buildMarshalTurn(m.Provider, prompt, schema, m.ConversationID)
		if !ok {
			return fmt.Errorf("unsupported Marshal provider %q", m.Provider)
		}
	}
	var launchEnv []string
	if m.ConfigureKnowledgeMCP != nil {
		var cleanup func()
		var err error
		args, launchEnv, cleanup, err = m.ConfigureKnowledgeMCP(ctx, m.Provider, m.Dir, args, nil)
		if err != nil {
			return err
		}
		defer cleanup()
	}
	// A provider that stalls (a dropped network, a hung login refresh) must
	// not hold the run in REVIEWING forever: the turn is bounded, and on
	// expiry the provider's whole session is killed.
	timeout := marshalRoleTurnTimeout()
	cmdCtx, cancelTurn := context.WithTimeoutCause(ctx, timeout, errMarshalTurnDeadline)
	defer cancelTurn()
	cmd := exec.CommandContext(cmdCtx, binary, args...)
	detachControllingTerminal(cmd)
	killProviderGroupOnCancel(cmd)
	// A helper that inherited the output pipe must not keep Output waiting
	// after the provider itself is gone.
	cmd.WaitDelay = 5 * time.Second
	cmd.Dir = m.Dir
	if m.Provider == "codex" {
		// Codex exec and exec resume accept '-' as the stdin prompt marker.
		// Keep canonical evidence out of argv, whose individual strings are
		// bounded by the operating system.
		cmd.Stdin = strings.NewReader(prompt)
	}
	if err := m.prepareRequiredMarshalBinary(cmd.Path); err != nil {
		return err
	}
	startedAt := time.Now().UTC()
	if launchEnv != nil {
		cmd.Env = append(os.Environ(), launchEnv...)
	}
	data, err := cmd.Output()
	timedOut := err != nil && errors.Is(context.Cause(cmdCtx), errMarshalTurnDeadline)
	if !timedOut && errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
		// The provider answered and exited; only a lingering descendant
		// held the pipe open. Its answer is complete.
		err = nil
	}
	if contextErr := finishContext(); contextErr != nil {
		if timedOut {
			return errors.Join(marshalTurnTimeoutError(timeout, err), contextErr)
		}
		return contextErr
	}
	if timedOut {
		return marshalTurnTimeoutError(timeout, err)
	}
	if err != nil {
		return marshalTurnError(err, data)
	}
	if err := m.validateRequiredMarshalBinary(); err != nil {
		return err
	}
	payload, id, err := marshalCLIOutput(m.Provider, data)
	if err != nil {
		return err
	}
	payload, _ = marshalDraftFence(payload)
	if err = m.decodeRequiredMarshalResult(payload, out); err != nil {
		return fmt.Errorf("Marshal model JSON: %w", err)
	}
	m.captureRequiredMarshalTurn(prompt, schema, data, payload, priorConversation, id, startedAt, time.Now().UTC())
	if id != "" {
		m.ConversationID = id
	}
	return nil
}

// claudeResultEvent returns Claude's final result object. With verbose on
// (for example "verbose": true in the user's settings) --output-format json
// prints every event as one JSON array instead of the single result object.
func claudeResultEvent(data []byte) []byte {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return data
	}
	var events []json.RawMessage
	if json.Unmarshal(trimmed, &events) != nil {
		return data
	}
	for i := len(events) - 1; i >= 0; i-- {
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(events[i], &event) == nil && event.Type == "result" {
			return events[i]
		}
	}
	return data
}

func marshalCLIOutput(provider string, data []byte) ([]byte, string, error) {
	if payload, session, handled, err := buildMarshalOutput(provider, data); handled {
		return payload, session, err
	}
	switch provider {
	case "claude":
		var result struct {
			SessionID  string          `json:"session_id"`
			Structured json.RawMessage `json:"structured_output"`
			Result     string          `json:"result"`
		}
		if err := json.Unmarshal(claudeResultEvent(data), &result); err != nil {
			return nil, "", err
		}
		if len(result.Structured) > 0 {
			return result.Structured, result.SessionID, nil
		}
		if result.Result != "" {
			return []byte(result.Result), result.SessionID, nil
		}
	case "agy":
		var result struct {
			ConversationID string          `json:"conversation_id"`
			Status         string          `json:"status"`
			Structured     json.RawMessage `json:"structured_output"`
			Response       string          `json:"response"`
			DeniedActions  []struct {
				Action string `json:"action"`
			} `json:"denied_actions"`
			Result struct {
				ConversationID string `json:"conversation_id"`
				Response       string `json:"response"`
			} `json:"result"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, "", err
		}
		if result.Status != "" && result.Status != "SUCCESS" {
			return nil, "", fmt.Errorf("agy Marshal turn failed: %s", result.Status)
		}
		if len(result.Structured) > 0 && string(result.Structured) != "null" {
			return result.Structured, result.ConversationID, nil
		}
		if result.Result.Response != "" {
			return []byte(result.Result.Response), result.Result.ConversationID, nil
		}
		if result.Response != "" {
			return []byte(result.Response), result.ConversationID, nil
		}
		if len(result.DeniedActions) > 0 {
			return nil, "", fmt.Errorf("agy produced no structured Marshal output: %s permission denied", result.DeniedActions[0].Action)
		}
	case "codex":
		var session, text string
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			var event struct {
				Type     string `json:"type"`
				ThreadID string `json:"thread_id"`
				Item     struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"item"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
				return nil, "", err
			}
			if event.Type == "thread.started" {
				session = event.ThreadID
			}
			if event.Type == "item.completed" && event.Item.Type == "agent_message" {
				text = event.Item.Text
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, "", err
		}
		if text != "" {
			return []byte(text), session, nil
		}
	}
	return nil, "", fmt.Errorf("%s produced no structured Marshal output", strings.TrimSpace(provider))
}

// MarshalTaskDecomposition guides both planners without imposing a task count.
const MarshalTaskDecomposition = "Create one task per independent requirement or component. Split unrelated fixes and packages into separate tasks, even when one worker can do them all. Declare explicit dependencies only for shared files or a result another task needs; leave independent tasks dependency-free so they can run in parallel and fail separately. If several criteria describe one inseparable change, explain that grouping in the task instructions. "

// Shared by initial planning and amendments: verification cannot observe
// arbitrary state that existed only while a worker was running.
// MarshalCheckContract also governs the interactive planner briefing.
const MarshalCheckContract = "Checks run in a fresh checkout of the committed result in a clean sandbox with no worker environment, network or temporary files from the worker. The source is read-only; write build outputs and generated reports under $MARSHAL_BUILD_DIR or /tmp. Checks must use only repository content; commit any evidence they need into the repository. Checks are rerun after an integration merge: verify the resulting repository content (files and their contents, build/test commands), never commit history, HEAD diffs or commit structure (including git diff-tree, log, rev-list or show HEAD). MARSHAL already records changed-file scope; do not ask checks to verify that scope. "

func requestsGovernedWork(goal string) bool {
	lower := strings.ToLower(goal)
	return strings.Contains(lower, "governed")
}

func allowsNativeException(goal string) bool {
	lower := strings.ToLower(goal)
	return strings.Contains(lower, "allow native") ||
		strings.Contains(lower, "permit native") ||
		strings.Contains(lower, "native exception") ||
		strings.Contains(lower, "allow-native") ||
		strings.Contains(lower, "native allowed")
}

func (m *MarshalCLI) Draft(ctx context.Context, goal string) (MarshalDraft, error) {
	if m.ProjectID == "" {
		return MarshalDraft{}, errors.New("Marshal model has no project binding")
	}
	workers := m.availableWorkers()
	offered := m.assignableWorkers()
	if len(offered) == 0 {
		return MarshalDraft{}, errors.New("no worker CLI is available")
	}
	if requestsGovernedWork(goal) {
		m.AllowedModes = []marshal.WorkerMode{marshal.Governed}
		m.AllowNativeException = allowsNativeException(goal)
	} else {
		m.AllowedModes = nil
		m.AllowNativeException = false
	}
	var proposal marshalTaskProposal
	err := m.turn(ctx, "Return JSON tasks for this goal. Use only worker names from "+strings.Join(offered, ", ")+". "+MarshalTaskDecomposition+marshalDraftTaskPolicy()+" Each task declares type change, inspection, or verification. Change tasks must produce a change; inspection and verification may have an unchanged result only with complete passing evidence. Each task needs a unique short id, precise acceptance criteria, exact files to change, dependencies, and executable checks with explicit command and criteria fields: copy each criterion string verbatim from the task criteria into the checks that prove it; cover every criterion without paraphrasing. "+MarshalCheckContract+"Instructions must refer to the runtime-assigned worktree, never hardcode this checkout path; file tools may use absolute paths inside that assigned worktree, and must carry instructions (purpose, approach, what to leave alone) and an expected output. Each task sets control strict when the worker must follow its instructions exactly, otherwise free. Draft the tasks only; do not perform them. Keep tasks small. Goal: "+goal, marshalDraftSchema, &proposal)
	if err != nil {
		return MarshalDraft{}, err
	}
	mode := m.effectiveDraftPlanMode(ctx)
	return m.materializeWithMode(proposal, "", 1, workers, mode)
}
func (m *MarshalCLI) Review(ctx context.Context, task marshal.Task, handin marshal.HandIn, control marshal.Control) (marshal.Review, error) {
	var out marshal.Review
	err := m.turn(ctx, m.marshalReviewPrompt(task, handin, control), marshalReviewSchema, &out)
	return out, err
}
func (m *MarshalCLI) Amend(ctx context.Context, run marshal.Run, reason string) (MarshalDraft, error) {
	workers := m.availableWorkers()
	var proposal marshalTaskProposal
	input, target, err := marshalAmendPacket(run, reason)
	if err != nil {
		return MarshalDraft{}, err
	}
	newTaskModes := "New tasks prefer governed for codex and claude; other workers use native."
	if privateAGYEnabled() {
		newTaskModes = "New tasks prefer governed for codex and claude; opencode and agy also support governed when requested and admitted by a current verified profile."
	}
	preamble := "Return the complete amended JSON task list. "
	if target >= 0 {
		preamble = "Return the amended target task and any new tasks needed. The runtime preserves every other existing task unchanged. "
	}
	err = m.turn(ctx, preamble+"Use only workers "+strings.Join(m.assignableWorkers(), ", ")+". Preserve each existing task mode and type unless the operator requests a change. Copy each existing task's criteria, checks and check criteria verbatim unless the operator asks to change them; each check criterion must be the exact text of one of its task's criteria. Declare type change, inspection or verification for new tasks. "+newTaskModes+" "+MarshalTaskDecomposition+MarshalCheckContract+"Amendment packet: "+string(input), marshalDraftSchema, &proposal)
	if err != nil {
		return MarshalDraft{}, err
	}
	if target >= 0 {
		found := false
		original := proposalFromRun(run)
		for _, item := range proposal.Tasks {
			if item.ID == run.Tasks[target].PlanTaskID {
				if found {
					return MarshalDraft{}, fmt.Errorf("amendment repeats target task %q", item.ID)
				}
				original.Tasks[target] = item
				found = true
				continue
			}
			for _, existing := range run.Tasks {
				if item.ID == existing.PlanTaskID {
					return MarshalDraft{}, fmt.Errorf("amendment may not change non-target task %q", item.ID)
				}
			}
			original.Tasks = append(original.Tasks, item)
		}
		if !found {
			return MarshalDraft{}, fmt.Errorf("amendment omitted target task %q", run.Tasks[target].PlanTaskID)
		}
		proposal = original
	}
	bindAmendCriteria(&proposal, run.Tasks)
	return m.materializeWithMode(proposal, run.PlanID, run.PlanVersion, workers, m.amendPlanMode(ctx, run))
}

type marshalTaskProposal struct {
	Tasks []marshalProposedTask `json:"tasks"`
}

type marshalProposedTask struct {
	ID         string                 `json:"id"`
	Title      string                 `json:"title"`
	Criteria   []string               `json:"criteria"`
	Paths      []string               `json:"paths"`
	DependsOn  []string               `json:"depends_on"`
	Components []string               `json:"components"`
	Worker     string                 `json:"worker"`
	Mode       marshal.WorkerMode     `json:"mode"`
	Type       marshal.TaskType       `json:"type"`
	Checks     []marshalProposedCheck `json:"checks"`
	// Instructions and ExpectedOutput become part of the approved plan
	// task. Strict control requires instructions for every task.
	Instructions   string `json:"instructions,omitempty"`
	ExpectedOutput string `json:"expected_output,omitempty"`
	// Control is the per-task control level: "strict" or "free". Empty means
	// free. Any other value refuses the draft.
	Control string `json:"control,omitempty"`
}

type marshalProposedCheck struct {
	Command  string   `json:"command"`
	Criteria []string `json:"criteria"`
}

// MarshalWorkers lists the worker CLIs a Marshal of the given provider may
// assign tasks to.
func MarshalWorkers(provider string) []string {
	return (&MarshalCLI{Provider: provider}).assignableWorkers()
}

// AGYAssignable reports whether the Marshal may give a task to agy in this
// edition on this host.
func AGYAssignable() bool { return draftAssignableWorker("agy") }

// marshalDraftFence unwraps one Markdown code fence only when the whole
// trimmed reply is that fence. Prose around it, multiple fences, an
// unterminated fence or trailing text stay untouched and are rejected by the
// normal strict JSON parsing.
func marshalDraftFence(data []byte) ([]byte, bool) {
	trimmed := bytes.TrimSpace(data)
	if !bytes.HasPrefix(trimmed, []byte("```")) {
		return data, false
	}
	lineEnd := bytes.IndexByte(trimmed, '\n')
	if lineEnd < 0 {
		return data, false
	}
	switch strings.TrimSpace(string(trimmed[:lineEnd])) {
	case "```", "```json":
	default:
		return data, false
	}
	body := trimmed[lineEnd+1:]
	lastLine := bytes.LastIndexByte(body, '\n')
	if lastLine < 0 || strings.TrimSpace(string(body[lastLine+1:])) != "```" {
		return data, false
	}
	content := body[:lastLine]
	for _, line := range bytes.Split(content, []byte("\n")) {
		if strings.HasPrefix(strings.TrimSpace(string(line)), "```") {
			return data, false
		}
	}
	return content, true
}

// DraftFromProposal turns the task list an interactive Marshal session wrote
// into a draft. The model supplies only the tasks, in the same form a
// headless draft turn returns; the runtime builds the plan around them. Plan
// identity, version, constitution binding and the graph digest are computed
// here, because a model cannot be relied on to reproduce them.
func (s *MarshalService) DraftFromProposal(data []byte, provider string) (MarshalDraft, error) {
	var proposal marshalTaskProposal
	payload, fenced := marshalDraftFence(data)
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&proposal); err != nil {
		return MarshalDraft{}, decodeDraftRejection(err)
	}
	if fenced {
		var extra json.RawMessage
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			return MarshalDraft{}, rejectDraft(errors.New("invalid Marshal draft JSON: trailing data after fenced block"), "draft", "text follows the fenced JSON block")
		}
	}
	m := &MarshalCLI{
		Provider:  provider,
		ProjectID: string(s.CanonicalPlanProjectID()),
		runtime:   s.runtime,
		service:   s,
		Gate:      s.EffectiveGate(),
	}
	if s != nil && s.Store != nil && s.ProjectID != "" {
		if settings, err := s.Store.GetMarshalSettings(context.Background(), s.ProjectID); err == nil {
			m.Settings = settings.Value
		}
	}
	mode := m.effectiveDraftPlanMode(context.Background())
	return m.materializeWithMode(proposal, "", 1, m.availableWorkers(), mode)
}

func (m *MarshalCLI) availableWorkers() []string {
	var workers []string
	for _, provider := range []string{"codex", "claude", "agy", "opencode"} {
		if _, err := exec.LookPath(provider); err == nil {
			workers = append(workers, provider)
		}
	}
	return workers
}

// assignableWorkers are the installed workers a draft may name in this
// edition: the ones the Marshal is offered. A worker that is installed but
// can take no task here (agy outside private Linux in the default edition)
// is left out, so the Marshal is never told to use a worker the runtime then
// refuses. Admission (materialize) still checks the installed list, so a
// draft that names such a worker gets the specific refusal.
func (m *MarshalCLI) assignableWorkers() []string {
	var out []string
	for _, worker := range m.availableWorkers() {
		if draftAssignableWorker(worker) {
			out = append(out, worker)
		}
	}
	return out
}

func (m *MarshalCLI) effectiveDraftPlanMode(ctx context.Context) plan.Mode {
	if m == nil {
		return plan.ModeStandard
	}
	settings := m.Settings
	if m.service != nil && m.service.Store != nil && m.service.ProjectID != "" {
		if rec, err := m.service.Store.GetMarshalSettings(ctx, m.service.ProjectID); err == nil {
			settings = rec.Value
		}
	} else if m.runtime != nil && m.runtime.store != nil {
		if rec, err := m.runtime.store.GetMarshalSettings(ctx, m.runtime.ProjectID()); err == nil {
			settings = rec.Value
		}
	}
	var gate marshal.CapabilityGate
	if m.service != nil && m.service.Gate != nil {
		gate = m.service.EffectiveGate()
	} else {
		gate = m.Gate
		if gate != nil {
			if _, live := gate.(interface{ ExecutionEnabled() bool }); !live && m.runtime != nil && m.runtime.store != nil {
				if s := m.runtime.Marshal(); s != nil {
					gate = projectExecutionGate{CapabilityGate: gate, service: s}
				}
			}
		}
	}
	return EffectiveDraftPlanMode(gate, settings)
}

func (m *MarshalCLI) amendPlanMode(ctx context.Context, run marshal.Run) plan.Mode {
	if m != nil && m.service != nil && m.service.Store != nil && run.PlanID != "" && run.PlanVersion > 0 {
		if existing, err := m.service.Store.GetPlan(ctx, run.PlanID, run.PlanVersion); err == nil && existing.Mode != "" {
			return existing.Mode
		}
	}
	if m != nil && m.runtime != nil && m.runtime.store != nil && run.PlanID != "" && run.PlanVersion > 0 {
		if existing, err := m.runtime.store.GetPlan(ctx, run.PlanID, run.PlanVersion); err == nil && existing.Mode != "" {
			return existing.Mode
		}
	}
	if run.Process05Bound {
		if run.Process05Parallel {
			return plan.ModeUltra
		}
		return plan.ModeStandard
	}
	if run.Tier == marshal.Ultra && isExtremeBuild {
		return plan.ModeUltra
	}
	return plan.ModeStandard
}

func (m *MarshalCLI) materialize(proposal marshalTaskProposal, planID string, version int64, workers []string) (MarshalDraft, error) {
	return m.materializeWithMode(proposal, planID, version, workers, m.effectiveDraftPlanMode(context.Background()))
}

func (m *MarshalCLI) materializeWithMode(proposal marshalTaskProposal, planID string, version int64, workers []string, mode plan.Mode) (MarshalDraft, error) {
	if len(proposal.Tasks) == 0 || !projectid.ID(m.ProjectID).Valid() {
		err := errors.New("invalid Marshal proposal or project")
		if len(proposal.Tasks) == 0 {
			return MarshalDraft{}, rejectDraft(err, "tasks", "at least one task is required")
		}
		return MarshalDraft{}, rejectDraft(err, "project", "the runtime has no valid project identity for this draft (not a draft error)")
	}
	if planID == "" {
		var err error
		planID, err = model.NewID("PLAN-")
		if err != nil {
			return MarshalDraft{}, err
		}
	}
	if mode != plan.ModeUltra {
		mode = plan.ModeStandard
	}
	p := plan.ExecutionPlan{ID: planID, ProjectID: projectid.ID(m.ProjectID), Goal: plan.GoalBinding{GoalID: "GOAL-" + planID, Revision: 1}, Version: version, State: plan.StateReady, Mode: mode, ConstitutionVersion: constitution.Current, Checks: map[string][]string{}}
	if m != nil && len(m.AllowedModes) > 0 {
		for _, am := range m.AllowedModes {
			p.HardConstraints = append(p.HardConstraints, "allowed-mode:"+string(am))
		}
		if m.AllowNativeException {
			p.HardConstraints = append(p.HardConstraints, "allow-native-exception")
		}
	}
	draft := MarshalDraft{Plan: p}
	for i, item := range proposal.Tasks {
		at := func(field string) string { return fmt.Sprintf("tasks[%d].%s", i, field) }
		allowed := false
		for _, worker := range workers {
			allowed = allowed || item.Worker == worker
		}
		if !allowed || item.ID == "" || item.Title == "" || len(item.Criteria) == 0 || len(item.Paths) == 0 || len(item.Checks) == 0 {
			err := fmt.Errorf("invalid proposed task %q", item.ID)
			switch {
			case !allowed && knownWorkerName(item.Worker) != "":
				return MarshalDraft{}, rejectDraft(err, at("worker"), "%s is not installed here; use one of: %s", item.Worker, strings.Join(workers, ", "))
			case !allowed:
				return MarshalDraft{}, rejectDraft(err, at("worker"), "not an installed worker; use one of: %s", strings.Join(workers, ", "))
			case item.ID == "":
				return MarshalDraft{}, rejectDraft(err, at("id"), "required")
			case item.Title == "":
				return MarshalDraft{}, rejectDraft(err, at("title"), "required")
			case len(item.Criteria) == 0:
				return MarshalDraft{}, rejectDraft(err, at("criteria"), "required: give the task its success criterion")
			case len(item.Paths) == 0:
				return MarshalDraft{}, rejectDraft(err, at("paths"), "required: name exact project-relative files the task changes")
			}
			return MarshalDraft{}, rejectDraft(err, at("checks"), "required: give at least one executable check")
		}
		componentSeen := make(map[string]bool, len(item.Components))
		for j, id := range item.Components {
			if !marshalIdentifier(id) || componentSeen[id] {
				return MarshalDraft{}, rejectDraft(fmt.Errorf("invalid architecture part %q in task %q", id, item.ID), at(fmt.Sprintf("components[%d]", j)), "not an architecture part identifier, or repeated")
			}
			componentSeen[id] = true
		}
		mode := item.Mode
		if mode == "" {
			mode = marshal.Native
			if item.Worker == "codex" || item.Worker == "claude" {
				mode = marshal.Governed
			}
		}
		if mode != marshal.Native && mode != marshal.Governed {
			return MarshalDraft{}, rejectDraft(fmt.Errorf("invalid task mode %q", mode), at("mode"), "must be native or governed")
		}
		if mode == marshal.Governed && item.Worker != "codex" && item.Worker != "claude" && item.Worker != "opencode" && !(item.Worker == "agy" && privateAGYEnabled()) {
			rule := "worker %s does not support governed mode on this host"
			if !draftAssignableWorker(item.Worker) {
				rule += "; it cannot take tasks here, so give this task to another worker"
			} else {
				rule += "; use mode native or another worker"
			}
			return MarshalDraft{}, rejectDraft(fmt.Errorf("worker %s does not support governed mode", item.Worker), at("mode"), rule, item.Worker)
		}
		if mode == marshal.Native && m != nil && slices.Contains(m.AllowedModes, marshal.Governed) && !m.AllowNativeException {
			return MarshalDraft{}, rejectDraft(fmt.Errorf("task %s uses native mode in governed plan without explicit exception", item.ID), at("mode"), "native mode in a governed plan needs an explicit exception in the goal")
		}
		kind := item.Type
		if kind == "" {
			kind = marshal.TaskChange
		}
		if kind != marshal.TaskChange && kind != marshal.TaskInspection && kind != marshal.TaskVerification {
			return MarshalDraft{}, rejectDraft(errors.New("invalid task type"), at("type"), "must be change, inspection or verification")
		}
		control := marshal.Control(item.Control)
		if control != "" && control != marshal.ControlStrict && control != marshal.ControlFree {
			return MarshalDraft{}, rejectDraft(fmt.Errorf("task %s: control must be strict or free", item.ID), at("control"), "must be strict or free")
		}
		draft.Plan.Tasks = append(draft.Plan.Tasks, plan.Task{ID: item.ID, Title: item.Title, Criteria: item.Criteria, Paths: item.Paths, DependsOn: item.DependsOn, Type: string(kind), Mutating: true, Weight: 1, Instructions: item.Instructions, Control: item.Control, ExpectedOutput: item.ExpectedOutput})
		task := marshal.Task{Type: kind, PlanTaskID: item.ID, Title: item.Title, Worker: item.Worker, Mode: mode, Criteria: item.Criteria, Files: item.Paths, DependsOn: item.DependsOn, Components: append([]string(nil), item.Components...), Instructions: item.Instructions, Control: control, ExpectedOutput: item.ExpectedOutput}
		for _, check := range item.Checks {
			task.Checks = append(task.Checks, marshal.Check{Command: check.Command, Criteria: check.Criteria})
			draft.Plan.Checks[item.ID] = append(draft.Plan.Checks[item.ID], check.Command)
		}
		draft.Tasks = append(draft.Tasks, task)
	}
	graph, err := plan.BuildGraph(draft.Plan.Tasks)
	if err != nil {
		return MarshalDraft{}, placeDraftViolation(err, proposal)
	}
	draft.Plan.Graph = graph
	if err := validateDraft(draft); err != nil {
		return MarshalDraft{}, placeDraftViolation(err, proposal)
	}
	if warnings := DraftFileScopeWarnings(draft.Tasks); len(warnings) > 0 {
		draft.Plan.MarshalWarnings = append(draft.Plan.MarshalWarnings, warnings...)
	}
	return draft, nil
}

// Verify independently inspects the integrated tree and binds its verdict to
// the exact commit that the runtime checked. The CLI turn is read only.
func (m *MarshalCLI) Verify(ctx context.Context, run marshal.Run, head string, session verification.Session) error {
	_, err := m.VerifyEvidence(ctx, run, head, session)
	return err
}

func (m *MarshalCLI) VerifyEvidence(ctx context.Context, run marshal.Run, head string, session verification.Session) (marshal.VerifierEvidence, error) {
	// The verifier starts clean: a CLI that already holds a conversation
	// is not a fresh session, and its evidence says so.
	record := marshal.VerifierEvidence{Reviewer: "verifier:" + m.Provider, Provider: m.Provider, Commit: head, Verdict: "incomplete", FreshSession: m.ConversationID == ""}
	var verdict struct {
		Head     string   `json:"head"`
		Verdict  string   `json:"verdict"`
		Findings []string `json:"findings"`
	}
	input, err := verifierInput(run, head, session)
	if err != nil {
		return record, err
	}
	sum := sha256.Sum256(input)
	record.InputDigest = "sha256:" + hex.EncodeToString(sum[:])
	if err := m.turn(ctx, marshalRuntimeProofInstruction(session)+"Perform independent pre-delivery verification of the current integration tree. The target branch intentionally remains unchanged until this verification passes and the runtime applies close authorization. Verify the committed task artifacts and all goal constraints on those artifacts; do not require the subsequent delivery or closed state to have happened already. Compare HEAD with the supplied commit, review task changes against their criteria, and return JSON with head, verdict (pass or fail), and findings. Fail if evidence is insufficient: "+string(input), marshalVerifySchema, &verdict); err != nil {
		return record, err
	}
	record.SessionID = m.ConversationID
	record.Commit, record.Verdict, record.Findings = verdict.Head, verdict.Verdict, verdict.Findings
	if verdict.Head != head || verdict.Verdict != "pass" {
		return record, fmt.Errorf("independent verification refused commit %s: verdict=%q findings=%v", head, verdict.Verdict, verdict.Findings)
	}
	return record, nil
}

func verifierInput(run marshal.Run, head string, session verification.Session) ([]byte, error) {
	return json.Marshal(struct {
		Stage   string
		Head    string
		Goal    string
		Tasks   []marshal.Task
		Session verification.Session
	}{"pre_delivery", head, run.GoalBinding, run.Tasks, session})
}

func verifierInputDigest(run marshal.Run, head string, session verification.Session) string {
	input, err := verifierInput(run, head, session)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(input)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// defaultMarshalRoleTurnTimeout bounds one noninteractive Marshal role turn
// (planning, review, verification, permission decisions).
const defaultMarshalRoleTurnTimeout = 15 * time.Minute

// marshalRoleTurnTimeoutEnv overrides the bound with a time.ParseDuration
// value such as "30m"; a missing, malformed or non-positive value keeps the
// default.
const marshalRoleTurnTimeoutEnv = "MARSHAL_ROLE_TURN_TIMEOUT"

// ErrMarshalTurnTimeout marks a Marshal role turn the runtime stopped because
// the provider did not answer in time. Nothing was decided, so the run stops
// with the task where it was and /marshal resume repeats the turn.
var ErrMarshalTurnTimeout = errors.New("Marshal turn timed out")

// errMarshalTurnDeadline is the cancellation cause of the turn's own bound,
// telling it apart from a cancellation or deadline of the caller.
var errMarshalTurnDeadline = errors.New("Marshal role turn deadline")

func marshalRoleTurnTimeout() time.Duration {
	if value := strings.TrimSpace(os.Getenv(marshalRoleTurnTimeoutEnv)); value != "" {
		if d, err := time.ParseDuration(value); err == nil && d > 0 {
			return d
		}
	}
	return defaultMarshalRoleTurnTimeout
}

func marshalTurnTimeoutError(timeout time.Duration, err error) error {
	return fmt.Errorf("%w after %s; the provider was stopped and nothing was decided; /marshal resume to retry the turn (set %s to change the limit): %w", ErrMarshalTurnTimeout, timeout, marshalRoleTurnTimeoutEnv, err)
}

// marshalTurnError names a failed model turn with the provider's own output.
// A provider may report its quota or rate limit only on stdout (Codex exits 1
// with an empty stderr), and that message is what lets a limited reviewer be
// recognised and skipped.
func marshalTurnError(err error, stdout []byte) error {
	var stderr []byte
	if exit, ok := err.(*exec.ExitError); ok {
		stderr = exit.Stderr
	}
	detail := strings.TrimSpace(string(stderr) + "\n" + string(stdout))
	if detail == "" {
		return fmt.Errorf("Marshal model turn: %w", err)
	}
	if len(detail) > 4096 {
		detail = strings.ToValidUTF8(detail[:4096], "") + " [truncated]"
	}
	return fmt.Errorf("Marshal model turn: %w: %s", err, detail)
}
