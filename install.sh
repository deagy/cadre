#!/bin/sh
# Install Cadre for whichever AI coding runners are on this machine.
#
#   curl -fsSL https://raw.githubusercontent.com/deagy/cadre/main/install.sh | sh
#
# Before the monorepo merge this took roughly 18-20 manual actions across
# three or four repositories. It is now one command, because there is one
# repository and one marketplace name (`cadre-team`) for every runner.
#
# Deliberately POSIX sh, not bash: the whole point is that it runs on a
# machine nobody has prepared.
#
# What it touches, and nothing else:
#   ~/.cadre/dist          a checkout, used by Cline and for the `cadre` CLI
#   ~/.local/bin/cadre     a symlink to that checkout's launcher
#   ~/.codex/config.toml   an MCP entry, inside a marked block, backed up first
#   each runner's own plugin store, via that runner's own CLI
#
# Run with --uninstall to reverse all of it.

set -eu

REPO_SLUG="deagy/cadre"
REPO_URL="https://github.com/deagy/cadre.git"
MARKETPLACE="cadre-team"
PLUGIN="cadre"
CACHE_DIR="${CADRE_HOME:-$HOME/.cadre}"
CHECKOUT="$CACHE_DIR/dist"
BIN_DIR="${CADRE_BIN_DIR:-$HOME/.local/bin}"
CODEX_CONFIG="$HOME/.codex/config.toml"
BLOCK_BEGIN="# >>> cadre >>>"
BLOCK_END="# <<< cadre <<<"

RUNNERS=""
WITH_LIFECYCLE=0
DRY_RUN=0
UNINSTALL=0

say()  { printf '%s\n' "$*"; }
warn() { printf '%s\n' "$*" >&2; }
die()  { printf 'cadre-install: %s\n' "$*" >&2; exit 1; }

# Every mutating action goes through this, so --dry-run is honest by
# construction rather than by remembering to check a flag at each call site.
run() {
  if [ "$DRY_RUN" -eq 1 ]; then
    printf '  would run: %s\n' "$*"
  else
    "$@"
  fi
}

usage() {
  cat <<EOF
Usage: install.sh [options]

  --runner=LIST      Comma-separated: claude,codex,cline. Default: whichever
                     are found on PATH.
  --with-lifecycle   Also install the G1-G10 lifecycle plugin and its kernel.
                     Most projects do not need this.
  --dry-run          Print every action without performing any of them.
  --uninstall        Reverse everything this script installs.
  -h, --help         Show this message.

Environment:
  CADRE_HOME         Checkout location (default ~/.cadre)
  CADRE_BIN_DIR      Where to link the launcher (default ~/.local/bin)
EOF
}

for arg in "$@"; do
  case "$arg" in
    --runner=*)      RUNNERS="${arg#--runner=}" ;;
    --with-lifecycle) WITH_LIFECYCLE=1 ;;
    --dry-run)       DRY_RUN=1 ;;
    --uninstall)     UNINSTALL=1 ;;
    -h|--help)       usage; exit 0 ;;
    *)               die "unknown option: $arg (try --help)" ;;
  esac
done

# --- preflight ---------------------------------------------------------
#
# Checked up front and reported together. Failing halfway through, after
# already having modified a runner's plugin store, is the outcome worth
# avoiding.

preflight() {
  missing=""
  command -v git >/dev/null 2>&1 || missing="$missing git"
  [ -z "$missing" ] || die "missing prerequisite(s):$missing"

  case "$(uname -s 2>/dev/null || echo unknown)" in
    Linux|Darwin) : ;;
    *)
      # bin/cadre is /bin/sh. On native Windows use install.ps1 instead;
      # saying so plainly beats half-working here.
      die "unsupported platform. On Windows use install.ps1, or run this from WSL."
      ;;
  esac
}

detect_runners() {
  found=""
  for runner in claude codex cline; do
    command -v "$runner" >/dev/null 2>&1 && found="$found $runner"
  done
  printf '%s' "${found# }"
}

# --- checkout ----------------------------------------------------------

sync_checkout() {
  if [ -d "$CHECKOUT/.git" ]; then
    say "  updating $CHECKOUT"
    run git -C "$CHECKOUT" fetch --quiet --depth 1 origin main
    run git -C "$CHECKOUT" reset --quiet --hard FETCH_HEAD
  else
    say "  cloning into $CHECKOUT"
    run mkdir -p "$CACHE_DIR"
    run git clone --quiet --depth 1 "$REPO_URL" "$CHECKOUT"
  fi
}

