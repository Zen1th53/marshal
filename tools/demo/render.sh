#!/usr/bin/env bash
# Regenerates the README demo, .github/assets/demo.gif, from scratch.
#
# 1. TestWriteDemoSequence (internal/tui/demo_sequence_test.go) drives the
#    shipped Board through its own key handlers on fixture state and writes
#    each frame, exactly as the Board paints it, to out/demo/NN-name.ans,
#    with out/demo/frames.txt listing each frame and how long it holds.
# 2. This script writes a VHS tape (out/demo/demo.tape) that shows the
#    frames one after another in a 120x30 terminal, and VHS records it.
#
# Needs Go, vhs (github.com/charmbracelet/vhs, with its ttyd, ffmpeg and a
# Chromium) and the DejaVu Sans Mono font. Usage: tools/demo/render.sh
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd -P)
cd "$root"
out=out/demo
cols=120
rows=30

env -u TMUX -u TMUX_PANE go test ./internal/tui/ -run '^TestWriteDemoSequence$' -count=1 -demo-seq

[ -s "$out/frames.txt" ] || { echo "render.sh: no frames in $out" >&2; exit 1; }

tape=$out/demo.tape
{
	cat <<EOF
Output "$out/demo.gif"
Set Shell "bash"
Set FontFamily "DejaVu Sans Mono"
Set FontSize 14
Set LineHeight 1.0
Set Padding 14
Set Width 1138
Set Height 528
Set Framerate 20
Set TypingSpeed 1ms
Set CursorBlink false
Hide
Type \`export PS1= PS2=; stty -echo; tput civis; [ "\$(stty size)" = "$rows $cols" ] || stty size > $out/size-error\`
Enter
Sleep 300ms
EOF
	while read -r file ms; do
		printf 'Type `clear; cat %s/%s`\nEnter\nSleep 120ms\nShow\nSleep %dms\nHide\n' "$out" "$file" "$ms"
	done <"$out/frames.txt"
} >"$tape"

rm -f "$out/size-error" "$out/demo.gif"
vhs "$tape"
if [ -e "$out/size-error" ]; then
	echo "render.sh: the VHS terminal is $(cat "$out/size-error") (rows cols), want $rows $cols; adjust Width/Height" >&2
	exit 1
fi
cp "$out/demo.gif" .github/assets/demo.gif
ls -l .github/assets/demo.gif
