#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
from typing import Any


MAPPINGS: dict[str, tuple[str, str, str, str]] = {
    "CONF-003": ("./internal/store", "TestConcurrentClaimHasExactlyOneWinner", "BLOCKED", "N concurrent claims produced exactly one owner"),
    "CONF-004": ("./internal/worktree", "TestDirtyTaskWorktreeIsNeverDestroyed", "BLOCKED", "dirty worktree was rejected and preserved"),
    "CONF-005": ("./internal/store", "TestDeveloperCannotCloseQAOrAppSecFinding", "DENY", "developer could not close the QA-owned finding"),
    "CONF-006": ("./internal/store", "TestDeveloperCannotCloseQAOrAppSecFinding", "DENY", "developer could not close the AppSec-owned finding"),
    "CONF-008": ("./internal/store", "TestHEADChangeInvalidatesVerificationAtomically", "INVALIDATE", "verification at the prior commit was invalidated"),
    "CONF-009": ("./internal/store", "TestExpiredLeaseDoesNotAuthorizeTaskSteal", "BLOCKED", "expired lease alone did not authorize stealing"),
    "CONF-010": ("./internal/policy", "TestDeniedOperationDoesNotExecute", "DENY", "denied destructive callback was not executed"),
    "CONF-018": ("./internal/store", "TestHEADChangeInvalidatesVerificationAtomically", "INVALIDATE", "HEAD change emitted invalidation and removed stale PASS"),
    "CONF-019": ("./internal/integration", "TestSecurityPolicyUnavailableFailsClosedBeforeMutation", "DENY", "unavailable policy prevented runtime mutation"),
    "CONF-023": ("./internal/store", "TestEventDuplicateIsIdempotentOnlyForSamePayload", "PASS", "identical event was idempotent and divergent duplicate conflicted"),
    "CONF-024": ("./internal/store", "TestObserveHEADRejectsStaleRevisionWithoutInvalidation", "BLOCKED", "stale revision was rejected without mutation"),
    "CONF-025": ("./internal/integration", "TestReconciliationIsReadOnlyAndReportsSplitBrain", "ESCALATE", "split brain was reported without changing either state"),
    "CONF-026": ("./internal/integration", "TestSecurityWorkerCrashPreservesWorktreeAndNeverCompletes", "BLOCKED", "worker crash preserved worktree and blocked task completion"),
    "CONF-027": ("./internal/mcp", "TestMCPIncompatibleProtocolVersion", "DENY", "incompatible MCP protocol version was explicitly rejected"),
    "CONF-028": ("./internal/a2a", "TestA2ARoleSpoofingDenied", "DENY", "remote A2A caller role spoofing was rejected"),
}


def current_commit(root: Path) -> str:
    proc = subprocess.run(
        ["git", "-C", str(root), "rev-parse", "HEAD"],
        text=True,
        capture_output=True,
        timeout=10,
        check=False,
    )
    return proc.stdout.strip() if proc.returncode == 0 else "UNAVAILABLE"


def scenario_invariant(root: Path, scenario_id: str) -> str:
    document = json.loads((root / "conformance" / "SCENARIOS.json").read_text(encoding="utf-8"))
    for scenario in document["scenarios"]:
        if scenario["id"] == scenario_id:
            return scenario["invariant"]
    return "unknown scenario"


def run_scenario(root: Path, scenario_id: str) -> dict[str, Any]:
    mapping = MAPPINGS.get(scenario_id)
    if mapping is None:
        return {
            "status": "NOT_RUN",
            "scenario_id": scenario_id,
            "reason": "no executable Runtime V1 mapping",
            "invariant": scenario_invariant(root, scenario_id),
            "current_commit": current_commit(root),
        }
    package, test_name, verdict, observed = mapping
    command = ["go", "test", package, "-run", f"^{test_name}$", "-count=1"]
    with tempfile.TemporaryDirectory(prefix="marshal-go-cache-") as cache:
        environment = os.environ.copy()
        environment.update({"CGO_ENABLED": "0", "GOCACHE": cache})
        try:
            proc = subprocess.run(
                command,
                cwd=root,
                env=environment,
                text=True,
                capture_output=True,
                timeout=120,
            )
        except subprocess.TimeoutExpired:
            return {
                "status": "FAIL",
                "scenario_id": scenario_id,
                "command": command,
                "exit_code": None,
                "reason": "executable invariant timed out",
                "current_commit": current_commit(root),
            }
    payload: dict[str, Any] = {
        "status": "PASS" if proc.returncode == 0 else "FAIL",
        "scenario_id": scenario_id,
        "command": command,
        "exit_code": proc.returncode,
        "observed_invariant": observed if proc.returncode == 0 else "test failed",
        "invariant": scenario_invariant(root, scenario_id),
        "current_commit": current_commit(root),
    }
    if proc.returncode == 0:
        payload["verdict"] = verdict
    else:
        payload["stdout"] = proc.stdout[-4000:]
        payload["stderr"] = proc.stderr[-4000:]
    return payload


def main() -> int:
    parser = argparse.ArgumentParser(description="Run one executable MARSHAL Runtime V1 conformance scenario")
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument("--scenario", required=True)
    args = parser.parse_args()
    result = run_scenario(args.root.resolve(), args.scenario)
    print(json.dumps(result, indent=2))
    return 1 if result["status"] == "FAIL" else 0


if __name__ == "__main__":
    raise SystemExit(main())
