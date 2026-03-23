BINARY     := score-orchestrator
CONFIG     ?= orchestrator.yaml
PORT       ?= 8080
ORG        ?= my-org
ENV        ?= dev
WORKLOAD   ?= my-service
SCORE      ?= score.yaml

.PHONY: build run-server run-deploy clean fmt vet tidy help

## build: compile the binary
build:
	go build -o $(BINARY) .

## run-server: build and start the HTTP server (PORT=8080 by default)
run-server: build
	./$(BINARY) server --config $(CONFIG) --port $(PORT)

## run-deploy: deploy a workload via CLI
##   make run-deploy ORG=my-org ENV=prod WORKLOAD=payment-service SCORE=./payment-service.yaml
run-deploy: build
	./$(BINARY) deploy \
		--config $(CONFIG) \
		--org $(ORG) \
		--env $(ENV) \
		--workload $(WORKLOAD) \
		--score $(SCORE)

## fmt: format all Go source files
fmt:
	go fmt ./...

## vet: run go vet
vet:
	go vet ./...

## tidy: tidy go.mod and go.sum
tidy:
	go mod tidy

## clean: remove compiled binary
clean:
	rm -f $(BINARY)

## help: show this help
help:
	@grep -E '^## ' Makefile | sed 's/^## //'
