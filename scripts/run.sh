#!/bin/sh
set -eu

repository="chensunlai/codex-utils"
version="${CODEX_UTILS_VERSION:-latest}"

case "$(uname -s)" in
  Linux) os="linux" ;;
  Darwin) os="darwin" ;;
  *) printf 'Unsupported operating system: %s\n' "$(uname -s)" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) printf 'Unsupported architecture: %s\n' "$(uname -m)" >&2; exit 1 ;;
esac

asset="codex-utils_${os}_${arch}.tar.gz"
if [ "$version" = "latest" ]; then
  download_base="https://github.com/$repository/releases/latest/download"
else
  case "$version" in v*) tag="$version" ;; *) tag="v$version" ;; esac
  download_base="https://github.com/$repository/releases/download/$tag"
fi

temporary="$(mktemp -d 2>/dev/null || mktemp -d -t codex-utils)"
cleanup() {
  rm -rf "$temporary"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

printf 'Downloading temporary codex-utils...\n'
curl -fsSL --retry 3 --connect-timeout 15 -o "$temporary/$asset" "$download_base/$asset"
curl -fsSL --retry 3 --connect-timeout 15 -o "$temporary/checksums.txt" "$download_base/checksums.txt"

expected="$(awk -v name="$asset" '$2 == name { print $1; exit }' "$temporary/checksums.txt")"
if [ -z "$expected" ]; then
  printf 'Checksum entry is missing for %s\n' "$asset" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$temporary/$asset" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$temporary/$asset" | awk '{print $1}')"
else
  actual="$(openssl dgst -sha256 "$temporary/$asset" | awk '{print $NF}')"
fi
if [ "$expected" != "$actual" ]; then
  printf 'Checksum verification failed for %s\n' "$asset" >&2
  exit 1
fi

tar -xzf "$temporary/$asset" -C "$temporary" codex-utils
chmod 0755 "$temporary/codex-utils"

if [ "$#" -gt 0 ]; then
  "$temporary/codex-utils" "$@"
elif [ -r /dev/tty ]; then
  "$temporary/codex-utils" </dev/tty
else
  "$temporary/codex-utils"
fi
