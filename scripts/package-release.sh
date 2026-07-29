#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
VERSION=${1:-}
if [[ -z "$VERSION" ]]; then
  VERSION=$(sed -n 's/[[:space:]]*version = "\([^"]*\)"/\1/p' "$ROOT/cmd/sfs/main.go" | head -1)
fi
VERSION=${VERSION#v}
"$ROOT/scripts/check-version.sh" "$VERSION"

OUT=${OUT_DIR:-$ROOT/dist/release}
OUT=$(python3 -c 'import pathlib,sys; print(pathlib.Path(sys.argv[1]).resolve())' "$OUT")
DIST_ROOT=$(python3 -c 'import pathlib,sys; print(pathlib.Path(sys.argv[1]).resolve())' "$ROOT/dist")
if [[ "$OUT" == "$DIST_ROOT" ]]; then
  echo "OUT_DIR must not be the dist root itself" >&2
  exit 2
fi
case "$OUT/" in
  "$DIST_ROOT/"*) ;;
  *)
    echo "OUT_DIR must be a child of $DIST_ROOT" >&2
    exit 2
    ;;
esac
WORK="$OUT/.work"
rm -rf "$OUT"
mkdir -p "$OUT" "$WORK"

git -C "$ROOT" rev-parse --is-inside-work-tree >/dev/null 2>&1 || {
  echo "release packaging requires a Git checkout" >&2
  exit 2
}
if [[ -n "$(git -C "$ROOT" status --porcelain --untracked-files=normal)" ]]; then
  echo "release packaging requires a clean tracked and untracked worktree" >&2
  exit 2
fi
HEAD_COMMIT=$(git -C "$ROOT" rev-parse HEAD)
if [[ -z "${COMMIT:-}" ]]; then
  COMMIT=$HEAD_COMMIT
fi
if [[ "$COMMIT" != "$HEAD_COMMIT" ]]; then
  echo "COMMIT=$COMMIT does not match checked out HEAD=$HEAD_COMMIT" >&2
  exit 2
fi
if [[ -z "${SOURCE_DATE_EPOCH:-}" ]]; then
  SOURCE_DATE_EPOCH=$(git -C "$ROOT" show -s --format=%ct HEAD)
fi
if [[ -z "${BUILT_AT:-}" ]]; then
  BUILT_AT=$(python3 - "$SOURCE_DATE_EPOCH" <<'PY'
import datetime, sys
print(datetime.datetime.fromtimestamp(int(sys.argv[1]), datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"))
PY
)
fi
LDFLAGS="-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.builtAt=$BUILT_AT"

copy_runtime_docs() {
  local dir=$1
  cp "$ROOT/README.md" "$ROOT/LICENSE" "$ROOT/CHANGELOG.md" "$ROOT/THIRD_PARTY_NOTICES.md" "$ROOT/config.example.json" "$ROOT/config.demo.json" "$dir/"
  mkdir -p "$dir/docs" "$dir/skills/super-feishu-search"
  git -C "$ROOT" archive --format=tar HEAD -- LICENSES | tar -xf - -C "$dir"
  cp "$ROOT/docs/GETTING_STARTED.md" "$ROOT/docs/CONFIGURATION.md" \
     "$ROOT/docs/CLI_REFERENCE.md" "$ROOT/docs/TROUBLESHOOTING.md" "$dir/docs/"
  cp "$ROOT/skills/super-feishu-search/SKILL.md" "$dir/skills/super-feishu-search/"
  cat > "$dir/INSTALL.txt" <<EOF
SuperFeishuSearch $VERSION

1. Place the sfs binary on PATH.
2. Run: sfs version
3. Full offline preview: sfs --config config.demo.json serve
4. Open http://127.0.0.1:3765 and try Search, Continue, Research, Ask, preview, Doctor, and Session restore.
5. Real lark-cli check: sfs --backend larkcli --profile work --as user doctor

See docs/GETTING_STARTED.md for complete instructions.
EOF
}

build_target() {
  local goos=$1 goarch=$2
  local name="SuperFeishuSearch-$VERSION-$goos-$goarch"
  local dir="$WORK/$name"
  local binary="sfs"
  [[ "$goos" == "windows" ]] && binary="sfs.exe"
  mkdir -p "$dir"
  echo "Building $goos/$goarch"
  (
    cd "$ROOT"
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
      go build -buildvcs=false -trimpath -ldflags "$LDFLAGS" -o "$dir/$binary" ./cmd/sfs
  )
  copy_runtime_docs "$dir"
  if [[ "$goos" == "windows" ]]; then
    python3 "$ROOT/scripts/archive-release.py" --input "$dir" --output "$OUT/$name.zip" --epoch "$SOURCE_DATE_EPOCH"
  else
    python3 "$ROOT/scripts/archive-release.py" --input "$dir" --output "$OUT/$name.tar.gz" --epoch "$SOURCE_DATE_EPOCH"
  fi
}

build_target linux amd64
build_target linux arm64
build_target darwin amd64
build_target darwin arm64
build_target windows amd64
build_target windows arm64

SOURCE_NAME="SuperFeishuSearch-$VERSION-source"
SOURCE_DIR="$WORK/$SOURCE_NAME"
mkdir -p "$SOURCE_DIR"
git -C "$ROOT" archive --format=tar HEAD | tar -xf - -C "$SOURCE_DIR"

python3 "$ROOT/scripts/archive-release.py" --input "$SOURCE_DIR" --output "$OUT/$SOURCE_NAME.tar.gz" --epoch "$SOURCE_DATE_EPOCH"
python3 "$ROOT/scripts/archive-release.py" --input "$SOURCE_DIR" --output "$OUT/$SOURCE_NAME.zip" --epoch "$SOURCE_DATE_EPOCH"

if [[ -e "$OUT/.DS_Store" || -L "$OUT/.DS_Store" ]]; then
  unlink "$OUT/.DS_Store"
fi

(
  cd "$OUT"
  if command -v sha256sum >/dev/null 2>&1; then
    checksum=(sha256sum)
  else
    checksum=(shasum -a 256)
  fi

  : > SHA256SUMS
  while IFS= read -r path; do
    "${checksum[@]}" "${path#./}" >> SHA256SUMS
  done < <(find . -maxdepth 1 -type f ! -name SHA256SUMS | LC_ALL=C sort)
)
rm -rf "$WORK"
if [[ -e "$OUT/.DS_Store" || -L "$OUT/.DS_Store" ]]; then
  unlink "$OUT/.DS_Store"
fi
echo "Release artifacts written to $OUT"
