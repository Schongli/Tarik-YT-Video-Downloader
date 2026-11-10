#!/bin/sh
# Fetches the yt-dlp engine, checks it against the published checksums, and builds dist/Tarik.exe.
# Works on Linux, macOS and Git Bash on Windows. Needs Go 1.24+ and curl.
set -eu
cd "$(dirname "$0")"
rel=https://github.com/yt-dlp/yt-dlp/releases/latest/download
if [ ! -f engine/yt-dlp.exe ]; then
  mkdir -p engine
  curl -fsSL -o engine/yt-dlp.exe.part "$rel/yt-dlp.exe"
  want=$(curl -fsSL "$rel/SHA2-256SUMS" | awk '$2 == "yt-dlp.exe" {print $1}')
  got=$(sha256sum engine/yt-dlp.exe.part | cut -d' ' -f1)
  if [ -z "$want" ] || [ "$got" != "$want" ]; then
    echo "yt-dlp.exe does not match the published checksum; not using it (left at engine/yt-dlp.exe.part)" >&2
    exit 1
  fi
  mv engine/yt-dlp.exe.part engine/yt-dlp.exe
fi
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H windowsgui" -o dist/Tarik.exe .
echo "built dist/Tarik.exe"
