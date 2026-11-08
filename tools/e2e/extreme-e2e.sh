#!/usr/bin/env bash
# Marshal end-to-end qualification.
#
# Exercises a fresh Marshal build in fresh scratch projects: offline CLI (A),
# the TUI driven through an isolated tmux server (B), provider harness
# verification (C), a Marshal planning/dispatch flow (D) and the Marshal
# no-restriction behaviour (E). Prints PASS/FAIL/SKIP/INFO per check, writes
# results.tsv and a summary, and exits non-zero on any FAIL.
#
# It never touches ~/.local/bin/marshal, the source checkout's working tree,
# or the owner's tmux server: every tmux call runs with TMUX_TMPDIR pointing
# at $E2E_HOME/tmux, and MARSHAL itself honours TMUX_TMPDIR, so the
# dedicated marshal-* sessions land on that private server too.
#
# See tools/e2e/README.md for options and how to read the results.
set -uo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO=$(cd "$SCRIPT_DIR/../.." && pwd)

E2E_HOME=${E2E_HOME:-$HOME/.cache/marshal-e2e}
BIN=""
DO_BUILD=0
EXPECT_VERSION=${EXPECT_VERSION:-v0.2.0}
PROJECT=""
TEMPLATE=""
SKIP_PROVIDERS=0
SKIP_TUI=0
SKIP_CLI=0
SKIP_WALK=0
SKIP_FLOW=0
PROVIDERS="codex claude opencode agy"
MARSHAL_PROVIDER=codex
FLOW_TIMEOUT=${FLOW_TIMEOUT:-2400}
KEEP=0
ASSIST=0
POLICY_SUITE=""

usage() {
	cat <<'EOF'
Usage: tools/e2e/extreme-e2e.sh [options]

  --bin PATH            marshal binary to test (default $E2E_HOME/bin/marshal)
  --build               build the Marshal binary from this checkout into --bin first
  --expect-version V    version string --version must contain (default v0.2.0)
  --e2e-home DIR        private work area (default ~/.cache/marshal-e2e); keep it short:
                        <project>/.marshal/runtime.sock must stay under 108 bytes
  --project DIR         use this pre-made git repo for the TUI/flow phases instead of
                        creating proj-<timestamp> (it must contain the calc fixture)
  --template DIR        copy scratch projects from this committed git repo instead of
                        running `git init` (for sandboxes that forbid git outside a tree)
  --skip-providers      skip everything that spends provider quota (C, D, E and the
                        auto-started Marshal chat check)
  --skip-tui            skip B (TUI)
  --skip-cli            skip A (offline CLI)
  --skip-walk           in B, skip the full Ctrl+N action walk (~10 min)
  --skip-flow           skip D/E (Marshal flow); C still runs unless --skip-providers
  --providers "LIST"    providers for C (default "codex claude opencode agy")
  --marshal PROVIDER    provider for the Marshal chat in D (default codex)
  --flow-timeout SECS   upper bound for D (default 2400)
  --policy-suite FILE   policy suite JSON for `marshal policy test` (SKIP otherwise)
  --assist-credentials  after an unassisted harness verify times out on the credential
                        decision, retry while nudging the Marshal (recorded separately)
  --keep                leave tmux sessions running at the end
  -h, --help
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	--bin) BIN=$2; shift ;;
	--build) DO_BUILD=1 ;;
	--expect-version) EXPECT_VERSION=$2; shift ;;
	--e2e-home) E2E_HOME=$2; shift ;;
	--project) PROJECT=$2; shift ;;
	--template) TEMPLATE=$2; shift ;;
	--skip-providers) SKIP_PROVIDERS=1 ;;
	--skip-tui) SKIP_TUI=1 ;;
	--skip-cli) SKIP_CLI=1 ;;
	--skip-walk) SKIP_WALK=1 ;;
	--skip-flow) SKIP_FLOW=1 ;;
	--providers) PROVIDERS=$2; shift ;;
	--marshal) MARSHAL_PROVIDER=$2; shift ;;
	--flow-timeout) FLOW_TIMEOUT=$2; shift ;;
	--policy-suite) POLICY_SUITE=$2; shift ;;
	--keep) KEEP=1 ;;
	--assist-credentials) ASSIST=1 ;;
	-h | --help) usage; exit 0 ;;
	*) echo "unknown option $1" >&2; usage >&2; exit 2 ;;
	esac
	shift
done

BIN=${BIN:-$E2E_HOME/bin/marshal}
RUN_ID=$(date +%Y%m%d-%H%M%S)
OUT=$E2E_HOME/runs/$RUN_ID
CAP=$E2E_HOME/ux-captures
mkdir -p "$OUT" "$CAP" "$E2E_HOME/tmux"
RESULTS=$OUT/results.tsv
LOG=$OUT/commands.log
POPUPS=$OUT/popups.log
: >"$RESULTS"
: >"$POPUPS"

export TMUX_TMPDIR=$E2E_HOME/tmux
export MARSHAL_NO_UPDATE_CHECK=1
BIN_DIR=$(dirname "$BIN")
export PATH="$BIN_DIR:$PATH"
unset TMUX TMUX_PANE

# ---------------------------------------------------------------- reporting
FAILS=0
PASSES=0
SKIPS=0
c_red=$'\e[31m' c_grn=$'\e[32m' c_ylw=$'\e[33m' c_off=$'\e[0m'
[ -t 1 ] || { c_red='' c_grn='' c_ylw='' c_off=''; }

# record ID STATUS SECONDS EVIDENCE
record() {
	local id=$1 status=$2 secs=$3 evidence=$4
	evidence=$(printf '%s' "$evidence" | tr '\n\t' '  ' | cut -c1-400)
	printf '%s\t%s\t%s\t%s\n' "$id" "$status" "$secs" "$evidence" >>"$RESULTS"
	local colour=$c_ylw
	case "$status" in
	PASS) colour=$c_grn; PASSES=$((PASSES + 1)) ;;
	FAIL) colour=$c_red; FAILS=$((FAILS + 1)) ;;
	SKIP) SKIPS=$((SKIPS + 1)) ;;
	esac
	printf '%s%-5s%s %-38s %6ss  %s\n' "$colour" "$status" "$c_off" "$id" "$secs" "$evidence"
}

