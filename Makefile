# Build the agentmail images and the directory gRPC server.
#   make
#   make server
#   make ensure-tools
#   make clean
#   make TAG=dev
#   make DOCKER=podman

DOCKER ?= docker
GO ?= go
TAG ?= latest
PROTOC_GEN_GO_VERSION := v1.36.12
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
export PATH := $(shell $(GO) env GOPATH)/bin:$(PATH)

.PHONY: all postfix dovecot directory server generate clean ensure-tools

all: postfix dovecot directory server

postfix:
	$(DOCKER) build -f $(ROOT)/postfix/Dockerfile -t agentmail-postfix:$(TAG) $(ROOT)

dovecot:
	$(DOCKER) build -f $(ROOT)/dovecot/Dockerfile -t agentmail-dovecot:$(TAG) $(ROOT)

directory: generate
	$(DOCKER) build -f $(ROOT)/directory/Dockerfile -t agentmail-directory:$(TAG) $(ROOT)

server: $(ROOT)/bin/directory

ensure-tools:
	@command -v $(GO) >/dev/null 2>&1 || { echo "go is required to build" >&2; exit 1; }
	@command -v protoc >/dev/null 2>&1 || { echo "protoc is required to generate the directory service" >&2; exit 1; }
	@command -v protoc-gen-go >/dev/null 2>&1 || $(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	@command -v protoc-gen-go-grpc >/dev/null 2>&1 || $(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)

generate: ensure-tools
	cd $(ROOT) && $(GO) generate ./...

$(ROOT)/bin/directory: generate
	mkdir -p $(ROOT)/bin
	cd $(ROOT) && CGO_ENABLED=0 $(GO) build -trimpath -o $@ ./cmd/directory

clean:
	rm -rf $(ROOT)/bin
	rm -f $(ROOT)/gen/agentmail/directory/v1/*.pb.go
	@set -eu; \
	for image in agentmail-postfix agentmail-dovecot agentmail-directory; do \
		ids=$$($(DOCKER) ps -aq --filter "ancestor=$$image" || true); \
		if [ -n "$$ids" ]; then $(DOCKER) rm -f $$ids; fi; \
		imgids=$$($(DOCKER) images -q "$$image" || true); \
		if [ -n "$$imgids" ]; then $(DOCKER) rmi -f $$imgids; fi; \
	done
