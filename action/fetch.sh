#!/bin/sh
# Downloads the einv binary for this runner from our own releases and verifies
# it. No container image, no toolchain: one static binary plus its checksum.
set -eu

slug=DCNWS/invoicekit.go

case "$RUNNER_OS" in
	Linux) goos=linux ;;
	macOS) goos=darwin ;;
	Windows) goos=windows ;;
	*)
		echo "::error::unsupported RUNNER_OS=$RUNNER_OS" >&2
		exit 1
		;;
esac
case "$RUNNER_ARCH" in
	X64) goarch=amd64 ;;
	ARM64) goarch=arm64 ;;
	*)
		echo "::error::unsupported RUNNER_ARCH=$RUNNER_ARCH" >&2
		exit 1
		;;
esac

tag=${1:-}
if [ -z "$tag" ]; then
	tag=$(curl -fsSL "https://api.github.com/repos/$slug/releases/latest" |
		sed -n 's/^  "tag_name": "\(.*\)",$/\1/p' | head -1)
fi
[ -n "$tag" ] || {
	echo "::error::could not resolve a release tag for $slug" >&2
	exit 1
}
version=${tag#v}
asset="einv-$version-$goos-$goarch.tar.gz"
base="https://github.com/$slug/releases/download/$tag"

dest=$RUNNER_TEMP/invoicekit
mkdir -p "$dest"

# The checksum file comes from the same release, so a truncated or tampered
# asset fails here rather than in somebody's merge gate.
curl -fsSL -o "$dest/SHA256SUMS.txt" "$base/SHA256SUMS.txt"
curl -fsSL -o "$dest/$asset" "$base/$asset"
line=$(grep "$asset\$" "$dest/SHA256SUMS.txt") || {
	echo "::error::$asset is not named in SHA256SUMS.txt" >&2
	exit 1
}
printf '%s\n' "$line" | (cd "$dest" && sha256sum -c -)

tar xzf "$dest/$asset" -C "$dest"
[ -x "$dest/einv" ] || {
	echo "::error::$asset contains no executable einv" >&2
	exit 1
}
printf '%s\n' "$dest" >>"$GITHUB_PATH"
# The usage banner is what the binary calls itself; there is no version verb.
"$dest/einv" 2>&1 | sed -n 1p