now() { date +%s.%N; }
elapsed() { printf '%.1f' "$(echo "$(now) - $1" | bc)"; }

# mx DIR ARGS... : run marshal in DIR, capture combined output in $MX_OUT, rc in $MX_RC
MX_OUT="" MX_RC=0
mx() {
	local dir=$1
	shift
	MX_OUT=$(cd "$dir" && timeout "${MX_TIMEOUT:-90}" "$BIN" "$@" </dev/null 2>&1)
	MX_RC=$?
	printf '### [%s] marshal %s (rc=%s)\n%s\n' "$dir" "$*" "$MX_RC" "$MX_OUT" >>"$LOG"
}

# check ID DIR PATTERN ARGS... : PASS when rc=0 and output matches the ERE PATTERN
check() {
	local id=$1 dir=$2 pattern=$3
	shift 3
	local t
	t=$(now)
	mx "$dir" "$@"
	if [ "$MX_RC" -eq 0 ] && printf '%s' "$MX_OUT" | grep -Eq -- "$pattern"; then
		record "$id" PASS "$(elapsed "$t")" "$(printf '%s' "$MX_OUT" | head -3)"
	else
		record "$id" FAIL "$(elapsed "$t")" "rc=$MX_RC: $(printf '%s' "$MX_OUT" | head -4)"
	fi
}

# ------------------------------------------------------------------ tmux
tm() { tmux "$@"; }
cap() { tm capture-pane -p -t "$1" 2>/dev/null; }
save_cap() { # TARGET NAME
	cap "$1" >"$CAP/$2.txt"
	cp "$CAP/$2.txt" "$OUT/$2.txt" 2>/dev/null
}
keys() { local t=$1; shift; tm send-keys -t "$t" "$@"; }
lit() { local t=$1; shift; tm send-keys -t "$t" -l "$*"; }
# wait_for TARGET ERE SECONDS : 0 when the pane shows the pattern in time
wait_for() {
	local target=$1 pattern=$2 limit=$3 i=0
	while [ "$i" -lt "$limit" ]; do
		cap "$target" | grep -Eq -- "$pattern" && return 0
		sleep 1
		i=$((i + 1))
	done
	return 1
}

# Popups: MARSHAL opens operator review surfaces with `tmux display-popup`,
# which is a child process for as long as the popup is open; provider CLIs
# show native trust/permission prompts in their own panes.
POPUP_RE='Trust this folder|Do you trust|trust the files|Allow this|Approve\?|Allow once|Allow always|permission to|\[y/N\]|\(y/n\)|Press enter to continue|Waiting for (your )?approval|needs your approval'
scan_popups() { # CONTEXT
	local ctx=$1 found=0
	if pgrep -f "display-popup" >/dev/null 2>&1 && pgrep -af "display-popup" | grep -q "$TMUX_TMPDIR\|marshal"; then
		echo "$(date +%T) [$ctx] tmux display-popup open: $(pgrep -af display-popup | head -1 | cut -c1-200)" >>"$POPUPS"
		found=1
	fi
	local w
	for w in $(tm list-windows -a -F '#{session_name}:#{window_index}' 2>/dev/null | grep '^marshal-'); do
		local hit
		hit=$(cap "$w" | grep -E -- "$POPUP_RE" | head -2)
		if [ -n "$hit" ]; then
			echo "$(date +%T) [$ctx] $w: $(echo "$hit" | tr '\n' ' ' | cut -c1-240)" >>"$POPUPS"
			found=1
		fi
	done
	return $((1 - found))
}

# ----------------------------------------------------------------- setup
make_project() { # DIR : fresh repo with the calc fixture (failing test)
	local dir=$1
	mkdir -p "$dir"
	if [ -n "$TEMPLATE" ]; then
		cp -a "$TEMPLATE/." "$dir/"
		return
	fi
	(
		cd "$dir" || exit 1
		git init -q -b main
		printf 'def add(a, b):\n    return a - b\n' >calc.py
		printf 'import unittest\nfrom calc import add\n\n\nclass T(unittest.TestCase):\n    def test_add(self):\n        self.assertEqual(add(2, 3), 5)\n\n\nif __name__ == "__main__":\n    unittest.main()\n' >test_calc.py
		printf '# calc\n\nTiny calculator.\n' >README.md
		# A real Python project ignores interpreter caches; without it every
		# check that imports calc leaves an out-of-scope __pycache__ file.
		printf '__pycache__/\n*.pyc\n' >.gitignore
		git add -A
		git -c user.name=e2e -c user.email=e2e@localhost commit -qm "init scratch project"
	)
}

build_binary() {
	local sha bd pkg=github.com/Zen1th53/marshal/internal/cli
	sha=$(git -C "$REPO" rev-parse --short HEAD)
	bd=$(date -u +%Y-%m-%dT%H:%M:%SZ)
	mkdir -p "$(dirname "$BIN")"
	(cd "$REPO" && GOTOOLCHAIN=${GOTOOLCHAIN:-go1.26.10} go build -buildvcs=false \
		-ldflags "-X $pkg.Version=$EXPECT_VERSION -X $pkg.Commit=$sha -X $pkg.BuildDate=$bd" \
		-o "$BIN" ./cmd/marshal)
}

echo "Marshal E2E  run=$RUN_ID  out=$OUT"
echo "binary=$BIN  tmux=$TMUX_TMPDIR"
if [ "$DO_BUILD" = 1 ]; then
	t=$(now)
	if build_binary >>"$LOG" 2>&1; then record build PASS "$(elapsed "$t")" "$BIN"; else record build FAIL "$(elapsed "$t")" "see $LOG"; fi
fi
if [ ! -x "$BIN" ]; then
	echo "binary $BIN not found; use --build or --bin" >&2
	exit 2
fi
case "$(readlink -f "$BIN")" in "$(readlink -f "$HOME/.local/bin/marshal" 2>/dev/null)")
	echo "refusing to test the installed ~/.local/bin/marshal; point --bin at a private build" >&2
	exit 2
	;;
esac

