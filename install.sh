#!/bin/sh
# Marshal installer.
#
#   curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
#
# Downloads the release binary for this machine, verifies it against the
# release's published checksums, and installs it. Nothing is run with sudo and
# nothing outside the install directory is touched.
#
# Environment:
#   MARSHAL_VERSION      release tag to install (default: the latest release)
#   MARSHAL_INSTALL_DIR  where to put the binary (default: ~/.local/bin)

set -eu

REPO="Zen1th53/marshal"
INSTALL_DIR="${MARSHAL_INSTALL_DIR:-$HOME/.local/bin}"

info() { printf '%s\n' "$*" >&2; }
die() { printf 'marshal: %s\n' "$*" >&2; exit 1; }

need() {
    command -v "$1" >/dev/null 2>&1 || die "this installer needs $1, which is not on PATH"
}

# Releases are built for Linux and macOS. A curl install of the binary is not
# Gatekeeper-quarantined on macOS, and the darwin/arm64 binary is ad-hoc signed
# by the Go linker, so it runs on Apple Silicon without any extra step.
case $(uname -s) in
    Linux) os="linux" ;;
    Darwin) os="darwin" ;;
    *) die "no release binary for $(uname -s); Marshal publishes linux and darwin (macOS).
  Build from source instead: go install github.com/$REPO/cmd/marshal@latest" ;;
esac

case $(uname -m) in
    x86_64 | amd64) arch="amd64" ;;
    aarch64 | arm64) arch="arm64" ;;
    *) die "no release binary for $(uname -m); Marshal publishes amd64 and arm64" ;;
esac

need curl
need tar
need install
need mkdir

# sha256sum is coreutils; shasum is the Perl one. Either verifies the download,
# and an installer that skipped verification when neither was present would be
# defeating its own checksum file.
if command -v sha256sum >/dev/null 2>&1; then
    checksum_cmd="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
    checksum_cmd="shasum -a 256"
else
    die "this installer needs sha256sum or shasum to verify the download"
fi

# Every request is bounded. Without this a connection that stops delivering
# leaves the installer waiting forever. The limits bound silence rather than
# size: a slow link that keeps delivering is allowed to finish, and one that
# drops below 1 KB/s for 30 seconds is abandoned and retried.
fetch() {
    curl -fsSL --connect-timeout 15 --speed-limit 1024 --speed-time 30 \
        --retry 3 --retry-delay 2 "$@"
}

tag="${MARSHAL_VERSION:-}"
if [ -z "$tag" ]; then
    info "Resolving the latest Marshal release..."
    tag=$(fetch --max-time 30 "https://api.github.com/repos/$REPO/releases/latest" |
        sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' |
        head -n 1)
    [ -n "$tag" ] || die "could not resolve the latest release; set MARSHAL_VERSION to a tag"
fi
version="${tag#v}"

archive="marshal_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

tmp=$(mktemp -d)
# Leave nothing behind, including when the download or verification fails.
trap 'rm -rf "$tmp"' EXIT INT TERM

info "Downloading Marshal $tag for $os/$arch..."
fetch -o "$tmp/$archive" "$base/$archive" ||
    die "could not download $archive from release $tag"
fetch --max-time 60 -o "$tmp/checksums.txt" "$base/checksums.txt" ||
    die "release $tag publishes no checksums.txt; refusing to install unverified"

info "Verifying checksum..."
(cd "$tmp" && grep " $archive\$" checksums.txt > expected.txt &&
    $checksum_cmd -c expected.txt >/dev/null 2>&1) ||
    die "checksum verification failed for $archive; the download was not installed"

tar -xzf "$tmp/$archive" -C "$tmp" ||
    die "could not extract $archive"
[ -f "$tmp/marshal" ] || die "$archive does not contain a marshal binary"

{ mkdir -p "$INSTALL_DIR" &&
    install -m 755 "$tmp/marshal" "$INSTALL_DIR/marshal"; } ||
    die "could not install to $INSTALL_DIR; set MARSHAL_INSTALL_DIR to a writable directory"


# ---------------------------------------------------------------------------
# Setup. Everything below makes the install usable without a second step:
# PATH, Git, tmux and the coding agents are checked and, where it is safe,
# fixed. A step that needs sudo is only run after a yes typed at the terminal;
# without a terminal (CI, an agent, a pipe with no tty) it is printed instead.
#
#   MARSHAL_NO_MODIFY_PATH=1   leave shell startup files alone
#   MARSHAL_INSTALL_DEPS=0     never install Git or tmux, only report them
#   MARSHAL_INSTALL_YES=1      answer yes to every question (still no sudo
#                              without a terminal to type the password in)
# ---------------------------------------------------------------------------

