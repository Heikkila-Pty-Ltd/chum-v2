#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
hygiene_script="$repo_root/scripts/hygiene_check.sh"
tmp_repo=$(mktemp -d)
trap 'rm -rf "$tmp_repo"' EXIT

git init -q "$tmp_repo"
(
  cd "$tmp_repo"
  git config user.email guardrails@example.com
  git config user.name guardrails-test
  cat >README.md <<'EOF'
# probe
EOF
  git add README.md
  git commit -qm "init"
)

if (cd "$tmp_repo" && GITHUB_HEAD_REF=master bash "$hygiene_script" --ci >/dev/null 2>&1); then
  echo "hygiene_check_test: expected master branch rejection in CI mode" >&2
  exit 1
fi

(cd "$tmp_repo" && GITHUB_HEAD_REF=feature/hardening bash "$hygiene_script" --ci >/dev/null)

(
  cd "$tmp_repo"
  git checkout --detach >/dev/null 2>&1
  if bash "$hygiene_script" --ci >/dev/null 2>&1; then
    echo "hygiene_check_test: expected detached-head CI rejection without branch env" >&2
    exit 1
  fi
)

echo "hygiene_check_test: ok"