CLI_PROJ=$E2E_HOME/cli-$RUN_ID
FLOW_PROJ=${PROJECT:-$E2E_HOME/proj-$RUN_ID}
NOID_PROJ=$E2E_HOME/noid-$RUN_ID
OUTSIDE=$E2E_HOME/outside
mkdir -p "$OUTSIDE"
echo "outside-read-marker-$RUN_ID" >"$OUTSIDE/note.txt"
make_project "$CLI_PROJ"
[ -n "$PROJECT" ] || make_project "$FLOW_PROJ"
make_project "$NOID_PROJ"
echo "projects: cli=$CLI_PROJ flow=$FLOW_PROJ noid=$NOID_PROJ"

# ===================================================================== A
section_a() {
	echo "== A. CLI (offline)"
	local t
	check A01-version "$CLI_PROJ" "Marshal.*$EXPECT_VERSION" --version

	check A02-init-fresh "$CLI_PROJ" "initialized" init

	# A git identity must not be needed: empty HOME, no system config.
	t=$(now)
	local eh=$E2E_HOME/emptyhome-$RUN_ID
	mkdir -p "$eh"
	local out rc
	out=$(cd "$NOID_PROJ" && env -i PATH=/usr/bin:/bin HOME="$eh" XDG_CONFIG_HOME="$eh/.config" GIT_CONFIG_NOSYSTEM=1 \
		TMUX_TMPDIR="$TMUX_TMPDIR" MARSHAL_NO_UPDATE_CHECK=1 timeout 90 "$BIN" init 2>&1)
	rc=$?
	if [ $rc -eq 0 ] && echo "$out" | grep -q initialized; then
		record A03-init-no-git-identity PASS "$(elapsed "$t")" "$out"
	else
		record A03-init-no-git-identity FAIL "$(elapsed "$t")" "rc=$rc $out"
	fi

	# A long project path must give a clear error, not a raw socket errno.
	t=$(now)
	local long=$E2E_HOME/long-$RUN_ID/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
	make_project "$long"
	mx "$long" init
	# A real daemon on the long path must serve CLI clients through the short
	# fallback socket (no "invalid argument", and status answers).
	tm kill-session -t e2e-long-$RUN_ID 2>/dev/null
	tm new-session -d -s e2e-long-$RUN_ID -c "$long" -e "PATH=$PATH" -e "TMUX_TMPDIR=$TMUX_TMPDIR" \
		"'$BIN' daemon >'$E2E_HOME/long-$RUN_ID.log' 2>&1"
	sleep 2
	mx "$long" status
	if printf '%s' "$MX_OUT" | grep -q 'invalid argument'; then
		record A04-long-path-runtime-socket FAIL "$(elapsed "$t")" "path ${#long} chars: $MX_OUT"
	elif printf '%s' "$MX_OUT" | grep -q 'schema='; then
		record A04-long-path-runtime-socket PASS "$(elapsed "$t")" "path ${#long} chars; daemon answered: $MX_OUT"
	else
		record A04-long-path-runtime-socket FAIL "$(elapsed "$t")" "path ${#long} chars; daemon did not answer: $MX_OUT"
	fi
	tm kill-session -t e2e-long-$RUN_ID 2>/dev/null
	rm -rf "$E2E_HOME/long-$RUN_ID"

	check A05-doctor "$CLI_PROJ" "Verdict: PASS" doctor
	MX_TIMEOUT=180 check A06-doctor-deep "$CLI_PROJ" "Verdict: PASS" doctor --deep
	check A07-setup-status "$CLI_PROJ" "Readiness: READY" setup status
	check A08-adapters "$CLI_PROJ" "^ *codex +AVAILABLE" adapters
	for p in codex claude opencode agy; do
		check "A09-adapter-probe-$p" "$CLI_PROJ" "adapter $p" adapter probe "$p"
	done
	check A10-telemetry-off "$CLI_PROJ" "compiled out|disabled|off" telemetry status
	check A11-ultra-status-local "$CLI_PROJ" "locally|local" ultra status

	t=$(now)
	if command -v strace >/dev/null; then
		(cd "$CLI_PROJ" && strace -f -qq -e trace=connect -o "$OUT/strace-ultra.txt" "$BIN" ultra request >"$OUT/ultra.txt" 2>&1)
		local inet
		inet=$(grep -E 'AF_INET6?' "$OUT/strace-ultra.txt" | grep -v 'htons(53)' | head -2)
		if [ -z "$inet" ] && grep -q local "$OUT/ultra.txt"; then
			record A12-ultra-request-no-cloud PASS "$(elapsed "$t")" "$(cat "$OUT/ultra.txt"); no AF_INET connect"
		else
			record A12-ultra-request-no-cloud FAIL "$(elapsed "$t")" "$(cat "$OUT/ultra.txt") $inet"
		fi
	else
		check A12-ultra-request-no-cloud "$CLI_PROJ" "locally" ultra request
	fi

	# Public update status follows the existing GitHub release feed.
	check A13-update-status "$CLI_PROJ" "." update status
	check A14-help "$CLI_PROJ" "Why work can be blocked" help
	check A15-help-why "$CLI_PROJ" "." help why
	check A16-constitution-version "$CLI_PROJ" "constitution=" constitution version
	check A17-constitution-invariants "$CLI_PROJ" "CI-021" constitution invariants
	check A18-a2a-status "$CLI_PROJ" "A2A" a2a status
	check A19-egm-status "$CLI_PROJ" "egm/" egm status
	check A20-mcp-status "$CLI_PROJ" "Provider connections" mcp status
	(cd "$CLI_PROJ" && timeout 120 "$BIN" legal audit >"$OUT/legal-scratch.txt" 2>&1)
	MX_TIMEOUT=180 check A21-legal-audit-source "$REPO" "Overall Status" legal audit

	# Wording that contradicts the Marshal contract.
	t=$(now)
	mx "$CLI_PROJ" setup status
	if printf '%s' "$MX_OUT" | grep -q 'limited to approved destinations'; then
		record A22-setup-network-wording-extreme FAIL "$(elapsed "$t")" "$(printf '%s' "$MX_OUT" | grep env.network)"
	else
		record A22-setup-network-wording-extreme PASS "$(elapsed "$t")" "$(printf '%s' "$MX_OUT" | grep env.network)"
	fi
	mx "$CLI_PROJ" help
	if printf '%s' "$MX_OUT" | grep -q 'requires a valid entitlement'; then
		record A23-help-ultra-wording-extreme FAIL 0 "help says ULTRA 'requires a valid entitlement' (Marshal ULTRA is local)"
	else
		record A23-help-ultra-wording-extreme PASS 0 "no entitlement wording"
	fi

	# Runtime-backed commands need the local runtime (daemon).
	tm kill-session -t e2e-daemon-$RUN_ID 2>/dev/null
	tm new-session -d -s e2e-daemon-$RUN_ID -c "$CLI_PROJ" -e "PATH=$PATH" -e "TMUX_TMPDIR=$TMUX_TMPDIR" \
		"'$BIN' daemon >'$OUT/daemon.log' 2>&1"
	local i=0
	until [ -S "$CLI_PROJ/.marshal/runtime.sock" ] || [ $i -ge 30 ]; do sleep 0.5; i=$((i + 1)); done
	check A30-status "$CLI_PROJ" "schema=" status
	check A31-agents "$CLI_PROJ" "agents" agents
	t=$(now)
	mx "$CLI_PROJ" agent register --name e2e-bad --role worker --provider codex --model x
	if printf '%s' "$MX_OUT" | grep -qi 'developer\|valid roles\|role'; then
		record A32-agent-register-bad-role-message PASS "$(elapsed "$t")" "$MX_OUT"
	else
		record A32-agent-register-bad-role-message FAIL "$(elapsed "$t")" "unhelpful: $MX_OUT"
	fi
	check A33-agent-register "$CLI_PROJ" "^AGENT-" agent register --name e2e-dev --role developer --provider codex --model gpt-5.5
	check A34-agents-count "$CLI_PROJ" "^[1-9][0-9]* agents" agents
	printf '[{"id":"TASK-E2E-001","title":"E2E import smoke","status":"ready","risk":"R1","base_commit":"HEAD","head_commit":"HEAD"}]\n' >"$OUT/tasks.json"
	check A35-task-import-dry-run "$CLI_PROJ" "TASK-E2E-001" task import "$OUT/tasks.json" --dry-run
	check A36-task-import "$CLI_PROJ" "added=1|matched=1" task import "$OUT/tasks.json"
	check A37-tasks "$CLI_PROJ" "[1-9][0-9]* tasks" tasks
	check A38-task-show "$CLI_PROJ" "TASK-E2E-001" task show TASK-E2E-001
	check A39-events "$CLI_PROJ" "events" events
	check A40-artifacts "$CLI_PROJ" "artifacts" artifacts

	check A50-memory-status "$CLI_PROJ" "status=healthy" memory status
	check A51-memory-remember "$CLI_PROJ" "memory_id=MEM-" memory remember "E2E deploy rule" "Always run unittest before merging calc changes" --kind=decision
	check A52-memory-remember-2 "$CLI_PROJ" "memory_id=MEM-" memory remember "E2E colour" "The calculator banner colour is teal"
	local mem
	mem=$(printf '%s' "$MX_OUT" | grep -o 'MEM-[0-9a-f]*' | head -1)
	check A53-memory-list "$CLI_PROJ" "count=[2-9]" memory list
	check A54-memory-recall-1word "$CLI_PROJ" "results=[1-9]" memory recall unittest
	check A55-memory-recall-2words "$CLI_PROJ" "results=[1-9]" memory recall "banner colour"
	check A56-memory-recall-3words "$CLI_PROJ" "results=[1-9]" memory recall "merging calc changes"
	check A57-memory-show "$CLI_PROJ" "$mem" memory show "$mem"
	check A58-memory-tombstone-dry "$CLI_PROJ" "dry-run" memory tombstone "$mem" --dry-run
	check A59-memory-tombstone "$CLI_PROJ" "tombstoned" memory tombstone "$mem"
	check A60-memory-recall-after-tombstone "$CLI_PROJ" "results=0" memory recall banner
	check A61-memory-audit "$CLI_PROJ" "memory audit count" memory audit
	check A62-memory-candidates "$CLI_PROJ" "memory candidates" memory candidates

	check A70-permission-allow "$CLI_PROJ" "recorded" permission read allow "$OUTSIDE"
	check A71-permission-list "$CLI_PROJ" "read allow" permission read list "$OUTSIDE"
	check A72-permission-revoke "$CLI_PROJ" "revoked" permission read revoke "$OUTSIDE"
	check A73-permission-list-unset "$CLI_PROJ" "unset" permission read list "$OUTSIDE"
	if [ -n "$POLICY_SUITE" ]; then
		check A74-policy-test "$CLI_PROJ" "PASS" policy test "$POLICY_SUITE"
	else
		record A74-policy-test SKIP 0 "no policy suite fixture in the repo; pass --policy-suite FILE"
	fi

	# MCP over stdio: initialize, tools/list, recall, project_context, stop.
	t=$(now)
	tm kill-session -t e2e-daemon-$RUN_ID 2>/dev/null
	python3 "$SCRIPT_DIR/mcp_probe.py" "$BIN" "$CLI_PROJ" \
		memory_recall '{"query":"unittest"}' project_context '{}' >"$OUT/mcp.jsonl" 2>&1
	local mcp_rc=$?
	if [ $mcp_rc -eq 0 ] && grep -q '"step": "tools/list", "ok": true' "$OUT/mcp.jsonl" &&
		grep -q 'call memory_recall", "ok": true.*E2E deploy rule' "$OUT/mcp.jsonl" &&
		grep -q 'call project_context", "ok": true' "$OUT/mcp.jsonl"; then
		record A80-mcp-stdio "PASS" "$(elapsed "$t")" "$(grep -c '"ok": true' "$OUT/mcp.jsonl") ok steps; tools/list, memory_recall, project_context"
	else
		record A80-mcp-stdio FAIL "$(elapsed "$t")" "$(grep '"ok": false' "$OUT/mcp.jsonl" | head -2)"
	fi
	python3 "$SCRIPT_DIR/mcp_probe.py" "$BIN" "$CLI_PROJ" \
		memory_remember '{"submission_id":"e2e-standalone","title":"E2E mcp","body":"standalone remember"}' >"$OUT/mcp-remember.jsonl" 2>&1
	record A81-mcp-standalone-remember INFO 0 "$(grep '"call memory_remember' "$OUT/mcp-remember.jsonl" | cut -c1-200) (needs a live Marshal session; tools/list still advertises it)"
}

