package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/verification"
)

// MarshalCLI runs one structured, noninteractive model turn at a time.
type MarshalCLI struct {
	Provider       string
	Binary         string
	Dir            string
	ProjectID      string
	ConversationID string
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

const marshalDraftSchema = `{"type":"object","additionalProperties":false,"properties":{"tasks":{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string"},"title":{"type":"string"},"criteria":{"type":"array","items":{"type":"string"}},"paths":{"type":"array","items":{"type":"string"}},"depends_on":{"type":"array","items":{"type":"string"}},"worker":{"type":"string"},"checks":{"type":"array","items":{"type":"string"}}},"required":["id","title","criteria","paths","depends_on","worker","checks"]}}},"required":["tasks"]}`
const marshalReviewSchema = `{"type":"object","additionalProperties":false,"properties":{"Verdict":{"type":"string","enum":["accept","return","reassign","escalate"]},"Reviewer":{"type":"string"},"Reasons":{"type":"array","items":{"type":"string"}},"EvidenceRefs":{"type":"array","items":{"type":"string"}}},"required":["Verdict","Reviewer","Reasons","EvidenceRefs"]}`
const marshalVerifySchema = `{"type":"object","additionalProperties":false,"properties":{"head":{"type":"string"},"verdict":{"type":"string","enum":["pass","fail"]},"findings":{"type":"array","items":{"type":"string"}}},"required":["head","verdict","findings"]}`

func (m *MarshalCLI) turn(ctx context.Context, prompt, schema string, out any) error {
	if m == nil {
		return errors.New("Marshal CLI is unavailable")
	}
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
	case "agy":
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
			args = []string{"exec", "--json", "-s", "read-only", "--output-schema", file.Name(), prompt}
		} else {
			args = []string{"exec", "resume", "--json", "-c", `sandbox_mode="read-only"`, "--output-schema", file.Name(), m.ConversationID, prompt}
		}
	default:
		return fmt.Errorf("unsupported Marshal provider %q", m.Provider)
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = m.Dir
	data, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok && len(exit.Stderr) > 0 {
			detail := strings.TrimSpace(string(exit.Stderr) + "\n" + string(data))
			if len(detail) > 4096 {
				detail = detail[:4096] + " [truncated]"
			}
			return fmt.Errorf("Marshal model turn: %w: %s", err, detail)
		}
		return fmt.Errorf("Marshal model turn: %w", err)
	}
	payload, id, err := marshalCLIOutput(m.Provider, data)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("Marshal model JSON: %w", err)
	}
	if id != "" {
		m.ConversationID = id
	}
	return nil
}

