#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
TMP=${TMPDIR:-/tmp}/sfs-smoke-$$
BIN="$TMP/sfs"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/sessions"

cd "$ROOT"
CGO_ENABLED=0 go build -trimpath -o "$BIN" ./cmd/sfs
"$BIN" version
"$BIN" --help > "$TMP/help.txt" 2>&1
"$BIN" --backend mock search --help > "$TMP/search-help.txt" 2>&1
grep -q "SuperFeishuSearch" "$TMP/help.txt"
grep -q "Usage of search" "$TMP/search-help.txt"

"$BIN" --backend mock --session-dir "$TMP/sessions" --output json \
  search "A 项目 延期" --sources docs,messages,minutes > "$TMP/search.json"

"$BIN" --backend mock --session-dir "$TMP/sessions" --output json \
  query --source tasks --filter '{"query":"测试","completed":false}' > "$TMP/query.json"

"$BIN" --backend mock --session-dir "$TMP/sessions" --output json \
  research "A 项目延期原因" --sources docs,messages,minutes,tasks --fetch-top 4 > "$TMP/research.json"

"$BIN" --backend mock --session-dir "$TMP/sessions" --output json \
  plan examples/plan-search-fetch.json > "$TMP/plan.json"

python3 - "$TMP" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
search = json.loads((root / "search.json").read_text())
query = json.loads((root / "query.json").read_text())
research = json.loads((root / "research.json").read_text())
plan = json.loads((root / "plan.json").read_text())

assert search["session_id"], search
assert search["candidates"], search
assert query["candidates"], query
assert research["candidate_pack"]["candidates"], research
assert research["evidence_pack"]["evidence"], research
assert plan["result"]["nodes"], plan
print("Mock CLI smoke test passed.")
PY
