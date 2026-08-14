package policy

import (
	"fmt"
	"os"

	"github.com/Zen1th53/marshal/internal/model"
	"go.yaml.in/yaml/v3"
)

type roleRules struct {
	May    []string `yaml:"may"`
	MayNot []string `yaml:"may_not"`
}

type document struct {
	Version             int                  `yaml:"version"`
	Principle           string               `yaml:"principle"`
	Default             map[string]string    `yaml:"default"`
	Roles               map[string]roleRules `yaml:"roles"`
	DangerousOperations []string             `yaml:"dangerous_operations"`
}

type Engine struct {
	document document
}

func Load(path string) (*Engine, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open capability policy: %w", err)
	}
	defer file.Close()
	var doc document
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode capability policy: %w", err)
	}
	if err := validateDocument(doc); err != nil {
		return nil, err
	}
	return &Engine{document: doc}, nil
}

func validateDocument(doc document) error {
	if doc.Version != 1 || doc.Principle == "" {
		return fmt.Errorf("%w: unsupported or incomplete capability policy", model.ErrInvalid)
	}
	required := map[string][]string{
		"filesystem_read":       {"allow"},
		"filesystem_write":      {"task_scoped"},
		"shell_execute":         {"task_scoped"},
		"network_read":          {"task_scoped"},
		"network_write":         {"deny_unless_required"},
		"git_read":              {"allow"},
		"git_commit":            {"task_scoped"},
		"git_history_rewrite":   {"deny"},
		"dependency_add":        {"architect_or_policy_review"},
		"secrets_read":          {"deny_unless_explicit"},
		"secrets_write":         {"deny_unless_explicit"},
		"production_read":       {"deny_unless_explicit"},
		"production_write":      {"deny"},
		"deploy":                {"approval_required"},
		"destructive_operation": {"approval_required"},
		"external_upload":       {"deny_unless_explicit"},
	}
	for key, allowed := range required {
		value, ok := doc.Default[key]
		if !ok || !contains(allowed, value) {
			return fmt.Errorf("%w: capability policy key %s is missing or unsupported", model.ErrInvalid, key)
		}
	}
	for key := range doc.Default {
		if _, ok := required[key]; !ok {
			return fmt.Errorf("%w: unknown capability policy key %s", model.ErrInvalid, key)
		}
	}
	knownRoles := make(map[string]struct{}, 5)
	for _, role := range []model.Role{
		model.RoleOrchestrator, model.RoleArchitect, model.RoleDeveloper, model.RoleQA, model.RoleAppSec,
	} {
		knownRoles[string(role)] = struct{}{}
		if _, ok := doc.Roles[string(role)]; !ok {
			return fmt.Errorf("%w: role policy %s is missing", model.ErrInvalid, role)
		}
	}
	for role := range doc.Roles {
		if _, ok := knownRoles[role]; !ok {
			return fmt.Errorf("%w: unknown role policy %s", model.ErrInvalid, role)
		}
	}
	return nil
}

func (e *Engine) Decide(input model.PolicyInput) model.PolicyDecision {
	if input.AgentID == "" || input.SessionID == "" || !input.Role.Valid() {
		return decision(model.Deny, "identity", "privileged operation requires a bound agent session")
	}
	if input.Production {
		return decision(model.Deny, "default.production_write", "production mutation is denied")
	}
	switch input.Operation {
	case model.FilesystemRead:
		return decision(model.Allow, "default.filesystem_read", "filesystem read is allowed")
	case model.FilesystemWrite:
		return e.taskScoped(input, "default.filesystem_write")
	case model.ShellExecute:
		return e.taskScoped(input, "default.shell_execute")
	case model.NetworkAccess:
		if input.Required && input.TaskOwned {
			return decision(model.Allow, "default.network_write", "task-required network access is allowed")
		}
		return decision(model.Deny, "default.network_write", "network access was not established as task-required")
	case model.GitCommit:
		return e.taskScoped(input, "default.git_commit")
	case model.GitPush:
		return decision(model.Deny, "default.external_upload", "Git push is an external mutation not exposed to workers")
	case model.GitHistoryRewrite:
		return decision(model.Deny, "default.git_history_rewrite", "history rewrite is denied")
	case model.SecretRead:
		if input.ExplicitPermission && input.TaskOwned {
			return decision(model.Allow, "default.secrets_read", "explicit task-scoped secret access is allowed")
		}
		return decision(model.Deny, "default.secrets_read", "secret access lacks explicit permission")
	case model.ExternalUpload:
		if input.ExplicitPermission && input.TaskOwned {
			return decision(model.Allow, "default.external_upload", "explicit task-scoped upload is allowed")
		}
		return decision(model.Deny, "default.external_upload", "external upload lacks explicit permission")
	case model.Deploy:
		if input.ApprovalValid {
			return decision(model.Allow, "default.deploy", "deployment approval is valid")
		}
		return decision(model.RequireApproval, "default.deploy", "deployment requires approval")
	case model.DestructiveOperation:
		if input.ApprovalValid {
			return decision(model.Allow, "default.destructive_operation", "destructive-operation approval is valid")
		}
		return decision(model.RequireApproval, "default.destructive_operation", "destructive operation requires approval")
	default:
		return decision(model.Deny, "default.deny", "operation is not defined by policy")
	}
}

func (e *Engine) taskScoped(input model.PolicyInput, rule string) model.PolicyDecision {
	if input.TaskID != "" && input.TaskOwned && input.TargetInScope {
		return decision(model.Allow, rule, "operation is bound to the owned task scope")
	}
	return decision(model.Deny, rule, "operation is outside the owned task scope")
}

func Enforce(engine *Engine, input model.PolicyInput, operation func() error) error {
	result := engine.Decide(input)
	switch result.Decision {
	case model.Allow:
		return operation()
	case model.RequireApproval:
		return fmt.Errorf("%w: %s", model.ErrApprovalRequired, result.Reason)
	default:
		return fmt.Errorf("%w: %s", model.ErrPolicyDenied, result.Reason)
	}
}

func decision(value model.Decision, rule, reason string) model.PolicyDecision {
	return model.PolicyDecision{Decision: value, Rule: rule, Reason: reason}
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
