FROM golang:1.26.7-bookworm AS dev
RUN apt-get update && apt-get install -y --no-install-recommends tmux shellcheck && rm -rf /var/lib/apt/lists/* && useradd --create-home --uid 1000 developer && mkdir /workspace && chown developer:developer /workspace
USER developer
ENV GOCACHE=/home/developer/.cache/go-build
WORKDIR /workspace
CMD ["sleep", "infinity"]

FROM dev AS build
COPY --chown=developer:developer go.mod go.sum ./
RUN go mod download
COPY --chown=developer:developer . .
RUN CGO_ENABLED=0 go build -trimpath -o /home/developer/tmux-gitlab-pipeline ./cmd/tmux-gitlab-pipeline

FROM scratch AS artifact
COPY --from=build /home/developer/tmux-gitlab-pipeline /tmux-gitlab-pipeline