link_launcher() {
  run mkdir -p "$BIN_DIR"
  # -f so re-running repoints an existing link instead of failing.
  run ln -sfn "$CHECKOUT/bin/cadre" "$BIN_DIR/cadre"
  # Past tense only when it actually happened -- a dry run that reports work
  # it did not do is worse than no dry run.
  [ "$DRY_RUN" -eq 1 ] || say "  linked $BIN_DIR/cadre"
  case ":$PATH:" in
    *":$BIN_DIR:"*) : ;;
    *)
      warn ""
      warn "  Note: $BIN_DIR is not on your PATH. Add it:"
      warn "    bash/zsh   echo 'export PATH=\"$BIN_DIR:\$PATH\"' >> ~/.profile"
      warn "    fish       fish_add_path $BIN_DIR"
      warn ""
      ;;
  esac
}

# --- runners -----------------------------------------------------------

install_claude() {
  say "claude:"
  run claude plugin marketplace add "$REPO_SLUG"
  run claude plugin install "$PLUGIN@$MARKETPLACE" --scope user
  if [ "$WITH_LIFECYCLE" -eq 1 ]; then
    run claude plugin install "cadre-lifecycle-core@$MARKETPLACE" --scope user
  fi
}

install_codex() {
  say "codex:"
  # Codex takes owner/repo directly, so no clone is needed for this part.
  run codex plugin marketplace add "$REPO_SLUG"
  # `marketplace add` is a no-op when the marketplace is already configured,
  # and it does NOT refresh the snapshot -- an existing install would keep
  # serving whatever it cached, which on this machine was a pre-monorepo
  # revision. Upgrade explicitly so a re-run actually updates.
  run codex plugin marketplace upgrade "$MARKETPLACE" || true
  run codex plugin add "$PLUGIN@$MARKETPLACE"
  if [ "$WITH_LIFECYCLE" -eq 1 ]; then
    run codex plugin add "cadre-lifecycle-core@$MARKETPLACE"
  fi
  # Codex discovers custom agents only from ~/.codex/agents/, never from a
  # plugin manifest.
  #
  # Non-fatal: bootstrap-codex refuses to overwrite a namespaced wrapper it
  # does not own, which is correct and expected on any machine that already
  # has some. That is not a reason to abandon the rest of the install --
  # and because of `set -e` it previously did exactly that, skipping the MCP
  # configuration below.
  if ! run "$CHECKOUT/bin/cadre" bootstrap-codex; then
    warn "  some Codex role wrappers were left alone (already present and not"
    warn "  installed by cadre). Remove them and re-run to replace them."
  fi
  configure_codex_mcp
}

