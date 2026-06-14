#!/usr/bin/env bash
#
# install.sh
#
# One-shot installer for Cerebro. Checks prerequisites, copies config
# templates if they're missing, fetches Go dependencies, optionally runs
# database migrations, and builds the binary.
#
# Usage:
#   ./install.sh                   # interactive, full install
#   ./install.sh --no-migrate      # skip `make migrate-up`
#   ./install.sh --no-build        # skip `go build` (useful in CI)
#   ./install.sh --no-path         # skip adding the binary to $PATH
#   ./install.sh --path-dir=DIR    # install binary into DIR instead of default
#   ./install.sh --yes             # non-interactive; accept all defaults
#   ./install.sh --help
#
# Exit codes:
#   0  success
#   1  prerequisite missing
#   2  config step failed
#   3  migration failed
#   4  build failed
#   5  PATH install failed
#
# Safe to re-run: existing configs/<file> are never overwritten.

set -euo pipefail

# ---------------------------------------------------------------------------
# Colours (disabled when stdout is not a TTY or NO_COLOR is set).
# ---------------------------------------------------------------------------
if [[ -t 1 && -z "${NO_COLOR:-}" ]]; then
  RED=$'\033[0;31m'
  GREEN=$'\033[0;32m'
  YELLOW=$'\033[0;33m'
  BLUE=$'\033[0;34m'
  BOLD=$'\033[1m'
  RESET=$'\033[0m'
else
  RED=""; GREEN=""; YELLOW=""; BLUE=""; BOLD=""; RESET=""
fi

info()  { printf "%s==>%s %s\n" "${BLUE}${BOLD}" "${RESET}" "$*"; }
ok()    { printf "%s ✓ %s%s\n"   "${GREEN}"       "$*" "${RESET}"; }
warn()  { printf "%s ! %s%s\n"   "${YELLOW}"      "$*" "${RESET}"; }
fail()  { printf "%s ✗ %s%s\n"   "${RED}${BOLD}"  "$*" "${RESET}" >&2; }

# ---------------------------------------------------------------------------
# Flag parsing.
# ---------------------------------------------------------------------------
DO_MIGRATE=1
DO_BUILD=1
DO_PATH=1
ASSUME_YES=0
PATH_DIR=""

usage() {
  sed -n '3,26p' "$0" | sed 's/^# \{0,1\}//'
  exit 0
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --no-migrate)   DO_MIGRATE=0 ;;
    --no-build)     DO_BUILD=0 ;;
    --no-path)      DO_PATH=0 ;;
    --path-dir=*)   PATH_DIR="${1#*=}" ;;
    --path-dir)     shift; PATH_DIR="$1" ;;
    --yes|-y)       ASSUME_YES=1 ;;
    --help|-h)      usage ;;
    *) fail "unknown flag: $1"; exit 1 ;;
  esac
  shift
done

# ---------------------------------------------------------------------------
# Anchor to the repo root (directory containing this script).
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

if [[ ! -f go.mod || ! -d configs ]]; then
  fail "install.sh must live at the repo root next to go.mod and configs/"
  exit 1
fi

prompt_yes_no() {
  # $1 = prompt, $2 = default (y|n). Honours --yes.
  local prompt="$1" default="${2:-y}" reply
  if [[ $ASSUME_YES -eq 1 ]]; then
    printf "%s [auto: %s]\n" "$prompt" "$default"
    [[ "$default" == "y" ]]
    return
  fi
  read -r -p "$prompt " reply || reply=""
  reply="${reply:-$default}"
  [[ "$reply" =~ ^[Yy]$ ]]
}

# ---------------------------------------------------------------------------
# 1. Prerequisite checks.
# ---------------------------------------------------------------------------
info "Checking prerequisites"

MISSING=0

check_bin() {
  local bin="$1" hint="$2"
  if command -v "$bin" >/dev/null 2>&1; then
    ok "$bin found: $(command -v "$bin")"
  else
    fail "$bin not found — $hint"
    MISSING=1
  fi
}

check_bin go    "install Go 1.26+ from https://go.dev/dl/"
check_bin make  "install GNU make (macOS: xcode-select --install)"
check_bin git   "install git"

