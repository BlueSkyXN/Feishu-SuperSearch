#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 VERSION" >&2
  exit 2
fi
EXPECTED=${1#v}
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

MAIN=$(sed -n 's/[[:space:]]*version = "\([^"]*\)"/\1/p' "$ROOT/cmd/sfs/main.go" | head -1)
MAKE=$(sed -n 's/^VERSION ?= //p' "$ROOT/Makefile" | head -1)
DOCKER=$(sed -n 's/^ARG VERSION=//p' "$ROOT/Dockerfile" | head -1)
OPENAPI=$(awk '/^info:/{in_info=1; next} in_info && /^[^[:space:]]/{in_info=0} in_info && /^[[:space:]]+version:/{gsub(/["\x27]/, "", $2); print $2; exit}' "$ROOT/api/openapi.yaml")
WEB=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1], encoding="utf-8"))["version"])' "$ROOT/web/package.json")
WEB_LOCK=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1], encoding="utf-8"))["version"])' "$ROOT/web/package-lock.json")
CHANGELOG=$(sed -n 's/^## \([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\) -.*/\1/p' "$ROOT/CHANGELOG.md" | head -1)
STATUS=$(sed -n 's/^版本：[[:space:]]*//p' "$ROOT/docs/IMPLEMENTATION_STATUS.md" | head -1)

failed=0
for pair in "cmd/sfs/main.go:$MAIN" "Makefile:$MAKE" "Dockerfile:$DOCKER" "api/openapi.yaml:$OPENAPI" "web/package.json:$WEB" "web/package-lock.json:$WEB_LOCK" "CHANGELOG.md:$CHANGELOG" "docs/IMPLEMENTATION_STATUS.md:$STATUS"; do
  file=${pair%%:*}
  value=${pair#*:}
  if [[ "$value" != "$EXPECTED" ]]; then
    echo "$file declares version $value, expected $EXPECTED" >&2
    failed=1
  fi
done

check_marker() {
  local file=$1 marker=$2
  if ! grep -Fq -- "$marker" "$ROOT/$file"; then
    echo "$file is missing version marker: $marker" >&2
    failed=1
  fi
}

check_marker README.md "SuperFeishuSearch-$EXPECTED-darwin-arm64.tar.gz"
check_marker docs/GETTING_STARTED.md "SuperFeishuSearch-$EXPECTED-darwin-arm64.tar.gz"
check_marker docs/RELEASING.md "git tag -a v$EXPECTED"
check_marker docs/GITHUB_ACTIONS.md "gh release view v$EXPECTED"
check_marker docs/DEVELOPMENT.md "make package VERSION=$EXPECTED"
check_marker docs/TESTING.md "make package VERSION=$EXPECTED"
check_marker CONTRIBUTING.md "make package VERSION=$EXPECTED"
check_marker .github/workflows/release.yml "default: \"$EXPECTED\""
check_marker .github/ISSUE_TEMPLATE/bug_report.yml "sfs $EXPECTED commit=..."

exit "$failed"
