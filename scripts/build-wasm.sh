#!/bin/sh
# Build the WebAssembly build of the engine plus the JS glue it needs, into
# wasm/. The TypeScript package in ../invoicekit.ts copies these two files into
# its assets directory.
#
#   ./scripts/build-wasm.sh [dest]
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
dest=${1:-$root/wasm}
GOROOT=$(go env GOROOT)
mkdir -p "$dest"
GOOS=js GOARCH=wasm go build -trimpath -ldflags='-s -w' -o "$dest/einv.wasm" "$root/cmd/einv-wasm"
if [ -f "$GOROOT/lib/wasm/wasm_exec.js" ]; then
  cp "$GOROOT/lib/wasm/wasm_exec.js" "$dest/wasm_exec.js"
else
  cp "$GOROOT/misc/wasm/wasm_exec.js" "$dest/wasm_exec.js"
fi
gzip -9 -c "$dest/einv.wasm" > "$dest/einv.wasm.gz"
ls -l "$dest"
