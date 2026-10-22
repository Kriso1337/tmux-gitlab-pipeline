#!/bin/sh
set -eu
plugin_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
platform=$(uname -s | tr '[:upper:]' '[:lower:]')
case $(uname -m) in
    arm64|aarch64) arch=arm64 ;;
    x86_64|amd64) arch=amd64 ;;
    *) printf 'Unsupported architecture\n' >&2; exit 1 ;;
esac
for binary in "$plugin_dir/bin/${platform}_${arch}/tmux-gitlab-pipeline" "$plugin_dir/bin/tmux-gitlab-pipeline"; do
    if [ -x "$binary" ]; then exec "$binary" "$@"; fi
done
if command -v tmux-gitlab-pipeline >/dev/null 2>&1; then exec tmux-gitlab-pipeline "$@"; fi
"$plugin_dir/scripts/install.sh"
exec "$plugin_dir/bin/${platform}_${arch}/tmux-gitlab-pipeline" "$@"
