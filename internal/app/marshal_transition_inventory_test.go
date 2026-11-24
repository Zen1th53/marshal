package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// frozenTaskMoves is the task lifecycle table. A change here is a change to
// the run engine's contract and must be reviewed with every writer below.
var frozenTaskMoves = map[marshal.TaskState][]marshal.TaskState{
	marshal.Queued:     {marshal.Dispatched},
	marshal.Dispatched: {marshal.HandedIn, marshal.Returned},
	marshal.HandedIn:   {marshal.Accepted, marshal.Returned, marshal.Reassigned, marshal.Escalated},
	marshal.Accepted:   {marshal.Merged, marshal.Returned},
	marshal.Returned:   {marshal.Dispatched, marshal.Reassigned, marshal.Escalated},
	marshal.Reassigned: {marshal.Dispatched, marshal.Escalated},
	marshal.Escalated:  {marshal.Returned, marshal.Reassigned, marshal.Cancelled},
	marshal.Merged:     {},
	marshal.Cancelled:  {},
}

// frozenTaskStateWriters counts every assignment of a task state in the
// package's non-test sources of both editions, by file, function and target.
var frozenTaskStateWriters = map[string]int{
	"failover_default.go handleWorkerLimit -> Escalated":                        1,
	"failover_extreme.go admitProviderDispatch -> Reassigned":                   1,
	"failover_extreme.go handleWorkerLimit -> Escalated":                        2,
	"failover_extreme.go handleWorkerLimit -> Reassigned":                       1,
	"handin_identity.go pauseHandInNotRecorded -> Escalated":                    1,
	"marshal_approval_wait_extreme.go pausePrivateProcess05Approval -> Queued":  2,
	"marshal_approval_wait_extreme.go reworkExpiredProcess05Approval -> Queued": 1,
	"marshal_governed_reassign.go escalateGovernedHarnessMismatch -> Escalated": 1,
	"marshal_infra_retry_extreme.go returnInfraFailure -> Escalated":            3,
	"marshal_infra_retry_extreme.go returnInfraFailure -> Returned":             1,
	"marshal_lane_admission.go escalateTaskLaneAdmission -> Escalated":          1,
	"marshal_lane_admission.go restartInterruptedLaneTask -> Returned":          1,
	"marshal_lane_admission.go retryLaneAdmissionOnce -> Returned":              1,
	"marshal_lifecycle.go ApplyAmendDraftBound -> Queued":                       2,
	"marshal_lifecycle.go ApplyAmendDraftBound -> old.State":                    1,
	"marshal_lifecycle.go CancelTask -> Cancelled":                              1,
	"marshal_lifecycle.go CollectHandIn -> Escalated":                           1,
	"marshal_lifecycle.go CollectHandIn -> HandedIn":                            1,
	"marshal_lifecycle.go Merge -> Merged":                                      1,
	"marshal_lifecycle.go RetryTask -> Returned":                                1,
	"marshal_lifecycle.go Review -> Accepted":                                   1,
	"marshal_lifecycle.go applyMarshalReturn -> Escalated":                      1,
	"marshal_lifecycle.go applyMarshalReturn -> Reassigned":                     1,
	"marshal_lifecycle.go applyMarshalReturn -> Returned":                       1,
	"marshal_lifecycle.go dispatchWithBrief -> Dispatched":                      1,
	"marshal_lifecycle.go escalateWithDetail -> Escalated":                      1,
	"marshal_lifecycle.go parkPausedTask -> Queued":                             1,
	"marshal_lifecycle.go reassign -> Reassigned":                               1,
	"marshal_operations.go recoverOperation -> Dispatched":                      1,
	"marshal_operations.go recoverOperation -> Escalated":                       1,
	"marshal_service.go StartPlanningFromDraft -> Queued":                       1,
	"marshal_task_base.go reconcileStartedStaleLaunch -> Dispatched":            1,
	"marshal_task_base.go reconcileStartedStaleLaunch -> Escalated":             1,
	"worker_auth.go pauseWorkerAuth -> Escalated":                               1,
}

