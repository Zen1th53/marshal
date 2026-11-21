package architecturereview

import (
	"encoding/json"
	"fmt"
	"math/rand"
)

// marshalShapeFixture is a reconstruction of the owner's 17-part project
// architecture in the contract shape (name = id, covers, files,
// source_contract). Every object is "new", as on the owner's first draft;
// mixed turns some parts and connections into the other change kinds.
func marshalShapeFixture(mixed bool) json.RawMessage {
	type part struct {
		id, parent, does string
		covers, files    []string
	}
	parts := []part{
		{"cli", "", "Parses commands and starts the runtime", []string{"R1"}, []string{"cmd/marshal", "internal/cli"}},
		{"tui-board", "", "Terminal control centre for the owner", []string{"R2", "R3"}, []string{"internal/tui"}},
		{"web-board", "", "Local browser board", []string{"R3"}, []string{"internal/tui/web"}},
		{"architecture-review", "", "Owner reviews and accepts the architecture in the browser", []string{"R4", "R5"}, []string{"internal/architecturereview"}},
		{"review-server", "architecture-review", "Serves the page and validates every owner action", []string{"R4"}, []string{"internal/architecturereview/server.go"}},
		{"review-page", "architecture-review", "Embedded browser page", []string{"R5"}, []string{"internal/architecturereview/review.html"}},
		{"layout-engine", "architecture-review", "Computes the drawing geometry", []string{"R5"}, []string{"internal/architecturecodec"}},
		{"app-service", "", "Coordinates goals, runs and reviews", []string{"R1", "R6"}, []string{"internal/app"}},
		{"marshal-planner", "", "Turns a goal into requirements, design and tasks", []string{"R6", "R7"}, []string{"internal/marshal"}},
		{"scheduler", "", "Orders tasks into waves and lanes", []string{"R7"}, []string{"internal/scheduler"}},
		{"worker-runner", "", "Runs one task with a provider CLI", []string{"R8"}, []string{"internal/worker"}},
		{"providers", "", "Probes and drives the installed provider CLIs", []string{"R8"}, []string{"internal/providers"}},
		{"project-context", "", "Project contract: components, requirements, invariants", []string{"R9"}, []string{"internal/projectcontext"}},
		{"architecture-codec", "", "Canonical architecture JSON and geometry", []string{"R9"}, []string{"internal/architecturecodec"}},
		{"state-store", "", "SQLite state of goals, tasks and acceptances", []string{"R10"}, []string{"internal/store"}},
		{"git-ops", "", "Worktrees, commits and merges", []string{"R11"}, []string{"internal/git"}},
		{"sandbox", "", "Confines worker processes", []string{"R12"}, []string{"internal/sandbox"}},
		{"effect-inventory", "", "Lists every side-effect site", []string{"R12"}, []string{"tools/effect-inventory"}},
		{"release-tools", "", "Pack manifest and release checks", []string{"R13"}, []string{"tools"}},
		{"telemetry", "", "Local run events and logs", []string{"R14"}, []string{"internal/telemetry"}},
	}
	links := [][3]string{
		{"cli", "app-service", "commands"}, {"cli", "tui-board", "opens board"}, {"cli", "architecture-review", "opens review"},
		{"tui-board", "app-service", "owner actions"}, {"tui-board", "state-store", "reads progress"}, {"tui-board", "telemetry", "reads events"},
		{"web-board", "app-service", "owner actions"}, {"web-board", "state-store", "reads progress"},
		{"architecture-review", "app-service", "accept"}, {"architecture-review", "architecture-codec", "validate geometry"},
		{"architecture-review", "project-context", "reads contract"}, {"architecture-review", "state-store", "acceptance"},
		{"review-page", "review-server", "owner requests"}, {"review-server", "layout-engine", "geometry"},
		{"app-service", "marshal-planner", "goal"}, {"app-service", "scheduler", "tasks"}, {"app-service", "state-store", "goal rows"},
		{"app-service", "project-context", "contract"}, {"app-service", "git-ops", "branches"}, {"app-service", "telemetry", "events"},
		{"app-service", "architecture-review", "starts review"}, {"app-service", "providers", "provider status"},
		{"marshal-planner", "providers", "model turns"}, {"marshal-planner", "project-context", "requirements"},
		{"marshal-planner", "architecture-codec", "draft"}, {"marshal-planner", "state-store", "plan rows"},
		{"scheduler", "worker-runner", "task"}, {"scheduler", "state-store", "task rows"}, {"scheduler", "git-ops", "worktrees"},
		{"scheduler", "telemetry", "lane events"},
		{"worker-runner", "providers", "run CLI"}, {"worker-runner", "sandbox", "confine"}, {"worker-runner", "git-ops", "commit"},
		{"worker-runner", "state-store", "results"}, {"worker-runner", "telemetry", "logs"},
		{"providers", "sandbox", "spawn"}, {"providers", "telemetry", "usage"},
		{"project-context", "architecture-codec", "encode"}, {"project-context", "state-store", "contract rows"},
		{"git-ops", "sandbox", "git process"},
		{"effect-inventory", "sandbox", "sites"}, {"effect-inventory", "git-ops", "sites"},
		{"release-tools", "effect-inventory", "check"}, {"release-tools", "git-ops", "tag"},
		{"app-service", "marshal-planner", "revision"}, {"scheduler", "worker-runner", "retry"},
	}
	components := []any{}
	for i, p := range parts {
		c := map[string]any{"id": p.id, "name": p.id, "responsibility": p.does, "technology": "", "files": p.files, "covers": p.covers, "dependencies": []any{}, "change": "new"}
		if p.parent != "" {
			c["parent"] = p.parent
		}
		if mixed {
			c["change"] = []string{"existing", "existing", "new", "changed", "existing", "removed"}[i%6]
		}
		components = append(components, c)
	}
	connections := []any{}
	for i, l := range links {
		c := map[string]any{"id": fmt.Sprintf("%s-to-%s-%d", l[0], l[1], i), "source": l[0], "target": l[1], "label": l[2], "protocol": "", "change": "new"}
		if mixed {
			c["change"] = []string{"existing", "existing", "existing", "new", "changed", "removed"}[i%6]
		}
		connections = append(connections, c)
	}
	raw, _ := json.Marshal(map[string]any{"title": "marshal", "purpose": "Run goals with several provider CLIs under owner review", "components": components, "connections": connections, "constraints": []any{}, "source_contract": map[string]any{"id": "marshal", "revision": 2, "digest": "sha256:fixture"}, "base_revision": 1})
	return raw
}