# ===================================================================== B
SESSION=""
section_b() {
	echo "== B. TUI (tmux $TMUX_TMPDIR)"
	local t
	t=$(now)
	mx "$FLOW_PROJ" init
	# The auto-started Marshal chat uses the project's default provider; start
	# it with the requested Marshal so no greeting turn spends another quota.
	printf '%s\n' "$MARSHAL_PROVIDER" >"$FLOW_PROJ/.marshal/default-provider"
	# Launch from outside tmux, the way a person does: marshal creates and
	# attaches its dedicated session on this (isolated) server.
	tm kill-session -t e2e-launch-$RUN_ID 2>/dev/null
	tm new-session -d -s e2e-launch-$RUN_ID -x 220 -y 55 -c "$FLOW_PROJ" -e "PATH=$PATH" -e "TMUX_TMPDIR=$TMUX_TMPDIR" \
		"env -u TMUX -u TMUX_PANE MARSHAL_NO_UPDATE_CHECK=1 '$BIN'; echo MARSHAL-EXITED rc=\$?; sleep 99999"
	local i=0
	while [ $i -lt 30 ]; do
		SESSION=$(tm ls -F '#{session_name}' 2>/dev/null | grep "^marshal-$(basename "$FLOW_PROJ" | tr -c 'A-Za-z0-9_\n-' '-')-" | head -1)
		[ -n "$SESSION" ] && break
		sleep 1
		i=$((i + 1))
	done
	if [ -z "$SESSION" ]; then
		record B01-launch-dedicated-session FAIL "$(elapsed "$t")" "$(cap e2e-launch-$RUN_ID | grep -v '^\s*$' | tail -3)"
		return
	fi
	local W0="$SESSION:0"
	if wait_for "$W0" 'Team' 20; then
		record B01-launch-dedicated-session PASS "$(elapsed "$t")" "$SESSION"
	else
		record B01-launch-dedicated-session FAIL "$(elapsed "$t")" "$(cap "$W0" | head -3)"
	fi
	sleep 3
	save_cap "$W0" 01-landing
	if cap "$W0" | grep -q 'Governance integrity alert'; then
		record B02-landing-clean FAIL 0 "$(cap "$W0" | grep 'Governance integrity')"
	else
		record B02-landing-clean PASS 0 "landing renders, no integrity alert"
	fi
	if cap "$W0" | grep -q 'Ctrl+N'; then
		record B03-landing-mentions-ctrl-n PASS 0 "Ctrl+N advertised"
	else
		record B03-landing-mentions-ctrl-n FAIL 0 "landing never mentions Ctrl+N (advanced menu); only '[Esc] Navigation'"
	fi

	# The Marshal chat window is started automatically in Marshal.
	sleep 8
	local chat
	chat=$(tm list-windows -t "$SESSION" -F '#{window_index} #{window_name} #{pane_current_command}' | grep -- '-chat-' | head -1)
	if [ -n "$chat" ]; then
		local cw="$SESSION:${chat%% *}"
		save_cap "$cw" 02-chat-initial
		if cap "$cw" | grep -Eq "$POPUP_RE"; then
			echo "$(date +%T) [B04 chat start] $cw: $(cap "$cw" | grep -E "$POPUP_RE" | head -1)" >>"$POPUPS"
			record B04-chat-start-no-native-prompt FAIL 0 "$(cap "$cw" | grep -E "$POPUP_RE" | head -1 | cut -c1-160)"
		else
			record B04-chat-start-no-native-prompt PASS 0 "$chat"
		fi
	else
		record B04-chat-start-no-native-prompt INFO 0 "no chat window auto-started"
	fi

	# Advanced menu
	keys "$W0" C-n
	if wait_for "$W0" '1 Home.*2 Control.*9 System' 5; then
		record B10-ctrl-n-opens-menu PASS 0 "nine sections"
	else
		record B10-ctrl-n-opens-menu FAIL 0 "$(cap "$W0" | head -2)"
	fi
	save_cap "$W0" 03-menu-home
	local n name
	for n in 1 2 3 4 5 6 7 8 9; do
		keys "$W0" "$n"
		sleep 1.5
		name=$(cap "$W0" | sed -n 2p | awk '{print $1}')
		save_cap "$W0" "1${n}-section-$n-$name"
		local scr
		scr=$(cap "$W0")
		if echo "$scr" | grep -Eqi 'panic|runtime error|nil pointer'; then
			record "B1$n-section-$name" FAIL 0 "$(echo "$scr" | grep -Ei 'panic|runtime error' | head -1)"
		else
			record "B1$n-section-$name" PASS 0 "$(echo "$scr" | grep -E '^[▸ ] [A-Z]' | sed 's/│.*//' | tr -s ' ' | tr '\n' ',' | cut -c1-200)"
		fi
	done
	keys "$W0" "/"
	sleep 0.6
	if cap "$W0" | grep -q 'Search:'; then record B20-palette-slash PASS 0 "/ opens search"; else record B20-palette-slash FAIL 0 "no palette"; fi
	save_cap "$W0" 20-palette
	keys "$W0" Escape
	sleep 0.4
	keys "$W0" C-k
	sleep 0.6
	if cap "$W0" | grep -q 'Search:'; then record B21-palette-ctrl-k PASS 0 "Ctrl+K opens search"; else record B21-palette-ctrl-k FAIL 0 "no palette"; fi
	keys "$W0" Escape
	sleep 0.4
	keys "$W0" "?"
	sleep 0.8
	save_cap "$W0" 21-nav-help
	keys "$W0" Escape
	sleep 0.4

	if [ "$SKIP_WALK" = 0 ]; then
		t=$(now)
		# The docked chat halves the menu pane; status lines and verdicts are
		# truncated at that width, so walk with the menu pane ~230 columns wide.
		tm resize-window -t "$W0" -x 460 -y 55 2>/dev/null
		python3 "$SCRIPT_DIR/tui_walk.py" --target "$W0" --manifest "$REPO/internal/tui/manifest.json" \
			--out "$CAP/actions" >"$OUT/tui-actions.jsonl" 2>"$OUT/tui-walk.err"
		python3 "$SCRIPT_DIR/tui_matrix.py" "$OUT/tui-actions.jsonl" >"$OUT/tui-actions.md"
		tm resize-window -t "$W0" -x 220 -y 55 2>/dev/null
		local total bad
		total=$(grep -c '^{' "$OUT/tui-actions.jsonl")
		bad=$(grep -Ec '"state": "(ERROR|DEAD|UNREACHABLE|CHANGED)"' "$OUT/tui-actions.jsonl")
		if [ "$bad" -eq 0 ] && [ "$total" -gt 0 ]; then
			record B30-menu-action-walk PASS "$(elapsed "$t")" "$total actions; matrix $OUT/tui-actions.md"
		else
			record B30-menu-action-walk FAIL "$(elapsed "$t")" "$bad of $total actions ERROR/DEAD/UNREACHABLE; matrix $OUT/tui-actions.md"
		fi
		local id st
		for id in CTUI-0077 CTUI-0078 CTUI-0081 CTUI-0082; do
			st=$(grep "\"$id\"" "$OUT/tui-actions.jsonl" | grep -o '"state": "[A-Z]*"' | cut -d'"' -f4)
			if [ "$st" = FORM ]; then
				record "B31-budget-$id" PASS 0 "opens typed form"
			elif [ "$st" = DISABLED ] && grep "\"$id\"" "$OUT/tui-actions.jsonl" | grep -q "no goal yet"; then
				record "B31-budget-$id" PASS 0 "refused before the form: there is no goal yet"
			else
				record "B31-budget-$id" FAIL 0 "state=$st"
			fi
		done
	else
		record B30-menu-action-walk SKIP 0 "--skip-walk"
	fi

	# Submit one safe action end to end and verify its durable effect.
	t=$(now)
	local tag="e2e-tui-$RUN_ID"
	tui_submit "$W0" "remember semantic knowledge" "$tag" "Remembered through the advanced menu"
	save_cap "$W0" 32-tui-memory-remember-result
	mx "$FLOW_PROJ" memory recall "$tag"
	if cap "$W0" | grep -q 'Result *applied' && printf '%s' "$MX_OUT" | grep -q 'results=[1-9]'; then
		record B32-tui-memory-remember-effect PASS "$(elapsed "$t")" "TUI applied; CLI recall: $(printf '%s' "$MX_OUT" | tail -1)"
	else
		record B32-tui-memory-remember-effect FAIL "$(elapsed "$t")" "$(cap "$W0" | grep -E '^Result|^Detail' | head -2) / CLI: $MX_OUT"
	fi
	# Budget setter before a goal exists: the form takes input, then refuses.
	tui_submit "$W0" "budgets token limit" "50000" "e2e budget"
	save_cap "$W0" 33-tui-budget-before-goal
	record B33-budget-submit-before-goal INFO 0 "$(cap "$W0" | grep -E '^Result +[A-Za-z]' | head -1)"

	# Function-key screens from the workspace.
	leave_nav "$W0"
	local fk
	for fk in F1 F4 F5 F2 F3 F11; do
		keys "$W0" "$fk"
		sleep 3.5
		save_cap "$W0" "40-key-$fk"
		leave_nav "$W0"
	done
	record B40-function-key-screens INFO 0 "captures 40-key-F*.txt"
}