func marshalCLIOutput(provider string, data []byte) ([]byte, string, error) {
	switch provider {
	case "claude":
		var result struct {
			SessionID  string          `json:"session_id"`
			Structured json.RawMessage `json:"structured_output"`
			Result     string          `json:"result"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
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
			ConversationID string `json:"conversation_id"`
			Response       string `json:"response"`
			Result         struct {
				ConversationID string `json:"conversation_id"`
				Response       string `json:"response"`
			} `json:"result"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, "", err
		}
		if result.Result.Response != "" {
			return []byte(result.Result.Response), result.Result.ConversationID, nil
		}
		if result.Response != "" {
			return []byte(result.Response), result.ConversationID, nil
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

func (m *MarshalCLI) Draft(ctx context.Context, goal string) (MarshalDraft, error) {
	if m.ProjectID == "" {
		return MarshalDraft{}, errors.New("Marshal model has no project binding")
	}
	workers := m.availableWorkers()
	if len(workers) == 0 {
		return MarshalDraft{}, errors.New("no separate worker CLI is available")
	}
	var proposal marshalTaskProposal
	err := m.turn(ctx, "Return JSON tasks for this goal. Use only worker names from "+strings.Join(workers, ", ")+". Each task needs a unique short id, precise acceptance criteria, exact files to change, dependencies, and executable checks. Keep tasks small. Goal: "+goal, marshalDraftSchema, &proposal)
	if err != nil {
		return MarshalDraft{}, err
	}
	return m.materialize(proposal, "", 1, workers)
}
func (m *MarshalCLI) Review(ctx context.Context, task marshal.Task, handin marshal.HandIn) (marshal.Review, error) {
	var out marshal.Review
	input, _ := json.Marshal(struct {
		Task   marshal.Task
		HandIn marshal.HandIn
	}{task, handin})
	err := m.turn(ctx, "Review this hand-in and return a JSON verdict: "+string(input), marshalReviewSchema, &out)
	return out, err
}
func (m *MarshalCLI) Amend(ctx context.Context, run marshal.Run, reason string) (MarshalDraft, error) {
	workers := m.availableWorkers()
	var proposal marshalTaskProposal
	input, _ := json.Marshal(run)
	err := m.turn(ctx, "Return the complete amended JSON task list. Use only workers "+strings.Join(workers, ", ")+". Reason: "+reason+". Current run: "+string(input), marshalDraftSchema, &proposal)
	if err != nil {
		return MarshalDraft{}, err
	}
	return m.materialize(proposal, run.PlanID, run.PlanVersion, workers)
}

type marshalTaskProposal struct {
	Tasks []struct {
		ID        string   `json:"id"`
		Title     string   `json:"title"`
		Criteria  []string `json:"criteria"`
		Paths     []string `json:"paths"`
		DependsOn []string `json:"depends_on"`
		Worker    string   `json:"worker"`
		Checks    []string `json:"checks"`
	} `json:"tasks"`
}

func (m *MarshalCLI) availableWorkers() []string {
	var workers []string
	for _, provider := range []string{"codex", "claude", "agy", "opencode"} {
		if provider == m.Provider {
			continue
		}
		if _, err := exec.LookPath(provider); err == nil {
			workers = append(workers, provider)
		}
	}
	return workers
}

func (m *MarshalCLI) materialize(proposal marshalTaskProposal, planID string, version int64, workers []string) (MarshalDraft, error) {
	if len(proposal.Tasks) == 0 || !projectid.ID(m.ProjectID).Valid() {
		return MarshalDraft{}, errors.New("invalid Marshal proposal or project")
	}
	if planID == "" {
		var err error
		planID, err = model.NewID("PLAN-")
		if err != nil {
			return MarshalDraft{}, err
		}
	}
	p := plan.ExecutionPlan{ID: planID, ProjectID: projectid.ID(m.ProjectID), Goal: plan.GoalBinding{GoalID: "GOAL-" + planID, Revision: 1}, Version: version, State: plan.StateReady, Mode: plan.ModeStandard, ConstitutionVersion: constitution.Current, Checks: map[string][]string{}}
	draft := MarshalDraft{Plan: p}
	for _, item := range proposal.Tasks {
		allowed := false
		for _, worker := range workers {
			allowed = allowed || item.Worker == worker
		}
		if !allowed || item.ID == "" || item.Title == "" || len(item.Criteria) == 0 || len(item.Paths) == 0 || len(item.Checks) == 0 {
			return MarshalDraft{}, fmt.Errorf("invalid proposed task %q", item.ID)
		}
		draft.Plan.Tasks = append(draft.Plan.Tasks, plan.Task{ID: item.ID, Title: item.Title, Criteria: item.Criteria, Paths: item.Paths, DependsOn: item.DependsOn, Mutating: true, Weight: 1})
		task := marshal.Task{PlanTaskID: item.ID, Worker: item.Worker, Mode: marshal.Native, Criteria: item.Criteria, Files: item.Paths, DependsOn: item.DependsOn}
		for _, command := range item.Checks {
			task.Checks = append(task.Checks, marshal.Check{Command: command, Criteria: item.Criteria})
		}
		draft.Tasks = append(draft.Tasks, task)
		draft.Plan.Checks[item.ID] = item.Checks
	}
	graph, err := plan.BuildGraph(draft.Plan.Tasks)
	if err != nil {
		return MarshalDraft{}, err
	}
	draft.Plan.Graph = graph
	if err := validateDraft(draft); err != nil {
		return MarshalDraft{}, err
	}
	return draft, nil
}

// Verify independently inspects the integrated tree and binds its verdict to
// the exact commit that the runtime checked. The CLI turn is read only.
func (m *MarshalCLI) Verify(ctx context.Context, run marshal.Run, head string, session verification.Session) error {
	var verdict struct {
		Head     string   `json:"head"`
		Verdict  string   `json:"verdict"`
		Findings []string `json:"findings"`
	}
	input, err := json.Marshal(struct {
		Head   string
		Goal   string
		Tasks  []marshal.Task
		Checks map[string]verification.Status
	}{head, run.GoalBinding, run.Tasks, session.RequiredChecks})
	if err != nil {
		return err
	}
	if err := m.turn(ctx, "Independently inspect the current git tree. Compare HEAD with the supplied commit, review task changes against their criteria, and return JSON with head, verdict (pass or fail), and findings. Fail if evidence is insufficient: "+string(input), marshalVerifySchema, &verdict); err != nil {
		return err
	}
	if verdict.Head != head || verdict.Verdict != "pass" {
		return fmt.Errorf("independent verification refused commit %s: verdict=%q findings=%v", head, verdict.Verdict, verdict.Findings)
	}
	return nil
}
