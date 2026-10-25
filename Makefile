# Build the agentmail images and the directory gRPC server.
#   make
#   make server
#   make charts
#   make ensure-tools
#   make clean
#   make TAG=dev
#   make DOCKER=podman
#
# make charts renders charts/agentmail to dist/agentmail.yaml. The Bitwarden
# server base is not stored in the chart. Export it, plus the organization
# and project, before rendering:
#   BITWARDEN_SERVER_URL=$BITWARDEN_SERVER_URL \
#   BITWARDEN_ORGANIZATION_ID=... \
#   BITWARDEN_PROJECT_ID=... \
#   make charts

DOCKER ?= docker
GO ?= go
HELM ?= helm
TAG ?= latest
RELEASE ?= agentmail
NAMESPACE ?= agentmail
PROTOC_GEN_GO_VERSION := v1.36.12
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2
HELM_VERSION := v3.19.0

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
export PATH := $(shell $(GO) env GOPATH)/bin:$(PATH)

.PHONY: all postfix dovecot directory server generate clean ensure-tools charts

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

charts:
	@test -n "$(BITWARDEN_SERVER_URL)" || { echo "BITWARDEN_SERVER_URL is required to compile the chart" >&2; exit 1; }
	@test -n "$(BITWARDEN_ORGANIZATION_ID)" || { echo "BITWARDEN_ORGANIZATION_ID is required to compile the chart" >&2; exit 1; }
	@test -n "$(BITWARDEN_PROJECT_ID)" || { echo "BITWARDEN_PROJECT_ID is required to compile the chart" >&2; exit 1; }
	@command -v $(HELM) >/dev/null 2>&1 || $(GO) install helm.sh/helm/v3/cmd/helm@$(HELM_VERSION)
	mkdir -p $(ROOT)/dist
	$(HELM) lint $(ROOT)/charts/agentmail \
		--set-string bitwarden.serverURL='$(BITWARDEN_SERVER_URL)' \
		--set-string bitwarden.organizationID='$(BITWARDEN_ORGANIZATION_ID)' \
		--set-string bitwarden.projectID='$(BITWARDEN_PROJECT_ID)'
	$(HELM) template $(RELEASE) $(ROOT)/charts/agentmail \
		--namespace $(NAMESPACE) \
		--set-string bitwarden.serverURL='$(BITWARDEN_SERVER_URL)' \
		--set-string bitwarden.organizationID='$(BITWARDEN_ORGANIZATION_ID)' \
		--set-string bitwarden.projectID='$(BITWARDEN_PROJECT_ID)' \
		> $(ROOT)/dist/agentmail.yaml.tmp
	mv $(ROOT)/dist/agentmail.yaml.tmp $(ROOT)/dist/agentmail.yaml

$(ROOT)/bin/directory: generate
	mkdir -p $(ROOT)/bin
	cd $(ROOT) && CGO_ENABLED=0 $(GO) build -trimpath -o $@ ./cmd/directory

clean:
	rm -rf $(ROOT)/bin
	rm -f $(ROOT)/gen/agentmail/directory/v1/*.pb.go
	rm -f $(ROOT)/dist/agentmail.yaml $(ROOT)/dist/agentmail.yaml.tmp
	@set -eu; \
	for image in agentmail-postfix agentmail-dovecot agentmail-directory; do \
		ids=$$($(DOCKER) ps -aq --filter "ancestor=$$image" || true); \
		if [ -n "$$ids" ]; then $(DOCKER) rm -f $$ids; fi; \
		imgids=$$($(DOCKER) images -q "$$image" || true); \
		if [ -n "$$imgids" ]; then $(DOCKER) rmi -f $$imgids; fi; \
	done