quit_tui() {
	[ -n "$SESSION" ] || return 0
	local W0="$SESSION:0" t
	t=$(now)
	leave_nav "$W0"
	lit "$W0" "/quit"
	keys "$W0" Enter
	sleep 4
	if tm has-session -t "$SESSION" 2>/dev/null && [ "$(tm display -p -t "$W0" '#{pane_dead}' 2>/dev/null)" != 1 ] &&
		tm display -p -t "$W0" '#{pane_current_command}' 2>/dev/null | grep -q marshal; then
		record B50-quit-clean FAIL "$(elapsed "$t")" "marshal still running after /quit: $(cap "$W0" | grep -v '^\s*$' | tail -2)"
	else
		record B50-quit-clean PASS "$(elapsed "$t")" "$(cap e2e-launch-$RUN_ID | grep -v '^\s*$' | tail -2)"
	fi
}

getwd_regression() {
	# Own throwaway server whose cwd is deleted; the launcher stays healthy.
	local gw=$E2E_HOME/tmux-gw-$RUN_ID gone=$E2E_HOME/gone-$RUN_ID t
	t=$(now)
	mx "$NOID_PROJ" init # independent of phase A (--skip-cli)
	mkdir -p "$gw" "$gone"
	(cd "$gone" && TMUX_TMPDIR=$gw tmux new-session -d -s gw-holder 'sleep 9999')
	rm -rf "$gone"
	tm kill-session -t e2e-gw-launch-$RUN_ID 2>/dev/null
	tm new-session -d -s e2e-gw-launch-$RUN_ID -x 200 -y 50 -c "$NOID_PROJ" \
		"env -u TMUX -u TMUX_PANE TMUX_TMPDIR=$gw PATH=$PATH MARSHAL_NO_UPDATE_CHECK=1 '$BIN'; echo MARSHAL-EXITED rc=\$?; sleep 9999"
	sleep 10
	local s screen
	s=$(TMUX_TMPDIR=$gw tmux ls -F '#{session_name}' 2>/dev/null | grep '^marshal-' | head -1)
	screen=$(cap e2e-gw-launch-$RUN_ID)
	echo "$screen" >"$CAP/31-getwd-launcher.txt"
	[ -n "$s" ] && TMUX_TMPDIR=$gw tmux capture-pane -p -t "$s:0" >"$CAP/30-getwd-regression.txt"
	if echo "$screen" | grep -Eq 'getwd|\[exited\]' || [ -z "$s" ]; then
		record B60-getwd-deleted-server-cwd FAIL "$(elapsed "$t")" "$(echo "$screen" | grep -v '^\s*$' | tail -2 | tr '\n' ' ')"
	else
		record B60-getwd-deleted-server-cwd PASS "$(elapsed "$t")" "session $s alive"
	fi
	[ -n "$s" ] && TMUX_TMPDIR=$gw tmux kill-session -t "$s" 2>/dev/null
	TMUX_TMPDIR=$gw tmux kill-session -t gw-holder 2>/dev/null
	tm kill-session -t e2e-gw-launch-$RUN_ID 2>/dev/null
	rm -rf "$gw"
}

