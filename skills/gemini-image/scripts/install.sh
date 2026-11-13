#!/bin/sh
# Installs the gemini-image binary for this system from the GitHub releases.
# Run with --help for usage.
set -eu

# GEMINI_IMAGE_REPO_URL points the script at another server (tests, mirrors).
repo_url=${GEMINI_IMAGE_REPO_URL:-https://github.com/ehedenst/gemini-image}
version=latest
dir=${XDG_BIN_HOME:-$HOME/.local/bin}

usage() {
	cat <<EOF
Usage: install.sh [--version vX.Y.Z] [--dir DIR]

Download gemini-image for this system (macOS or Linux, amd64 or arm64) from
$repo_url/releases, check it against the
release's SHA-256 checksums and install it in DIR. Prints the path of the
binary on stdout; progress goes to stderr. If DIR already has that version,
nothing is downloaded.

Options:
  --version vX.Y.Z  release to install (default: the latest)
  --dir DIR         where to install (default: \$XDG_BIN_HOME or ~/.local/bin)
  -h, --help        show this help

Exit codes: 0 installed or already there, 1 download or check failed,
2 bad arguments or unsupported system.

Needs curl, tar, and sha256sum or shasum. On Windows, use install.ps1.
EOF
}

fail() {
	echo "error: $1" >&2
	exit "${2:-1}"
}

while [ $# -gt 0 ]; do
	case $1 in
	--version | --dir)
		[ $# -ge 2 ] || fail "$1 needs a value; see --help" 2
		case $1 in --version) version=$2 ;; *) dir=$2 ;; esac
		shift 2
		;;
	--version=*) version=${1#*=} && shift ;;
	--dir=*) dir=${1#*=} && shift ;;
	-h | --help) usage && exit 0 ;;
	*) fail "unknown argument \"$1\"; see --help" 2 ;;
	esac
done

case $(uname -s) in
Darwin) os=darwin ;;
Linux) os=linux ;;
MINGW* | MSYS* | CYGWIN*) fail "on Windows, run: powershell -ExecutionPolicy Bypass -File scripts/install.ps1" 2 ;;
*) fail "no release for $(uname -s); with Go installed, run: go install github.com/ehedenst/gemini-image@latest" 2 ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) fail "no release for $(uname -m); with Go installed, run: go install github.com/ehedenst/gemini-image@latest" 2 ;;
esac
# A shell running under Rosetta reports x86_64 on Apple silicon.
if [ "$os$arch" = darwinamd64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null)" = 1 ]; then
	arch=arm64
fi

command -v curl >/dev/null || fail "curl is needed; install it and try again"
if command -v sha256sum >/dev/null; then
	sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null; then
	sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
	fail "sha256sum or shasum is needed to check the download; install one and try again"
fi

case $version in
[0-9]*) version=v$version ;;
latest)
	# github.com/OWNER/REPO/releases/latest redirects to .../releases/tag/vX.Y.Z.
	url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$repo_url/releases/latest") ||
		fail "can't reach $repo_url/releases/latest"
	case $url in
	*/releases/tag/*) version=${url##*/} ;;
	*) fail "no release found at $repo_url/releases; with Go installed, run: go install github.com/ehedenst/gemini-image@latest" ;;
	esac
	;;
esac
echo "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' ||
	fail "--version must look like v1.2.3, got \"$version\"" 2

bin=$dir/gemini-image
if [ -x "$bin" ] && [ "$("$bin" --version 2>/dev/null)" = "gemini-image version $version" ]; then
	echo "gemini-image $version is already installed" >&2
	echo "$bin"
	exit 0
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
trap 'exit 130' INT TERM

name=gemini-image_${version}_${os}_${arch}
download="$repo_url/releases/download/$version"
echo "downloading $download/$name.tar.gz" >&2
curl -fsSL -o "$tmp/$name.tar.gz" "$download/$name.tar.gz" ||
	fail "can't download $name.tar.gz; see $repo_url/releases for the available versions"
curl -fsSL -o "$tmp/checksums.txt" "$download/checksums.txt" ||
	fail "can't download checksums.txt for $version"
want=$(awk -v f="$name.tar.gz" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || fail "checksums.txt of $version has no entry for $name.tar.gz"
got=$(sha256 "$tmp/$name.tar.gz")
[ "$got" = "$want" ] || fail "checksum mismatch for $name.tar.gz: got $got, want $want; nothing installed"

tar -xzf "$tmp/$name.tar.gz" -C "$tmp" "$name/gemini-image"
mkdir -p "$dir"
# Copy next to the target, then rename, so a running or half-copied binary is never left behind.
cp "$tmp/$name/gemini-image" "$dir/.gemini-image.tmp"
chmod 755 "$dir/.gemini-image.tmp"
mv -f "$dir/.gemini-image.tmp" "$bin"
"$bin" --version >/dev/null || fail "installed $bin, but it doesn't run"

echo "installed gemini-image $version" >&2
case ":$PATH:" in
*":$dir:"*) ;;
*) echo "note: $dir is not on PATH; run gemini-image as $bin" >&2 ;;
esac
echo "$bin"
