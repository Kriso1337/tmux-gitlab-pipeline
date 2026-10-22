#!/bin/sh
set -eu
plugin_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
release=${1:-0.5.1}
case "$release" in ''|*[!0-9.]*|.*|*..*) printf 'Invalid release version\n' >&2; exit 1 ;; esac
platform=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$platform" in darwin|linux) ;; *) printf 'Unsupported platform\n' >&2; exit 1 ;; esac
case $(uname -m) in arm64|aarch64) arch=arm64 ;; x86_64|amd64) arch=amd64 ;; *) printf 'Unsupported architecture\n' >&2; exit 1 ;; esac
command -v curl >/dev/null 2>&1 || { printf 'curl is required to install a release\n' >&2; exit 1; }
asset="tmux-gitlab-pipeline_${release}_${platform}_${arch}.tar.gz"
base="https://github.com/Kriso1337/tmux-gitlab-pipeline/releases/download/v${release}"
stage=$(mktemp -d)
trap 'rm -r "$stage"' EXIT HUP INT TERM
curl --proto '=https' --tlsv1.2 -fLsS --connect-timeout 10 --max-time 120 --max-filesize 33554432 "$base/$asset" -o "$stage/$asset"
curl --proto '=https' --tlsv1.2 -fLsS --connect-timeout 10 --max-time 30 --max-filesize 16384 "$base/SHA256SUMS" -o "$stage/SHA256SUMS"
expected=$(awk -v asset="$asset" '$2 == asset { print $1 }' "$stage/SHA256SUMS")
case "$expected" in ''|*[!0-9a-f]*) printf 'Invalid release checksum\n' >&2; exit 1 ;; esac
[ "${#expected}" -eq 64 ] || { printf 'Invalid release checksum length\n' >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then actual=$(sha256sum "$stage/$asset" | awk '{print $1}'); else actual=$(shasum -a 256 "$stage/$asset" | awk '{print $1}'); fi
[ "$actual" = "$expected" ] || { printf 'Release checksum mismatch\n' >&2; exit 1; }
mkdir -p "$plugin_dir/bin/${platform}_${arch}"
target="$plugin_dir/bin/${platform}_${arch}/tmux-gitlab-pipeline"
new=$(mktemp "${target}.new.XXXXXX")
trap 'rm -r "$stage"; if [ -f "$new" ]; then rm "$new"; fi' EXIT HUP INT TERM
tar -xOf "$stage/$asset" tmux-gitlab-pipeline/bin/tmux-gitlab-pipeline > "$new"
[ "$(wc -c < "$new")" -le 33554432 ] || { printf 'Binary size limit exceeded\n' >&2; exit 1; }
chmod 755 "$new"
[ "$("$new" version)" = "$release" ] || { printf 'Release binary version mismatch\n' >&2; exit 1; }
mv -f "$new" "$target"
printf 'Installed tmux-gitlab-pipeline %s\n' "$release"