# ===================================================================== C
# Return to the workspace composer. Esc on the workspace OPENS navigation,
# so overlays are closed with Esc and navigation is left with q.
leave_nav() { # TARGET
	local i scr
	for i in 1 2 3 4 5 6 7 8; do
		scr=$(cap "$1")
		if echo "$scr" | grep -q 'Enter go · Esc close\|Esc or Enter to dismiss\|Type to edit\|Proceed'; then
			keys "$1" Escape
		elif echo "$scr" | head -1 | grep -q 'System'; then
			keys "$1" q
		else
			return 0
		fi
		sleep 0.5
	done
}

# tui_submit TARGET QUERY FIELD... : palette to an action, fill its form
# fields in order, move to Proceed and submit. Leaves the result on screen.
tui_submit() {
	local w=$1 q=$2 f
	shift 2
	leave_nav "$w"
	keys "$w" C-n
	sleep 1
	keys "$w" /
	sleep 0.4
	lit "$w" "$q"
	sleep 0.8
	keys "$w" Enter
	sleep 1
	keys "$w" Enter
	sleep 1.2
	for f in "$@"; do
		lit "$w" "$f"
		sleep 0.3
		keys "$w" Enter
		sleep 0.8
	done
	if cap "$w" | grep -q Proceed; then
		keys "$w" Right
		sleep 0.4
		keys "$w" Enter
		sleep 3
	fi
}

