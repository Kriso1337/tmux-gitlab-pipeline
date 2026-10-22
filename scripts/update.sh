#!/bin/sh
set -eu
plugin_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
if [ "$#" -gt 1 ]; then printf 'Usage: update.sh [version]\n' >&2; exit 1; fi
release=${1:-}
if [ -z "$release" ]; then
    location=$(curl --proto '=https' --tlsv1.2 -fLsS -I --connect-timeout 10 --max-time 30 -o /dev/null -w '%{url_effective}' https://github.com/Kriso1337/tmux-gitlab-pipeline/releases/latest)
    case "$location" in https://github.com/Kriso1337/tmux-gitlab-pipeline/releases/tag/v*) release=${location##*/v} ;; *) printf 'Cannot resolve latest release\n' >&2; exit 1 ;; esac
fi
exec "$plugin_dir/scripts/install.sh" "$release"
