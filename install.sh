#!/bin/sh
# Installs the workshell binary (the server and the `workshell release` CLI)
# from GitHub Releases, after checking it against the release's SHA256SUMS.
#
#   curl -fsSL https://raw.githubusercontent.com/Shellworks-Development/workshell/master/install.sh | sh
#
# Environment:
#   WORKSHELL_VERSION   release tag to install, e.g. v0.1.0 (default: latest)
#   WORKSHELL_INSTALL   directory to install into (default: /usr/local/bin if
#                       writable, else ~/.local/bin)
set -eu

repo="Shellworks-Development/workshell"

fail() {
	echo "workshell install: $*" >&2
	exit 1
}

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) fail "unsupported OS $(uname -s); on Windows, download the .zip from https://github.com/$repo/releases" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) fail "unsupported architecture $(uname -m)" ;;
esac

version="${WORKSHELL_VERSION:-}"
if [ -z "$version" ]; then
	# /releases/latest redirects to /releases/tag/<tag>.
	version="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest")"
	version="${version##*/}"
	case "$version" in v*) ;; *) fail "couldn't find the latest release" ;; esac
fi

dir="${WORKSHELL_INSTALL:-}"
if [ -z "$dir" ]; then
	if [ -w /usr/local/bin ]; then dir=/usr/local/bin; else dir="$HOME/.local/bin"; fi
fi
mkdir -p "$dir"

name="workshell_${version}_${os}_${arch}"
base="https://github.com/$repo/releases/download/$version"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading workshell $version ($os/$arch)"
curl -fsSL -o "$tmp/$name.tar.gz" "$base/$name.tar.gz" || fail "no build for $os/$arch in $version"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" || fail "couldn't download SHA256SUMS"

expected="$(grep " $name.tar.gz\$" "$tmp/SHA256SUMS" | cut -d' ' -f1)"
[ -n "$expected" ] || fail "$name.tar.gz is not listed in SHA256SUMS"
if command -v sha256sum >/dev/null 2>&1; then
	actual="$(sha256sum "$tmp/$name.tar.gz" | cut -d' ' -f1)"
else
	actual="$(shasum -a 256 "$tmp/$name.tar.gz" | cut -d' ' -f1)"
fi
[ "$expected" = "$actual" ] || fail "checksum mismatch for $name.tar.gz"

tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
install -m 0755 "$tmp/$name/workshell" "$dir/workshell" 2>/dev/null || {
	cp "$tmp/$name/workshell" "$dir/workshell" && chmod 0755 "$dir/workshell"
}

echo "Installed $("$dir/workshell" version) to $dir/workshell"
case ":$PATH:" in
*":$dir:"*) ;;
*) echo "Add $dir to your PATH to run it as \`workshell\`." ;;
esac
