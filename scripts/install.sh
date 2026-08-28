#!/bin/sh
set -eu

repository="chensunlai/codex-utils"
version="${CODEX_UTILS_VERSION:-latest}"
install_dir="${CODEX_UTILS_INSTALL_DIR:-$HOME/.local/bin}"

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
trap 'rm -rf "$temporary"' EXIT HUP INT TERM

printf 'Downloading %s...\n' "$asset"
curl -fL --retry 3 --connect-timeout 15 -o "$temporary/$asset" "$download_base/$asset"
curl -fL --retry 3 --connect-timeout 15 -o "$temporary/checksums.txt" "$download_base/checksums.txt"

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
mkdir -p "$install_dir"
install -m 0755 "$temporary/codex-utils" "$install_dir/codex-utils"

printf 'Installed codex-utils to %s\n' "$install_dir/codex-utils"
case ":$PATH:" in
  *":$install_dir:"*) ;;
  *) printf 'Add %s to PATH, or run the binary by its full path.\n' "$install_dir" ;;
esac