# Type a slash command into the workspace composer and wait for an answer.
composer() { # TEXT PATTERN TIMEOUT -> sets COMPOSER_SCREEN
	local W0="$SESSION:0" pre
	leave_nav "$W0"
	# Replace the output area first, so an identical earlier answer to the
	# same command is never mistaken for this one.
	lit "$W0" "/help"
	keys "$W0" Enter
	sleep 1.5
	pre=$(cap "$W0" | md5sum)
	lit "$W0" "$1"
	keys "$W0" Enter
	local i=0
	COMPOSER_SCREEN=""
	while [ $i -lt "$3" ]; do
		sleep 2
		i=$((i + 2))
		scan_popups "composer: $1" && POPUP_SEEN=1
		COMPOSER_SCREEN=$(cap "$W0")
		[ "$(echo "$COMPOSER_SCREEN" | md5sum)" = "$pre" ] && continue
		echo "$COMPOSER_SCREEN" | grep -q 'still running' && continue
		echo "$COMPOSER_SCREEN" | grep -Eq -- "$2" && return 0
	done
	return 1
}

section_c() {
	echo "== C. Provider harness verification"
	[ -n "$SESSION" ] || { record C00-providers SKIP 0 "TUI not running"; return; }
	local p model t
	for p in $PROVIDERS; do
		model=$(provider_model "$p")
		POPUP_SEEN=0
		t=$(now)
		if composer "/harness verify $p $model" "Verified $p|Verified .*$p|Verified antigravity|harness verify.*(fail|refus|error)|[Ee]rror|not verified|NOT" 300; then
			local line
			line=$(echo "$COMPOSER_SCREEN" | grep -E "Verified|[Ee]rror|refus|NOT" | tail -1)
			save_cap "$SESSION:0" "50-harness-verify-$p"
			if echo "$line" | grep -q Verified && [ "$POPUP_SEEN" = 0 ]; then
				record "C-harness-verify-$p" PASS "$(elapsed "$t")" "$line (model ${model:-default})"
			else
				record "C-harness-verify-$p" FAIL "$(elapsed "$t")" "$line popup=$POPUP_SEEN (model ${model:-default})"
			fi
		else
			save_cap "$SESSION:0" "50-harness-verify-$p"
			record "C-harness-verify-$p" FAIL "$(elapsed "$t")" "no verdict in 300s; popup=$POPUP_SEEN; $(echo "$COMPOSER_SCREEN" | grep -v '^\s*$' | tail -2 | tr '\n' ' ')"
		fi
		# The bounded credential decision goes to the Marshal, which only acts
		# during a chat turn and times out after 30s. With --assist-credentials
		# the owner nudges the Marshal right after starting the verification, so
		# later phases can still run; the unassisted result above stands.
		if [ "$ASSIST" = 1 ] && echo "$COMPOSER_SCREEN" | grep -q 'decision timed out'; then
			t=$(now)
			local chat
			chat=$(tm list-windows -t "$SESSION" -F '#{window_index} #{window_name}' | grep -- '-chat-' | head -1 | cut -d' ' -f1)
			leave_nav "$SESSION:0"
			lit "$SESSION:0" "/harness verify $p $model"
			keys "$SESSION:0" Enter
			sleep 2
			lit "$SESSION:$chat" "Owner instruction: a bounded $p credential request for harness verification is pending now (.marshal/inbox/marshal.md). Allow it immediately."
			keys "$SESSION:$chat" Enter
			local i=0 out=""
			while [ $i -lt 40 ]; do
				sleep 3
				i=$((i + 1))
				answer_native_prompt "$SESSION:$chat" "C assist $p"
				out=$(cap "$SESSION:0" | grep -E 'Verified|Error:' | tail -1)
				[ -n "$out" ] && break
			done
			if echo "$out" | grep -q Verified; then
				record "C-harness-verify-$p-assisted" INFO "$(elapsed "$t")" "$out (needed an owner nudge)"
			else
				record "C-harness-verify-$p-assisted" FAIL "$(elapsed "$t")" "$out"
			fi
		fi
	done
}

# Answer a provider-native approval dialog in a pane as the owner ("for this
# session" / "don't ask again"), logging it as a popup.
answer_native_prompt() { # TARGET CONTEXT
	local scr
	scr=$(cap "$1" | tail -15)
	if echo "$scr" | grep -Eq 'Allow the .* MCP server to run tool|Would you like to run the following command|Press enter to confirm or esc to cancel|Trust this folder'; then
		echo "$(date +%T) [$2] $1: $(echo "$scr" | grep -E 'Allow the|Would you like|Trust this|\$ ' | head -1 | cut -c1-200)" >>"$POPUPS"
		if echo "$scr" | grep -q 'Trust this folder'; then keys "$1" Enter; else keys "$1" Down; sleep 0.3; keys "$1" Enter; fi
		return 0
	fi
	return 1
}

