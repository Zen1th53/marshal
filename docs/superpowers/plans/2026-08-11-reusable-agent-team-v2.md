# Reusable Agent Team V2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Produce a reusable four-agent engineering constitution with deep execution protocols, hard authority boundaries, evidence gates, and minimal ceremony.

**Architecture:** Shared rules live once in `TEAM.md`; role-specific reasoning and authority live in four role files; repeatable cross-role mechanisms live in `protocols/`; output schemas live in `templates/`. This avoids repetition while keeping every agent independently actionable.

**Tech Stack:** Markdown, repository-local agent instructions.

## Global Constraints

- Project-agnostic and reusable across repositories.
- No hardcoded framework, package manager, or deployment technology.
- Minimal change / no unrelated churn as a core rule.
- Evidence before completion claims.
- Explicit STOP and ESCALATE behavior.
- Separate Architect, Developer, QA, and AppSec authority.
- Security review remains defensive and authorized.

---

### Task 1: Shared constitution

**Files:**
- Create: `TEAM.md`

- [x] Define precedence, shared execution state, risk classes, authority, stop/escalate, evidence, diff discipline, handoff, and DoD.

### Task 2: Role operating systems

**Files:**
- Create: `ARCHITECT.md`
- Create: `DEVELOPER.md`
- Create: `QA.md`
- Create: `APPSEC.md`

- [x] Give each role a mandatory execution loop.
- [x] Give each role explicit authority and prohibited decisions.
- [x] Give each role technical review depth appropriate to its scope.
- [x] Give each role a final gate and handoff requirements.

### Task 3: Shared protocols

**Files:**
- Create: `protocols/HANDOFF.md`
- Create: `protocols/REVIEW.md`
- Create: `protocols/DEBUGGING.md`
- Create: `protocols/EVIDENCE.md`
- Create: `protocols/RELEASE.md`
- Create: `protocols/INCIDENT.md`

- [x] Remove repeated procedural detail from role files.
- [x] Make cross-role behavior deterministic.

### Task 4: Output templates

**Files:**
- Create: `templates/DESIGN.md`
- Create: `templates/ADR.md`
- Create: `templates/TEST-PLAN.md`
- Create: `templates/SECURITY-REVIEW.md`
- Create: `templates/THREAT-MODEL.md`
- Create: `templates/BUG-REPORT.md`
- Create: `templates/RELEASE-CHECKLIST.md`

- [x] Define artifact schemas without project-specific content.

### Task 5: Verification

- [x] Scan for unfinished-task placeholder markers.
- [x] Verify all expected files exist.
- [x] Verify each role contains Mission, Authority, execution loop, STOP/ESCALATE, and final gate.
- [x] Verify referenced protocol/template paths exist.
- [x] Package as ZIP.
