# MARSHAL Web Control Plane — Accessibility & Keyboard Conformance (T216)

This document details the accessibility architecture, semantic landmark structure, keyboard navigation flows, and conformance test results for the MARSHAL Web Control Plane.

---

## 1. Core Conformance Principles

1. **Information Architecture & Landmarks**:
   - Every primary route contains semantic `<header>`, `<main id="main-content" role="main">`, `<nav aria-label="Main Navigation">`, and explicit `<h1>`/`<h2>` heading hierarchies.
   - Modals and overlays declare `role="dialog"`, `aria-modal="true"`, and explicit `aria-label` descriptors.
   - Realtime alerts and notifications use `role="status"` and `aria-live="polite"` to prevent focus hijacking during high-throughput agent runs.

2. **Color Invariant**:
   - Color is **never** the sole indicator of status.
   - All state indicators (`StatusBadge`, `diff-tag`, and graph nodes) combine color with explicit text tokens (`READY`, `RUNNING`, `DEGRADED`, `BLOCKED`, `PENDING`) and iconography.

3. **Keyboard Navigation & Focus Trapping**:
   - Global shortcuts:
     - `Ctrl+K` / `Cmd+K`: Global Entity Search & Navigator
     - `Tab` / `Shift+Tab`: Predictable DOM focus order across forms, tables, and dialogs
     - `Escape`: Closes open modals, palettes, and dropdowns, returning focus to the triggering element
     - `ArrowUp` / `ArrowDown`: Navigates search results and command items

4. **Accessible DAG Alternative**:
   - For complex graph topologies where interactive Canvas/SVG nodes present visual barriers, an accessible sequential node table provides keyboard-navigable dependency traversal.

---

## 2. Keyboard & Route Conformance Matrix

| Route | Tested Landmarks | Keyboard Navigation | Non-Color Status Signals | Focus Isolation | Conformance Status |
|---|---|---|---|---|---|
| **Overview** | `<main>`, `<h2>`, metric boxes | Tab + Enter to jump | Text badges (`READY`, `BLOCKED`) | N/A | **PASS** |
| **Agents** | `<main>`, tables, cards | Tab through agent grid | Text badges + runtime roles | N/A | **PASS** |
| **Tasks & DAG** | `<main>`, forms, modals, tables | Full modal tab trap + Escape | Status badges + priority labels | Modal focus restore | **PASS** |
| **Runs & Logs** | `<main>`, code viewer | Terminal scroll + Tab controls | Exit code tokens + status labels | N/A | **PASS** |
| **Review & Quorum** | `<main>`, diff viewer, forms | Tab navigation through votes | Decision badges (`APPROVE`, `REJECT`)| Modal focus restore | **PASS** |
| **Evidence & Trace** | `<main>`, timeline tree | Tab through causal tree | Signature state tokens | N/A | **PASS** |
| **Memory Explorer** | `<main>`, search input, tables | Tab through search & filters | Lifecycle tags (`ACTIVE`, `TOMBSTONE`)| Modal focus restore | **PASS** |
| **Operations & Doctor**| `<main>`, status tables | Tab through diagnostics & jobs | `READY`/`DEGRADED` text badges | Modal focus restore | **PASS** |
| **Benchmarks** | `<main>`, metric tables | Tab through benchmark cards | `PASSED`/`NOT_RUN` tokens | N/A | **PASS** |
| **Settings** | `<main>`, forms, diagnostics | Tab through form inputs | Numeric & string values | N/A | **PASS** |
| **Global Search** | `role="dialog"`, `role="listbox"`| `↑`/`↓` arrows + Enter + Escape | Entity type tags + status badges | Strict dialog trap | **PASS** |

---

## 3. Known Limitations & Bounded Scope

1. **Complex SVG DAG Rendering**:
   - While visual nodes are complemented by the accessible table alternative, arbitrary pan-and-zoom SVG interaction via screen-reader virtual cursor remains platform-dependent.
2. **High-Frequency SSE Log Streaming**:
   - High-throughput execution logs (>500 lines/sec) are buffered; screen readers announce summary updates via `aria-live="polite"` rather than every individual streaming chunk to prevent reader buffer starvation.