bin="$INSTALL_DIR/marshal"
if [ -t 2 ] && [ -z "${NO_COLOR:-}" ]; then
    green=$(printf '\033[32m'); yellow=$(printf '\033[33m'); bold=$(printf '\033[1m'); dim=$(printf '\033[2m'); reset=$(printf '\033[0m')
else
    green=""; yellow=""; bold=""; dim=""; reset=""
fi
ok() { info "  ${green}✓${reset} $*"; }
warn() { info "  ${yellow}!${reset} $*"; }
todo=""
now=""
# short shows a path the way a person types it.
short() { case "$1" in "$HOME"/*) printf '~/%s' "${1#"$HOME"/}" ;; *) printf '%s' "$1" ;; esac; }
add_todo() { todo="$todo
  $*"; }

# A question needs someone to answer it: a terminal that is not CI.
can_ask() {
    [ -z "${CI:-}" ] && [ -z "${MARSHAL_INSTALL_NONINTERACTIVE:-}" ] &&
        { : </dev/tty; } 2>/dev/null
}
ask() {
    [ "${MARSHAL_INSTALL_YES:-}" = 1 ] && return 0
    can_ask || return 1
    printf '  %s [Y/n] ' "$1" >&2
    read -r answer </dev/tty || return 1
    case "$answer" in "" | y | Y | yes | YES | Yes) return 0 ;; esac
    return 1
}

info ""
info "${bold}Marshal $tag installed${reset} ${dim}($(short "$bin"))${reset}"
info ""
info "Setting up:"

# PATH: a binary the shell cannot find is not installed as far as the user
# is concerned.
case ":$PATH:" in
    *":$INSTALL_DIR:"*) ok "$(short "$INSTALL_DIR") is on PATH" ;;
    *)
        rc=""
        line="export PATH=\"$INSTALL_DIR:\$PATH\""
        case "${SHELL##*/}" in
            zsh) rc="${ZDOTDIR:-$HOME}/.zshrc" ;;
            bash) if [ "$os" = darwin ]; then rc="$HOME/.bash_profile"; else rc="$HOME/.bashrc"; fi ;;
            fish) rc="$HOME/.config/fish/conf.d/marshal.fish"; line="fish_add_path $INSTALL_DIR" ;;
            *) rc="$HOME/.profile" ;;
        esac
        if [ -n "${MARSHAL_NO_MODIFY_PATH:-}" ]; then
            warn "$(short "$INSTALL_DIR") is not on PATH (left alone: MARSHAL_NO_MODIFY_PATH)"
            add_todo "$line"
        elif [ -f "$rc" ] && grep -F "$INSTALL_DIR" "$rc" >/dev/null 2>&1; then
            ok "$(short "$INSTALL_DIR") is in $(short "$rc") (new terminals find marshal)"
            now="$line"
        elif { mkdir -p "${rc%/*}" && printf '\n# Added by the Marshal installer\n%s\n' "$line" >>"$rc"; } 2>/dev/null; then
            ok "Added $(short "$INSTALL_DIR") to PATH in $(short "$rc") (new terminals find marshal)"
            now="$line"
        else
            warn "$(short "$INSTALL_DIR") is not on PATH and $(short "$rc") could not be changed"
            add_todo "$line"
        fi
        ;;
esac

package_manager=""
for manager in brew pacman apt-get apt dnf zypper; do
    if command -v "$manager" >/dev/null 2>&1; then
        package_manager="$manager"
        break
    fi
done
# On macOS Homebrew wins; on Linux a system manager does (Linuxbrew is rare
# and would install a second tmux beside the system one).
if [ "$os" = linux ] && [ "$package_manager" = brew ]; then
    for manager in pacman apt-get apt dnf zypper; do
        if command -v "$manager" >/dev/null 2>&1; then package_manager="$manager"; break; fi
    done
fi
sudo=""
if [ "$(id -u 2>/dev/null || echo 1)" != 0 ]; then sudo="sudo "; fi
install_command() {
    case "$package_manager" in
        brew) echo "brew install $1" ;;
        pacman) echo "${sudo}pacman -S --needed --noconfirm $1" ;;
        apt | apt-get) echo "${sudo}$package_manager install -y $1" ;;
        dnf) echo "${sudo}dnf install -y $1" ;;
        zypper) echo "${sudo}zypper install -y $1" ;;
        *) echo "" ;;
    esac
}