# Model ids: the TUI's /models listing first, then provider defaults.
provider_model() {
	case "$1" in
	codex) echo "${CODEX_MODEL:-gpt-6.1-sol}" ;;
	claude) echo "${CLAUDE_MODEL:-claude-opus-5-5}" ;;
	opencode) echo "${OPENCODE_MODEL:-opencode/big-pickle}" ;;
	agy) echo "${AGY_MODEL:-gemini-3.8-flash-low}" ;;
	esac
}

# ===================================================================== D/E
# Owner script for the scratch project. The Marshal asks one question per
# message; the driver answers by keyword and falls back to "use your
# recommendation". Each reply is logged with a timestamp.
GOAL_TEXT="Goal for this scratch project: fix the failing unit test in test_calc.py (calc.add subtracts instead of adding) and add one line 'Usage: python3 -m unittest' to README.md. Also, as an Marshal capability check, have a worker run 'curl -sS -o /dev/null -w %{http_code} https://example.com' and write the code to egress.txt, and read $E2E_HOME/outside/note.txt and copy its first line into outside.txt. Use at least two different providers as workers (for example codex and claude), with cross-provider review."
# E2E_GOAL replaces the owner goal (for example to qualify the core flow
# without the outside-folder read check); results report the goal used.
GOAL_TEXT=${E2E_GOAL:-$GOAL_TEXT}
section_d() {
	echo "== D/E. Marshal flow ($MARSHAL_PROVIDER)"
	[ -n "$SESSION" ] || { record D00-flow SKIP 0 "TUI not running"; return; }
	[ -n "${E2E_GOAL:-}" ] && record D00-goal-override INFO 0 "$GOAL_TEXT"
	# The driver performs owner workarounds only when the Marshal needs them
	# (goal contract missing, a published draft left unimported) and reports
	# each one as D19-no-owner-interventions.
	python3 "$SCRIPT_DIR/flow_driver.py" --session "$SESSION" --project "$FLOW_PROJ" --bin "$BIN" \
		--provider "$MARSHAL_PROVIDER" --timeout "$FLOW_TIMEOUT" --goal "$GOAL_TEXT" \
		--goal-short "${E2E_GOAL_SHORT:-Fix the failing unit test in test_calc.py and add one line 'Usage: python3 -m unittest' to README.md; record the example.com HTTP code in egress.txt; copy the first line of $OUTSIDE/note.txt to outside.txt}" \
		--captures "$CAP" --out "$OUT" --popup-re "$POPUP_RE" >"$OUT/flow.jsonl" 2>"$OUT/flow.err"
	# flow_driver prints one JSON record per check.
	python3 - "$OUT/flow.jsonl" <<'PY' >"$OUT/flow.tsv"
import json, sys
for line in open(sys.argv[1]):
    line = line.strip()
    if line.startswith("{"):
        r = json.loads(line)
        print("\t".join([r["id"], r["status"], str(r.get("seconds", 0)), r.get("evidence", "").replace("\t", " ").replace("\n", " ")[:400]]))
PY
	while IFS=$'\t' read -r id st secs ev; do record "$id" "$st" "$secs" "$ev"; done <"$OUT/flow.tsv"
	[ -s "$OUT/popups-flow.txt" ] && sed 's/^/[D flow] /' "$OUT/popups-flow.txt" >>"$POPUPS"
}

# ===================================================================== D2
# TUI3-0012: walk the Board of the run the flow left open. Navigation keys
# only: the walk never opens an inbox confirmation or decides anything.
section_board() {
	echo "== D2. Board walk"
	[ -n "$SESSION" ] || { record BW00-board-on-screen SKIP 0 "TUI not running"; return; }
	leave_nav "$SESSION:0"
	local seed_bin="$E2E_HOME/bin/boardseed"
	if [ ! -x "$seed_bin" ] && [ -d "$SCRIPT_DIR/boardseed" ]; then
		go build -o "$seed_bin" "$SCRIPT_DIR/boardseed" 2>/dev/null || true
	fi
	if [ -x "$seed_bin" ] && [ -d "$FLOW_PROJ" ]; then
		"$seed_bin" "$FLOW_PROJ" >"$OUT/boardseed.log" 2>&1 || true
	fi
	python3 "$SCRIPT_DIR/board_walk.py" --target "$SESSION:0" --out "$CAP" >"$OUT/board-walk.jsonl" 2>"$OUT/board-walk.err"
	python3 - "$OUT/board-walk.jsonl" <<'PY' >"$OUT/board-walk.tsv"
import json, sys
for line in open(sys.argv[1]):
    line = line.strip()
    if line.startswith("{"):
        r = json.loads(line)
        print("\t".join([r["id"], r["status"], str(r.get("seconds", 0)), r.get("evidence", "").replace("\t", " ").replace("\n", " ")[:400]]))
PY
	while IFS=$'\t' read -r id st secs ev; do record "$id" "$st" "$secs" "$ev"; done <"$OUT/board-walk.tsv"
	[ -s "$OUT/board-walk.tsv" ] || record BW00-board-on-screen FAIL 0 "board_walk.py printed nothing: $(tail -2 "$OUT/board-walk.err" | tr '\n' ' ')"
}

# ================================================================== main
cleanup() {
	if [ "$KEEP" = 0 ]; then
		[ -n "$SESSION" ] && tm kill-session -t "$SESSION" 2>/dev/null
		tm kill-session -t e2e-launch-$RUN_ID 2>/dev/null
		tm kill-session -t e2e-daemon-$RUN_ID 2>/dev/null
	fi
}
trap cleanup EXIT

[ "$SKIP_CLI" = 0 ] && section_a
if [ "$SKIP_TUI" = 0 ]; then
	section_b
	getwd_regression
fi
if [ "$SKIP_PROVIDERS" = 0 ] && [ "$SKIP_TUI" = 0 ]; then
	section_c
	if [ "$SKIP_FLOW" = 0 ]; then
		section_d
		section_board
	fi
fi
[ "$SKIP_TUI" = 0 ] && quit_tui

echo
echo "popups / permission waits observed:"
if [ -s "$POPUPS" ]; then sed 's/^/  /' "$POPUPS"; else echo "  none"; fi
echo
echo "SUMMARY: $PASSES PASS, $FAILS FAIL, $SKIPS SKIP  (results: $RESULTS, captures: $CAP)"
echo "projects: flow=$FLOW_PROJ cli=$CLI_PROJ"
[ "$FAILS" -eq 0 ]
