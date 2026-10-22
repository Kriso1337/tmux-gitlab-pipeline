#!/bin/sh
set -eu
source_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
stage=$(mktemp -d)
trap 'rm -r "$stage"' EXIT HUP INT TERM
mkdir -p "$stage/plugin/scripts" "$stage/package/tmux-gitlab-pipeline/bin" "$stage/mock"
cp "$source_dir/scripts/install.sh" "$source_dir/scripts/update.sh" "$stage/plugin/scripts/"
cat > "$stage/package/tmux-gitlab-pipeline/bin/tmux-gitlab-pipeline" <<'BINARY'
#!/bin/sh
printf '0.5.0\n'
BINARY
chmod 755 "$stage/package/tmux-gitlab-pipeline/bin/tmux-gitlab-pipeline"
platform=$(uname -s | tr '[:upper:]' '[:lower:]')
case $(uname -m) in arm64|aarch64) arch=arm64 ;; x86_64|amd64) arch=amd64 ;; *) exit 1 ;; esac
asset="tmux-gitlab-pipeline_0.5.0_${platform}_${arch}.tar.gz"
tar -czf "$stage/$asset" -C "$stage/package" tmux-gitlab-pipeline
(cd "$stage" && sha256sum "$asset" > SHA256SUMS)
cat > "$stage/mock/curl" <<'CURL'
#!/bin/sh
set -eu
output=
url=
while [ "$#" -gt 0 ]; do
    case "$1" in -o) output=$2; shift 2 ;; https://*) url=$1; shift ;; *) shift ;; esac
done
case "$url" in
    */releases/latest) printf 'https://github.com/Kriso1337/tmux-gitlab-pipeline/releases/tag/v0.5.0' ;;
    */SHA256SUMS) cp "$TGP_INSTALL_FIXTURE/SHA256SUMS" "$output" ;;
    */tmux-gitlab-pipeline_0.5.0_*.tar.gz) cp "$TGP_INSTALL_FIXTURE/${url##*/}" "$output" ;;
    *) exit 2 ;;
esac
CURL
chmod 755 "$stage/mock/curl"
export TGP_INSTALL_FIXTURE="$stage"
export PATH="$stage/mock:$PATH"
"$stage/plugin/scripts/install.sh" 0.5.0
binary="$stage/plugin/bin/${platform}_${arch}/tmux-gitlab-pipeline"
[ "$("$binary" version)" = 0.5.0 ]
"$stage/plugin/scripts/update.sh"
before=$(sha256sum "$binary")
printf '%064d  %s\n' 0 "$asset" > "$stage/SHA256SUMS"
if "$stage/plugin/scripts/install.sh" 0.5.0 > "$stage/error" 2>&1; then printf 'Checksum mismatch was accepted\n' >&2; exit 1; fi
[ "$before" = "$(sha256sum "$binary")" ]
if "$stage/plugin/scripts/install.sh" '../invalid' > "$stage/error" 2>&1; then printf 'Invalid version was accepted\n' >&2; exit 1; fi
printf 'Installer checks passed\n'