# provide installs a missing tool when it is safe to: Homebrew needs no
# password and runs unasked; sudo runs only after a yes at the terminal.
provide() {
    tool="$1" reason="$2"
    command=$(install_command "$tool")
    if [ -z "$command" ]; then
        warn "$tool is missing: $reason"
        add_todo "install $tool with your system's package manager"
        return 1
    fi
    if [ "${MARSHAL_INSTALL_DEPS:-1}" != 0 ]; then
        run=""
        case "$command" in
            sudo\ *) can_ask && ask "$tool is missing ($reason). Install it now with: $command?" && run=1 ;;
            *) if [ "$package_manager" = brew ] || ask "$tool is missing ($reason). Install it now with: $command?"; then run=1; fi ;;
        esac
        if [ -n "$run" ]; then
            info "  ${dim}→ $command${reset}"
            if can_ask; then input=/dev/tty; else input=/dev/null; fi
            if /bin/sh -c "$command" <"$input" >&2 2>&1 && command -v "$tool" >/dev/null 2>&1; then
                return 0
            fi
            warn "installing $tool failed"
        fi
    fi
    warn "$tool is missing: $reason"
    add_todo "$command"
    return 1
}

# Git
if command -v git >/dev/null 2>&1; then
    ok "$(git --version 2>/dev/null | head -n 1)"
elif [ "$os" = darwin ] && [ "$package_manager" != brew ]; then
    warn "Git is missing: Marshal works inside Git repositories"
    add_todo "xcode-select --install   ${dim}# installs Git${reset}"
elif provide git "Marshal works inside Git repositories"; then
    ok "$(git --version 2>/dev/null | head -n 1) (installed)"
fi

# tmux 3.3a or newer
tmux_new_enough() {
    version=$(tmux -V 2>/dev/null | sed -n 's/^tmux [^0-9]*\([0-9][0-9]*\)\.\([0-9][0-9]*\)\([a-z]\{0,1\}\).*/\1 \2 \3/p')
    [ -n "$version" ] || return 0 # a build without a version number is assumed current
    set -- $version
    [ "$1" -gt 3 ] || { [ "$1" -eq 3 ] && { [ "$2" -gt 3 ] || { [ "$2" -eq 3 ] && [ -n "${3:-}" ]; }; }; }
}
tmux_reason="the Marshal TUI runs in it"
if command -v tmux >/dev/null 2>&1; then
    if tmux_new_enough; then
        ok "$(tmux -V)"
    else
        warn "$(tmux -V) is too old; Marshal needs 3.3a or newer (Ubuntu 24.04+, Debian 12+, or tmux built from source)"
        add_todo "upgrade tmux to 3.3a or newer"
    fi
elif provide tmux "$tmux_reason"; then
    ok "$(tmux -V) (installed)"
fi

# Coding agents: Marshal leads them, so at least one is needed. They are
# never installed here; each has its own sign-in.
agents=""
for agent in claude codex opencode agy; do
    if command -v "$agent" >/dev/null 2>&1; then agents="$agents $agent"; fi
done
if [ -n "$agents" ]; then
    ok "Coding agents:$agents"
else
    warn "No coding agent found: install at least one of Claude Code (claude), Codex (codex), OpenCode (opencode) or Antigravity (agy)"
    add_todo "install a coding agent: Claude Code, Codex, OpenCode or Antigravity"
fi

# The binary itself, and the anonymous install count. Registration happens
# here so an install is counted even before Marshal is first opened; it
# honours `marshal telemetry disable` and MARSHAL_CLOUD_ENDPOINT=off, and a
# failure changes nothing.
if "$bin" version >/dev/null 2>&1; then
    telemetry=$("$bin" telemetry register 2>/dev/null | head -n 1) || telemetry=""
    case "$telemetry" in
        *disabled* | *off*) ok "Anonymous telemetry is off" ;;
        "") ;;
        *) ok "Anonymous telemetry is on ${dim}(random ID, version, OS; never code or prompts · marshal telemetry disable)${reset}" ;;
    esac
else
    warn "$bin does not run on this machine"
fi

if [ -n "$todo" ]; then
    info ""
    info "${bold}Left to do:${reset}$todo"
fi
info ""
info "${bold}Start:${reset}"
if [ -n "$now" ]; then
    info "  $now   ${dim}# only in this terminal${reset}"
fi
info "  cd /path/to/your/repository && marshal"
