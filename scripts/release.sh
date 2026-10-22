#!/bin/sh
set -eu
version=${1:-0.5.1}
case "$version" in
    ''|*[!0-9.]*|.*|*..*) printf 'Invalid release version\n' >&2; exit 1 ;;
esac
mkdir -p dist
bundle=$(mktemp -d)
trap 'rm -r "$bundle"' EXIT HUP INT TERM
copy_support() {
    mkdir -p "$1/scripts" "$1/examples"
    cp gitlab-pipeline.tmux LICENSE INSTALL.txt "$1/"
    cp scripts/run.sh scripts/install.sh scripts/update.sh "$1/scripts/"
    cp examples/tmux.conf examples/alacritty.toml examples/claude-hooks.json examples/codex-hooks.json "$1/examples/"
}
for platform in linux darwin; do
    for arch in amd64 arm64; do
        stage=$(mktemp -d)
        mkdir -p "$stage/tmux-gitlab-pipeline/bin" "$stage/tmux-gitlab-pipeline/scripts" "$stage/tmux-gitlab-pipeline/examples"
        CGO_ENABLED=0 GOOS="$platform" GOARCH="$arch" go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$stage/tmux-gitlab-pipeline/bin/tmux-gitlab-pipeline" ./cmd/tmux-gitlab-pipeline
        copy_support "$stage/tmux-gitlab-pipeline"
        mkdir -p "$bundle/tmux-gitlab-pipeline/bin/${platform}_${arch}"
        cp "$stage/tmux-gitlab-pipeline/bin/tmux-gitlab-pipeline" "$bundle/tmux-gitlab-pipeline/bin/${platform}_${arch}/"
        tar -czf "dist/tmux-gitlab-pipeline_${version}_${platform}_${arch}.tar.gz" -C "$stage" tmux-gitlab-pipeline
        rm -r "$stage"
    done
done
copy_support "$bundle/tmux-gitlab-pipeline"
tar -czf "dist/tmux-gitlab-pipeline_${version}_all-platforms.tar.gz" -C "$bundle" tmux-gitlab-pipeline
cd dist
sha256sum "tmux-gitlab-pipeline_${version}_"*.tar.gz > SHA256SUMS
