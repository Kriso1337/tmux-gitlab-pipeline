CONTAINER := tmux-gitlab-pipeline-dev
IMAGE := tmux-gitlab-pipeline-dev:0.1.0

.PHONY: dev test check lint build release stop
dev:
	docker build --target dev -t $(IMAGE) .
	@if docker container inspect $(CONTAINER) >/dev/null 2>&1; then docker start $(CONTAINER); else docker run --detach --name $(CONTAINER) --mount type=bind,source="$(CURDIR)",target=/workspace $(IMAGE); fi

test:
	docker exec $(CONTAINER) go test -race ./...
	docker exec $(CONTAINER) sh scripts/test-install.sh

check:
	docker exec $(CONTAINER) sh -c 'test -z "$$(gofmt -l cmd internal)" && go vet ./... && shellcheck gitlab-pipeline.tmux scripts/*.sh'

lint:
	docker exec $(CONTAINER) go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run ./...

build:
	docker exec $(CONTAINER) sh scripts/build.sh

release:
	docker exec $(CONTAINER) sh scripts/release.sh

stop:
	docker stop $(CONTAINER)
