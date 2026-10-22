#!/bin/sh
set -eu
for platform in linux darwin; do
    for arch in amd64 arm64; do
        mkdir -p "bin/${platform}_${arch}"
        CGO_ENABLED=0 GOOS="$platform" GOARCH="$arch" go build -trimpath -ldflags '-s -w' -o "bin/${platform}_${arch}/tmux-gitlab-pipeline.new" ./cmd/tmux-gitlab-pipeline
        mv "bin/${platform}_${arch}/tmux-gitlab-pipeline.new" "bin/${platform}_${arch}/tmux-gitlab-pipeline"
    done
done
