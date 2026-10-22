#!/bin/sh
set -eu
plugin_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
exec "$plugin_dir/scripts/run.sh" bind