// generatedFixture is a deterministic large architecture: parts in tiers,
// connections mostly towards deeper tiers with some back edges.
func generatedFixture(parts, connections int) json.RawMessage {
	rng := rand.New(rand.NewSource(42))
	tiers := []string{"ui", "api", "svc", "domain", "store"}
	components := []any{}
	tierOf := make([]int, parts)
	for i := 0; i < parts; i++ {
		tierOf[i] = i * len(tiers) / parts
		components = append(components, map[string]any{"id": fmt.Sprintf("%s-%02d", tiers[tierOf[i]], i), "name": fmt.Sprintf("%s part %d", tiers[tierOf[i]], i), "responsibility": "Generated part", "technology": "", "files": []any{}, "change": []string{"existing", "new", "existing", "changed"}[i%4]})
	}
	list := []any{}
	seen := map[[2]int]int{}
	for len(list) < connections {
		a, b := rng.Intn(parts), rng.Intn(parts)
		// Forward across tiers, plus one back edge in fifty.
		if a == b || (tierOf[a] >= tierOf[b] && (tierOf[a] == tierOf[b] || len(list)%50 != 0)) {
			continue
		}
		if seen[[2]int{a, b}] > 1 {
			continue
		}
		seen[[2]int{a, b}]++
		list = append(list, map[string]any{"id": fmt.Sprintf("c%03d", len(list)), "source": components[a].(map[string]any)["id"], "target": components[b].(map[string]any)["id"], "label": fmt.Sprintf("flow %d", len(list)), "protocol": "", "change": []string{"existing", "existing", "new", "removed"}[len(list)%4]})
	}
	raw, _ := json.Marshal(map[string]any{"title": "generated", "purpose": "Scale fixture", "components": components, "connections": list, "constraints": []any{}})
	return raw
}
