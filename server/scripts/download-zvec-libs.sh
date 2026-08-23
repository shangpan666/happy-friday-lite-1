#!/bin/sh
set -eu

version="${1:-v0.6.0}"
root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
dest="$root/native/zvec"
tmp="${TMPDIR:-/tmp}/zvec-libs-${version}-darwin-arm64.tar.gz"

mkdir -p "$dest"
curl -fL -C - -o "$tmp" "https://github.com/zvec-ai/zvec-go/releases/download/${version}/zvec-libs-darwin-arm64.tar.gz"
tar -xzf "$tmp" -C "$dest"
test -f "$dest/include/zvec/c_api.h"
test -f "$dest/darwin_arm64/libzvec_c_api.dylib"
echo "Zvec native libraries installed in $dest"
