#!/bin/sh
set -eu

version="${1:-dev}"
output="${2:-dist}"
case "$output" in
  /*) ;;
  *) output="$(pwd)/$output" ;;
esac

rm -rf "$output"
mkdir -p "$output"

commit="$(git rev-parse --short HEAD 2>/dev/null || printf 'none')"
build_date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
ldflags="-s -w -X github.com/chensunlai/codex-utils/internal/buildinfo.Version=$version -X github.com/chensunlai/codex-utils/internal/buildinfo.Commit=$commit -X github.com/chensunlai/codex-utils/internal/buildinfo.Date=$build_date"

for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  os="${target%/*}"
  arch="${target#*/}"
  stage="$output/.stage-$os-$arch"
  mkdir -p "$stage"

  binary="codex-utils"
  if [ "$os" = "windows" ]; then
    binary="codex-utils.exe"
  fi

  printf 'Building %s/%s\n' "$os" "$arch"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build \
    -buildvcs=false \
    -trimpath \
    -ldflags "$ldflags" \
    -o "$stage/$binary" \
    ./cmd/codex-utils

  cp LICENSE README.md "$stage/"
  archive="codex-utils_${os}_${arch}"
  if [ "$os" = "windows" ]; then
    (cd "$stage" && zip -q "$output/$archive.zip" "$binary" LICENSE README.md)
  else
    tar -C "$stage" -czf "$output/$archive.tar.gz" "$binary" LICENSE README.md
  fi
  rm -rf "$stage"
done

(
  cd "$output"
  sha256sum codex-utils_*.tar.gz codex-utils_*.zip > checksums.txt
)
printf 'Release artifacts: %s\n' "$output"
