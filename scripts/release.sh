#!/bin/sh
# Publish the tag at HEAD as a GitHub Release with static binaries of the einv
# CLI, then prove the assets are actually downloadable without credentials.
#
# A release exists for two reasons. Developers who want the CLI in CI should not
# have to install a Go toolchain to get it, and the download counters on the
# assets are the only demand signal this project can measure without an account
# of any kind: stars are a bookmark, a download is somebody running the thing.
#
#   ./scripts/release.sh              # needs a token with contents:write
#   GITHUB_TOKEN=... ./scripts/release.sh
#
# Re-running is safe: the release and any asset already present are left alone.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
platforms='linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64'

cd "$root"
tag=$(git describe --tags --exact-match HEAD 2>/dev/null) || {
  echo 'HEAD is not tagged; tag the release before publishing binaries.' >&2
  exit 1
}
version=${tag#v}
grep -q "const Version = \"$version\"" einvoice.go || {
  echo "tag $tag disagrees with einvoice.Version; refusing to publish misleading binaries." >&2
  exit 1
}

token=${GITHUB_TOKEN:-}
[ -n "$token" ] || token=$(printf 'protocol=https\nhost=github.com\n\n' | git credential fill 2>/dev/null \
  | sed -n 's/^password=//p')
[ -n "$token" ] || { echo 'no GitHub token: set GITHUB_TOKEN or store credentials for github.com.' >&2; exit 1; }
remote=$(git remote get-url origin)
slug=$(printf '%s' "$remote" | sed -E 's#.*(github\.com[:/])##; s#\.git$##; s#^/##')
api=https://api.github.com/repos/$slug

dist=$(mktemp -d)
trap 'rm -rf "$dist"' EXIT
for p in $platforms; do
  goos=${p%/*}
  goarch=${p#*/}
  name="einv-$version-$goos-$goarch"
  bin=einv
  [ "$goos" = windows ] && bin=einv.exe
  out="$dist/$name"
  mkdir -p "$out"
  CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath -ldflags='-s -w' -o "$out/$bin" ./cmd/einv
  cp LICENSE NOTICE.md "$out/"
  (cd "$out" && tar czf "../$name.tar.gz" .)
done
(cd "$dist" && sha256sum ./*.tar.gz > SHA256SUMS.txt)

status=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $token" "$api/releases/tags/$tag")
case "$status" in
  404)
    body=$(GH_TAG="$tag" GH_VERSION="$version" node -e '
const { GH_TAG: tag, GH_VERSION: version } = process.env;
const body = `## einv ${version}

A static binary of the \`einv\` command: EN 16931 validation of CII and UBL\ne-invoices, Factur-X/ZUGFeRD PDF auditing, CII↔UBL conversion, and generation\nfrom a JSON invoice. No Java, no network calls, no CGO — one file that runs in CI.

\`\`\`
einv validate invoice.xml          # exit 0 clean, 1 findings, 2 unreadable
einv check dist/invoices/*.pdf     # PDF/A-3, the attachment and the XML inside it
einv convert invoice.xml --to ubl
\`\`\`

The same release is also a GitHub Action: \`uses: DCNWS/invoicekit.go@v${version}\`
validates the invoice XML a pull request touches, on the runner, and turns every
finding into an annotation titled with the rule id.

Rule sets: EN 16931 v1.3.17, XRechnung CIUS 2.6.0 (XRechnung 3.0.2), Peppol BIS\nBilling 3.0 release 3.0.21, all embedded at build time. The library itself is\n\`go get github.com/dcnws/invoicekit.go\`; these binaries are the same module\nbuilt with \`CGO_ENABLED=0\` and verified on the CEF conformance documents.

Checksums: \`sha256sum -c SHA256SUMS.txt\`.`;
process.stdout.write(JSON.stringify({ tag_name: tag, name: `einv ${version}`, body, draft: false, prerelease: false }));
')
    release=$(curl -s -X POST -H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
      -d "$body" "$api/releases")
    id=$(printf '%s' "$release" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>{const j=JSON.parse(d);if(!j.id){console.error(j.message||"no release id");process.exit(1)}console.log(j.id)})')
    ;;
  200)
    id=$(curl -s -H "Authorization: Bearer $token" "$api/releases/tags/$tag" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>{console.log(JSON.parse(d).id)})')
    ;;
  *) echo "could not look up the release for $tag: HTTP $status" >&2; exit 1;;
esac

existing=$(curl -s -H "Authorization: Bearer $token" "$api/releases/$id" \
  | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>{console.log((JSON.parse(d).assets||[]).map(a=>a.name).join(" "))})')
for f in "$dist"/*.tar.gz "$dist"/SHA256SUMS.txt; do
  name=$(basename "$f")
  case " $existing " in
    *" $name "*) echo "asset $name already published"; continue;;
  esac
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
    -H "Authorization: Bearer $token" -H 'Content-Type: application/octet-stream' \
    --data-binary "@$f" "https://uploads.github.com/repos/$slug/releases/$id/assets?name=$name")
  [ "$code" = 201 ] || { echo "uploading $name failed: HTTP $code" >&2; exit 1; }
  echo "uploaded $name"
done

# Prove the point of the whole exercise: someone with no GitHub account can see
# the release and count what it has been asked for.
public=$(curl -s "$api/releases/tags/$tag")
printf '%s' "$public" | node -e '
let d = "";
process.stdin.on("data", (c) => (d += c)).on("end", () => {
  const j = JSON.parse(d);
  if (!j.url) { console.error("the release is not readable without credentials"); process.exit(1); }
  const assets = j.assets || [];
  console.log(`release ${j.tag_name}: ${assets.length} assets, ${assets.reduce((n, a) => n + a.download_count, 0)} downloads`);
});'
echo "https://github.com/$slug/releases/tag/$tag"