// knownOffTablePaths are writers that move a task outside the lifecycle
// table, mostly on restart, pause or plan reset. They are frozen so that
// none is added without review.
var knownOffTablePaths = map[string]string{
	"marshal_approval_wait_extreme.go pausePrivateProcess05Approval -> Queued":  "an approval wait parks a dispatched task back in the queue",
	"marshal_approval_wait_extreme.go reworkExpiredProcess05Approval -> Queued": "an expired approval requeues the task with a fresh lane",
	"marshal_lifecycle.go ApplyAmendDraftBound -> Queued":                       "a plan amendment resets tasks to the queue",
	"marshal_lifecycle.go ApplyAmendDraftBound -> old.State":                    "a plan amendment keeps an unchanged task's prior state",
	"marshal_lifecycle.go parkPausedTask -> Queued":                             "an interrupted dispatch held by a pause returns to the queue",
	"marshal_operations.go recoverOperation -> Dispatched":                      "restart keeps an interrupted launch dispatched, never queued",
	"marshal_operations.go recoverOperation -> Escalated":                       "restart escalates an unrecoverable dispatched launch",
	"marshal_service.go StartPlanningFromDraft -> Queued":                       "a new run starts every task queued",
	"marshal_task_base.go reconcileStartedStaleLaunch -> Dispatched":            "restart re-adopts a started stale launch",
	"marshal_task_base.go reconcileStartedStaleLaunch -> Escalated":             "restart escalates a stale launch it cannot prove",
}

func taskStateWriters(t *testing.T) map[string]int {
	t.Helper()
	names := map[string]bool{"Queued": true, "Dispatched": true, "HandedIn": true, "Accepted": true, "Returned": true, "Reassigned": true, "Escalated": true, "Merged": true, "Cancelled": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	out := map[string]int{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok || len(as.Lhs) != len(as.Rhs) {
					return true
				}
				for i, lhs := range as.Lhs {
					sel, ok := lhs.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "State" {
						continue
					}
					rhs, ok := as.Rhs[i].(*ast.SelectorExpr)
					if !ok {
						continue
					}
					pkg, ok := rhs.X.(*ast.Ident)
					if !ok {
						continue
					}
					target := ""
					switch {
					case pkg.Name == "marshal" && names[rhs.Sel.Name]:
						target = rhs.Sel.Name
					case pkg.Name != "marshal" && rhs.Sel.Name == "State":
						target = pkg.Name + ".State"
					default:
						continue
					}
					out[file+" "+fn.Name.Name+" -> "+target]++
				}
				return true
			})
		}
	}
	return out
}

func TestTaskTransitionInventory(t *testing.T) {
	moves := marshal.TaskMoves()
	if !maps.EqualFunc(moves, frozenTaskMoves, slices.Equal[[]marshal.TaskState]) {
		t.Fatalf("task lifecycle table changed:\n got %v\nwant %v", moves, frozenTaskMoves)
	}
	got := taskStateWriters(t)
	for key, n := range got {
		if frozenTaskStateWriters[key] != n {
			t.Errorf("task state writer %q appears %d times, frozen %d", key, n, frozenTaskStateWriters[key])
		}
	}
	for key, n := range frozenTaskStateWriters {
		if got[key] != n {
			t.Errorf("frozen task state writer %q appears %d times, want %d", key, got[key], n)
		}
	}
	for key := range got {
		if (strings.HasSuffix(key, "-> Queued") || strings.HasSuffix(key, ".State")) && knownOffTablePaths[key] == "" {
			t.Errorf("task state writer %q leaves the lifecycle table without a known reason", key)
		}
	}
	for key := range knownOffTablePaths {
		if got[key] == 0 {
			t.Errorf("known off-table path %q no longer exists; update the inventory", key)
		}
	}
}
