package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/evidence"
)

func (r *Runtime) recordRunEvidence(ctx context.Context, runID, taskID, adapterName, adapterVersion, baseCommit, resultCommit string, result adapter.Result) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	created := time.Now().UTC()
	stdoutDigest := digestRuntimeBytes(result.Stdout)
	stderrDigest := digestRuntimeBytes(result.Stderr)
	nodes := []evidence.Node{
		runtimeEvidenceNode(evidence.NodeID("EVIDENCE-RUN-"+runID+"-COMMAND"), evidence.NodeTypeCommand, created, map[string]string{
			"task_id": taskID, "run_id": runID, "adapter": adapterName, "base_commit": baseCommit,
		}),
		runtimeEvidenceNode(evidence.NodeID("EVIDENCE-RUN-"+runID+"-OUTPUT"), evidence.NodeTypeOutput, created, map[string]string{
			"task_id": taskID, "run_id": runID, "adapter": adapterName, "status": string(result.Status),
			"exit_code": strconv.Itoa(result.ExitCode), "stdout_digest": stdoutDigest, "stderr_digest": stderrDigest,
			"result_commit": resultCommit,
		}),
		runtimeEvidenceNode(evidence.NodeID("EVIDENCE-RUN-"+runID+"-ENV"), evidence.NodeTypeEnvironment, created, map[string]string{
			"task_id": taskID, "run_id": runID, "adapter": adapterName, "adapter_version": adapterVersion,
		}),
	}
	for _, node := range nodes {
		if _, err := r.StoreEvidence(ctx, sessionForRun(ctx, r, taskID), node); err != nil {
			return err
		}
	}
	for _, edge := range []evidence.Edge{{From: nodes[0].ID, To: nodes[1].ID, Relation: "produced"}, {From: nodes[0].ID, To: nodes[2].ID, Relation: "executed-in"}} {
		if _, err := r.LinkEvidence(ctx, sessionForRun(ctx, r, taskID), edge); err != nil {
			return err
		}
	}
	return nil
}

// Runtime.Run already holds an active session for taskID. This lookup binds
// evidence capture to that canonical session instead of provider metadata.
func sessionForRun(ctx context.Context, r *Runtime, taskID string) string {
	active, err := r.store.ActiveLease(ctx, taskID)
	if err != nil {
		return ""
	}
	return active.Lease.SessionID
}

func runtimeEvidenceNode(id evidence.NodeID, typ evidence.NodeType, created time.Time, metadata map[string]string) evidence.Node {
	digest, _ := evidence.CanonicalDigest(typ, metadata)
	return evidence.Node{ID: id, Type: typ, Digest: digest, CreatedAt: created, Metadata: metadata}
}

func digestRuntimeBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}
