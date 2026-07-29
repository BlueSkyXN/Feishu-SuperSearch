#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
TMP=${TMPDIR:-/tmp}/sfs-http-smoke-$$
BIN="$TMP/sfs"
LOG="$TMP/server.log"
trap 'if [[ -n "${PID:-}" ]]; then kill "$PID" 2>/dev/null || true; wait "$PID" 2>/dev/null || true; fi; rm -rf "$TMP"' EXIT
mkdir -p "$TMP/sessions"

cd "$ROOT"
CGO_ENABLED=0 go build -trimpath -o "$BIN" ./cmd/sfs
PORT=$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)

"$BIN" --backend mock --session-dir "$TMP/sessions" serve --listen "127.0.0.1:$PORT" >"$LOG" 2>&1 &
PID=$!

for _ in $(seq 1 50); do
  if curl --fail --silent "http://127.0.0.1:$PORT/v1/health" > "$TMP/health.json"; then
    break
  fi
  sleep 0.1
done

curl --fail --silent --show-error \
  -H 'Content-Type: application/json' \
  -d '{"query":"A 项目 延期","sources":["docs","messages"]}' \
  "http://127.0.0.1:$PORT/v1/search" > "$TMP/search.json"

curl --fail --silent --show-error --no-buffer \
  -H 'Content-Type: application/json' \
  -H 'Accept: text/event-stream' \
  -d '{"query":"A 项目 延期","sources":["docs","messages"],"fetch_top_k":2}' \
  "http://127.0.0.1:$PORT/v1/research" > "$TMP/research.sse"

curl --fail --silent --show-error "http://127.0.0.1:$PORT/" > "$TMP/index.html"

python3 - "$TMP" <<'PY'
import json
import pathlib
import sys
root = pathlib.Path(sys.argv[1])
health = json.loads((root / "health.json").read_text())
search = json.loads((root / "search.json").read_text())
html = (root / "index.html").read_text()
stream = (root / "research.sse").read_text()
assert health.get("ok") is True, health
assert search.get("candidates"), search
assert "SuperFeishuSearch" in html
assert "event: progress" in stream, stream
assert "event: retrieval" in stream, stream
assert stream.count("event: result") == 1, stream
assert "event: error" not in stream, stream
assert stream.rfind("event: retrieval") < stream.rfind("event: result"), stream
print("HTTP/Web smoke test passed.")
PY