configure_codex_mcp() {
  entry="$BLOCK_BEGIN
[mcp_servers.cadre-dispatch]
command = \"cadre\"
args = [\"mcp-dispatch-server\"]
$BLOCK_END"

  if [ "$DRY_RUN" -eq 1 ]; then
    say "  would add an [mcp_servers.cadre-dispatch] block to $CODEX_CONFIG"
    return 0
  fi

  mkdir -p "$(dirname "$CODEX_CONFIG")"
  [ -f "$CODEX_CONFIG" ] || : > "$CODEX_CONFIG"

  if grep -qF "$BLOCK_BEGIN" "$CODEX_CONFIG" 2>/dev/null; then
    say "  $CODEX_CONFIG already has the cadre block; leaving it alone"
    return 0
  fi

  # Back up before touching a file the operator owns and may have edited.
  cp "$CODEX_CONFIG" "$CODEX_CONFIG.cadre-backup"
  printf '\n%s\n' "$entry" >> "$CODEX_CONFIG"
  say "  added the cadre MCP block to $CODEX_CONFIG (backup: $CODEX_CONFIG.cadre-backup)"
}

install_cline() {
  say "cline:"
  # The Cline plugin lives in a subdirectory, so this one does need the
  # checkout that the other runners do not.
  if [ "$DRY_RUN" -eq 1 ]; then
    say "  would run: cline plugin install $CHECKOUT/cline-plugins/cline --force"
    return 0
  fi
  if cline plugin install "$CHECKOUT/cline-plugins/cline" --force; then
    say "  installed"
  else
    # Known upstream defect, not something this script can fix: as of cline
    # CLI 3.0.46 invoking any locally-installed plugin's tool fails with
    # "JSON.stringify cannot serialize cyclic structures". Install and
    # uninstall work. Report it and carry on rather than aborting the whole
    # run over one runner.
    warn "  cline install failed. If the error mentions cyclic structures, that is a"
    warn "  known cline CLI defect (3.0.46), not a problem with this plugin."
  fi
}

install_kernel() {
  # Pre-warm the kernel the lifecycle shim resolves, by asking it its version.
  # The shim downloads and verifies the release it was generated against and
  # caches it, so this is the same code path a first real call would take --
  # doing it here just moves the wait to install time and surfaces a failure
  # while the operator is still watching.
  #
  # This used to run bootstrap_sdlc.py, which created a venv and pip-installed
  # the kernel. That kernel was Python and no longer exists in this
  # repository; the shim fetches the Go one.
  say "lifecycle kernel:"
  run "$CHECKOUT/plugin/plugins/lifecycle/bin/agentic-sdlc" --version
}

# --- uninstall ---------------------------------------------------------

do_uninstall() {
  # Honours --runner, which it previously ignored: `--runner=codex
  # --uninstall` uninstalled Claude Code too, because this looped over every
  # detected runner rather than the requested ones. Removing something the
  # operator did not ask to remove is the worst failure mode this script has.
  targets="$RUNNERS"
  [ -n "$targets" ] || targets="$(detect_runners)"
  scoped=0
  [ -n "$RUNNERS" ] && scoped=1

  say "Removing Cadre for: $targets"
  for runner in $targets; do
    case "$runner" in
      claude)
        run claude plugin uninstall "$PLUGIN@$MARKETPLACE" || true
        run claude plugin marketplace remove "$MARKETPLACE" || true
        ;;
      codex)
        run codex plugin remove "$PLUGIN@$MARKETPLACE" || true
        run codex plugin marketplace remove "$MARKETPLACE" || true
        ;;
      cline)
        run cline plugin uninstall cadre || true
        ;;
    esac
  done

  # Only rewrite the Codex config when Codex was actually a target.
  case " $targets " in *" codex "*) codex_targeted=1 ;; *) codex_targeted=0 ;; esac
  if [ "$codex_targeted" -eq 1 ] && [ "$DRY_RUN" -eq 0 ] && [ -f "$CODEX_CONFIG" ] && grep -qF "$BLOCK_BEGIN" "$CODEX_CONFIG"; then
    tmp="$CODEX_CONFIG.cadre-tmp"
    awk -v b="$BLOCK_BEGIN" -v e="$BLOCK_END" '
      $0 == b { skip = 1; next }
      $0 == e { skip = 0; next }
      !skip   { print }
    ' "$CODEX_CONFIG" > "$tmp" && mv "$tmp" "$CODEX_CONFIG"
    say "  removed the cadre block from $CODEX_CONFIG"
  fi

  # The checkout and the launcher are shared across runners, so a
  # runner-scoped uninstall must leave them in place.
  if [ "$scoped" -eq 1 ]; then
    say "  keeping $CHECKOUT and $BIN_DIR/cadre (shared; --runner was given)"
  else
    run rm -f "$BIN_DIR/cadre"
    run rm -rf "$CACHE_DIR"
  fi
  say "Done."
}

# --- main --------------------------------------------------------------

preflight

if [ "$UNINSTALL" -eq 1 ]; then
  do_uninstall
  exit 0
fi

[ -n "$RUNNERS" ] || RUNNERS="$(detect_runners)"
RUNNERS="$(printf '%s' "$RUNNERS" | tr ',' ' ')"

if [ -z "$RUNNERS" ]; then
  die "no supported runner found (claude, codex, or cline). Install one first, or pass --runner=."
fi

[ "$DRY_RUN" -eq 1 ] && say "(dry run: nothing will be changed)"
say "Runners: $RUNNERS"
say ""

say "checkout:"
sync_checkout
link_launcher
say ""

for runner in $RUNNERS; do
  case "$runner" in
    claude) install_claude ;;
    codex)  install_codex ;;
    cline)  install_cline ;;
    *)      die "unknown runner: $runner" ;;
  esac
  say ""
done

if [ "$WITH_LIFECYCLE" -eq 1 ]; then
  install_kernel
  say ""
fi

say "Done."
say ""
say "  cadre select --task \"...\" --files a.go --task-id T-1"
if [ "$WITH_LIFECYCLE" -eq 1 ]; then
  say "  cadre sdlc validate --root ."
else
  say ""
  say "Lifecycle governance (G1-G10 gates) is optional and not installed."
  say "Re-run with --with-lifecycle if you want it."
fi
