#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
patch_file="$script_dir/sqlite-vec.patch"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "error: $1 is required" >&2
    exit 1
  fi
}

out="${1:-$script_dir/sqlite-vec.wasm}"
workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

require_cmd curl
require_cmd tar
require_cmd patch

if [[ ! -f "$patch_file" ]]; then
  echo "error: patch file not found: $patch_file" >&2
  exit 1
fi

default_go_sqlite3_version="v0.30.5"
default_sqlite_vec_version="v0.1.6"

go_sqlite3_version="${GO_SQLITE3_VERSION:-$default_go_sqlite3_version}"
sqlite_vec_version="${SQLITE_VEC_VERSION:-$default_sqlite_vec_version}"

sqlite_vec_release="${sqlite_vec_version#v}"
go_sqlite3_url="https://github.com/ncruces/go-sqlite3/archive/refs/tags/${go_sqlite3_version}.tar.gz"
sqlite_vec_url="https://github.com/asg017/sqlite-vec/releases/download/${sqlite_vec_version}/sqlite-vec-${sqlite_vec_release}-amalgamation.tar.gz"

echo "GO_SQLITE3_VERSION=${go_sqlite3_version}"
echo "SQLITE_VEC_VERSION=${sqlite_vec_version}"

mkdir -p "$workdir/output"
curl -#L "$go_sqlite3_url" | tar xzC "$workdir/output" --strip-components=1

(
  cd "$workdir"
  ./output/sqlite3/tools.sh
  ./output/sqlite3/download.sh
  curl -#L "$sqlite_vec_url" | tar xzC ./output/sqlite3
  if [[ ! -f ./output/sqlite3/sqlite-vec.c ]]; then
    echo "error: sqlite-vec.c not found after extraction" >&2
    exit 1
  fi
  patch -p0 <"$patch_file"
  ./output/embed/build.sh
)

mkdir -p "$(dirname "$out")"
cp "$workdir/output/embed/sqlite3.wasm" "$out"
echo "generated: $out"