# go-migrate is only strictly required if we'll run migrations.
if [[ $DO_MIGRATE -eq 1 ]]; then
  if command -v migrate >/dev/null 2>&1; then
    ok "migrate found: $(command -v migrate)"
  else
    warn "migrate CLI not found — install with 'brew install golang-migrate'"
    warn "Continuing; migrations will be skipped at the end."
    DO_MIGRATE=0
  fi
fi

# Go version must be >= declared toolchain in go.mod.
if command -v go >/dev/null 2>&1; then
  GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
  REQUIRED_VERSION=$(awk '/^go /{print $2}' go.mod)
  # shellcheck disable=SC2183
  printf -v _vcmp '%s\n%s' "$REQUIRED_VERSION" "$GO_VERSION"
  LOWEST=$(printf '%s\n' "$REQUIRED_VERSION" "$GO_VERSION" | sort -V | head -1)
  if [[ "$LOWEST" != "$REQUIRED_VERSION" ]]; then
    fail "Go $GO_VERSION is older than the required $REQUIRED_VERSION (from go.mod)"
    MISSING=1
  else
    ok "Go version $GO_VERSION satisfies $REQUIRED_VERSION"
  fi
fi

if [[ $MISSING -eq 1 ]]; then
  fail "One or more prerequisites are missing. Aborting."
  exit 1
fi

# ---------------------------------------------------------------------------
# 2. Copy config templates (never overwrite existing files).
# ---------------------------------------------------------------------------
info "Preparing configs/"

CONFIG_FILES=(
  "app.yaml"
  "markets.yaml"
  "strategies.yaml"
  "secrets.env"
)

CREATED_ANY=0
for name in "${CONFIG_FILES[@]}"; do
  src="configs/${name}.example"
  dst="configs/${name}"
  if [[ ! -f "$src" ]]; then
    fail "missing template: $src"
    exit 2
  fi
  if [[ -f "$dst" ]]; then
    ok "configs/${name} already present; leaving it alone"
  else
    cp "$src" "$dst"
    ok "created configs/${name} from template"
    CREATED_ANY=1
  fi
done

# Keep secrets.env tight — it contains API keys.
if [[ -f configs/secrets.env ]]; then
  chmod 600 configs/secrets.env 2>/dev/null || true
fi

if [[ $CREATED_ANY -eq 1 ]]; then
  warn "Edit configs/secrets.env before running cerebro — API keys are required"
  warn "for demo/live modes and for LLM agents."
fi

# ---------------------------------------------------------------------------
# 3. Go modules.
# ---------------------------------------------------------------------------
info "Fetching Go module dependencies"
if go mod download; then
  ok "go mod download complete"
else
  fail "go mod download failed"
  exit 2
fi

# ---------------------------------------------------------------------------
# 4. Database migrations (optional).
# ---------------------------------------------------------------------------
if [[ $DO_MIGRATE -eq 1 ]]; then
  info "Running database migrations"
  if [[ -z "${DATABASE_URL:-}" ]]; then
    # Try to source it from secrets.env so operators don't have to export it.
    if [[ -f configs/secrets.env ]]; then
      SECRET_DB_URL=$(grep -E '^[[:space:]]*DATABASE_URL=' configs/secrets.env \
        | tail -1 \
        | cut -d= -f2- \
        | sed -E 's/^"(.*)"$/\1/; s/^'"'"'(.*)'"'"'$/\1/')
      if [[ -n "$SECRET_DB_URL" ]]; then
        export DATABASE_URL="$SECRET_DB_URL"
        ok "sourced DATABASE_URL from configs/secrets.env"
      fi
    fi
  fi

  if [[ -z "${DATABASE_URL:-}" ]]; then
    warn "DATABASE_URL not set and not found in configs/secrets.env; skipping migrations"
    warn "Run 'make migrate-up' manually once DATABASE_URL is exported."
  else
    if prompt_yes_no "Apply pending migrations with 'make migrate-up'? [Y/n]" "y"; then
      if make migrate-up; then
        ok "migrations applied"
      else
        fail "migrations failed"
        exit 3
      fi
    else
      warn "Skipping migrations per user choice"
    fi
  fi
fi

# ---------------------------------------------------------------------------
# 5. Build.
# ---------------------------------------------------------------------------
if [[ $DO_BUILD -eq 1 ]]; then
  info "Building cerebro binary"
  if make build; then
    ok "binary built at ./cerebro"
  else
    fail "build failed"
    exit 4
  fi
fi

