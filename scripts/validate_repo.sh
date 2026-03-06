#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
common_git_dir=$(git rev-parse --git-common-dir)
common_git_dir_abs=$(cd "$repo_root/$common_git_dir" 2>/dev/null && pwd || cd "$common_git_dir" && pwd)
primary_checkout=$(dirname "$common_git_dir_abs")
repo_name=$(basename "$primary_checkout")
cd "$repo_root"

export GOCACHE=${GOCACHE:-$repo_root/.cache/go-build}
export CARGO_TARGET_DIR=${CARGO_TARGET_DIR:-$repo_root/.cache/cargo-target}
mkdir -p "$GOCACHE" "$CARGO_TARGET_DIR"

pick_python() {
  local candidate
  for candidate in \
    "$repo_root/.venv/bin/python" \
    "$repo_root/.venv-hg-report/bin/python" \
    "$repo_root/venv/bin/python" \
    "$primary_checkout/.venv/bin/python" \
    "$primary_checkout/.venv-hg-report/bin/python" \
    "$primary_checkout/venv/bin/python" \
    python3 \
    python; do
    if [ -x "$candidate" ]; then
      printf '%s\n' "$candidate"
      return 0
    fi
    if command -v "$candidate" >/dev/null 2>&1; then
      command -v "$candidate"
      return 0
    fi
  done
  echo "validate_repo: no python interpreter available" >&2
  return 1
}

run_dashboard_integrity() {
  local missing=""
  local f base
  [ -d web ] || return 0
  [ -f web/index.html ] || return 0
  shopt -s nullglob
  for f in web/*.js; do
    base=$(basename "$f")
    if ! grep -q "src=\"${base}\"" web/index.html; then
      missing="${missing}  ${base}\n"
    fi
  done
  shopt -u nullglob
  if [ -n "$missing" ]; then
    printf 'validate_repo: web/index.html is missing script tags for:\n%b' "$missing" >&2
    return 1
  fi
}

ensure_node_modules() {
  if [ -e "$repo_root/node_modules" ]; then
    return 0
  fi
  if [ -d "$primary_checkout/node_modules" ]; then
    ln -s "$primary_checkout/node_modules" "$repo_root/node_modules"
    return 0
  fi
  echo "validate_repo: node_modules missing in worktree and primary checkout" >&2
  return 1
}

run_mjs_tests_if_present() {
  local files=()
  local dir
  for dir in scripts cross-venue-scanner/scripts; do
    if [ -d "$dir" ]; then
      while IFS= read -r file; do
        files+=("$file")
      done < <(find "$dir" -maxdepth 1 -type f -name '*.test.mjs' | sort)
    fi
  done
  if [ "${#files[@]}" -gt 0 ]; then
    command -v node >/dev/null 2>&1 || {
      echo "validate_repo: node is required for .mjs tests" >&2
      return 1
    }
    node --test "${files[@]}"
  fi
}

case "$repo_name" in
  assistant)
    go test ./...
    go build ./...
    ;;
  beads)
    make fmt-check
    go build -v ./cmd/bd
    go test -short ./...
    ;;
  cfo)
    ensure_node_modules
    npm run lint
    npm run build
    ;;
  chum)
    make quality
    ;;
  chum-automation)
    make check
    ;;
  clawstreetbots)
    py=$(pick_python)
    "$py" -m compileall src scripts
    ;;
  cortex)
    make test
    make build
    ;;
  cortex-factory)
    go test ./...
    go build ./...
    ;;
  da-kong-demo)
    grep -q '<html' index.html
    grep -q '<body' index.html
    ;;
  disco-michael-v0)
    find . -maxdepth 2 -type f | grep -q .
    ;;
  golf-directory)
    ensure_node_modules
    npm test -- --run
    npm run build
    ;;
  hg-FiF)
    py=$(pick_python)
    "$py" -m pytest tests
    ;;
  hg-chum-integration)
    make build
    make vet
    make test
    python3 -m py_compile collectors/*.py
    run_dashboard_integrity
    ;;
  hg-chum-integration-automation)
    make build
    make vet
    make test
    python3 -m py_compile collectors/*.py
    ;;
  hg-market-data)
    if command -v poetry >/dev/null 2>&1; then
      (cd brisbane_prop_updater && poetry run pytest ../tests)
    else
      py=$(pick_python)
      "$py" -m pytest tests
    fi
    ;;
  hg-reporting)
    py=$(pick_python)
    "$py" -m compileall lib hg-report ./*.py
    ;;
  hg-website)
    ensure_node_modules
    npm run lint
    npm run typecheck
    npm run test -- --run
    npm run build
    ;;
  kalshi-poly-arb)
    cargo fmt --all -- --check
    cargo clippy --workspace --all-targets -- -D warnings
    cargo test --workspace --all-targets
    cargo build --workspace --all-targets
    run_mjs_tests_if_present
    py=$(pick_python || true)
    if [ -n "${py:-}" ]; then
      "$py" -m py_compile $(find . -type f -name '*.py' -not -path './.git/*' | sort)
    fi
    ;;
  slide-doctor)
    py=$(pick_python)
    "$py" -m compileall slide_doctor
    ;;
  supercoach-bot)
    py=$(pick_python)
    "$py" -m compileall .
    ;;
  *)
    echo "validate_repo: no validation contract configured for $repo_name" >&2
    exit 1
    ;;
esac