# ---------------------------------------------------------------------------
# 6. Put the binary on $PATH so `cerebro` works from anywhere.
#
# Strategy:
#   - Honour --path-dir=DIR if the operator forced one.
#   - Otherwise prefer $GOBIN, then $GOPATH/bin, then ~/.local/bin. These are
#     all writable without sudo and are the conventional spots for user-
#     installed binaries.
#   - /usr/local/bin is deliberately avoided by default because it needs
#     sudo on macOS and litters a system directory from a per-user build.
#
# We install via `go install ./cmd/cerebro` rather than copying ./cerebro
# because `go install` handles GOOS/GOARCH, strips the binary into the
# right place atomically, and respects GOBIN.
# ---------------------------------------------------------------------------
PATH_INSTALLED_DIR=""
if [[ $DO_PATH -eq 1 ]]; then
  info "Installing cerebro to a directory on your \$PATH"

  # Resolve the target directory.
  if [[ -n "$PATH_DIR" ]]; then
    TARGET_DIR="$PATH_DIR"
  else
    GOBIN_VAL="$(go env GOBIN 2>/dev/null || true)"
    GOPATH_VAL="$(go env GOPATH 2>/dev/null || true)"
    if [[ -n "$GOBIN_VAL" ]]; then
      TARGET_DIR="$GOBIN_VAL"
    elif [[ -n "$GOPATH_VAL" ]]; then
      TARGET_DIR="${GOPATH_VAL}/bin"
    else
      TARGET_DIR="${HOME}/.local/bin"
    fi
  fi

  mkdir -p "$TARGET_DIR"

  # `go install` needs GOBIN to be an absolute path when set.
  case "$TARGET_DIR" in
    /*) ;;
    *)  TARGET_DIR="$(cd "$TARGET_DIR" && pwd)" ;;
  esac

  if GOBIN="$TARGET_DIR" go install -ldflags="-s -w" ./cmd/cerebro; then
    ok "installed $TARGET_DIR/cerebro"
    PATH_INSTALLED_DIR="$TARGET_DIR"
  else
    fail "failed to install binary to $TARGET_DIR"
    exit 5
  fi

  # Warn if the target isn't actually on $PATH — the install technically
  # succeeded but the user won't be able to run `cerebro` until they fix
  # their shell startup files.
  case ":${PATH}:" in
    *":${TARGET_DIR}:"*)
      ok "$TARGET_DIR is already on \$PATH"
      ;;
    *)
      warn "$TARGET_DIR is NOT on your \$PATH"
      warn "Add this to your shell rc (e.g. ~/.zshrc, ~/.bashrc):"
      printf "\n    %sexport PATH=\"%s:\$PATH\"%s\n\n" "${BOLD}" "$TARGET_DIR" "${RESET}"
      ;;
  esac
fi

# ---------------------------------------------------------------------------
# 7. Next steps.
#
# The config-dir flag matters a lot here: when the user runs `cerebro` from
# a random directory (now that it's on $PATH), the default `--config-dir=configs`
# resolves against their CWD and will silently miss the real configs/. We
# surface the absolute path to the repo's configs/ so copy-paste just works.
# ---------------------------------------------------------------------------
RUN_CMD="./cerebro"
CONFIG_FLAG=""
if [[ -n "$PATH_INSTALLED_DIR" ]]; then
  RUN_CMD="cerebro"
  CONFIG_FLAG=" --config-dir=${SCRIPT_DIR}/configs"
fi

cat <<EOF

${GREEN}${BOLD}Install complete.${RESET}

Next steps:
  1. Edit ${BOLD}configs/secrets.env${RESET} — at minimum set DATABASE_URL and REDIS_URL.
     For LLM agents, set at least one of OPENAI_API_KEY / ANTHROPIC_API_KEY /
     GEMINI_API_KEY. For demo/live, set BINANCE_DEMO_* or BINANCE_* keys.
  2. Validate config + connectivity:   ${BOLD}make check${RESET}
  3. Run in paper mode (no keys):      ${BOLD}${RUN_CMD} run --paper${CONFIG_FLAG}${RESET}
     Real prices, virtual fills:       ${BOLD}${RUN_CMD} run --demo${CONFIG_FLAG}${RESET}
     Live trading (requires triple
     agreement — see README):          ${BOLD}${RUN_CMD} run --live${CONFIG_FLAG}${RESET}

EOF
