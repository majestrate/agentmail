# Build the agentmail images and the directory gRPC server.
#   make
#   make server
#   make charts
#   make apply
#   make teardown
#   make push
#   make ensure-tools
#   make clean
#   make TAG=dev
#   make DOCKER=podman
#
# TAG defaults to the Unix time of this make invocation. The three built
# images and the chart tags for those images share that value. Postgres
# stays at the tag in values.yaml.
#
# make charts renders charts/agentmail to dist/agentmail.yaml and the
# vaultwarden-kubernetes-secrets chart to
# dist/vaultwarden-kubernetes-secrets.yaml.
#   DOCKER_REGISTRY=$DOCKER_REGISTRY \
#   VAULTWARDEN_SERVER_URL=$VAULTWARDEN_SERVER_URL \
#   make charts
# make apply renders the charts, creates Secret vaultwarden-kubernetes-secrets
# from BW_CLIENTID, BW_CLIENTSECRET, and VAULTWARDEN__MASTERPASSWORD
# (or BW_MASTER_PASSWORD), applies dist/vaultwarden-kubernetes-secrets.yaml
# in its namespace, then applies dist/agentmail.yaml. It does not generate
# Vaultwarden items.
# make teardown deletes the mail namespace and the vaultwarden-kubernetes-secrets
# namespace, including its credential Secret, plus that release's ClusterRole
# and ClusterRoleBinding.
# make push tags the three built images with DOCKER_REGISTRY and pushes them.
#   DOCKER_REGISTRY=$DOCKER_REGISTRY make push

DOCKER ?= docker
GO ?= go
HELM ?= helm
KUBECTL ?= kubectl
TAG ?= latest
# Registry host or host/path. Empty leaves chart repositories as written.
# make push requires it. One trailing slash is removed.
DOCKER_REGISTRY ?=
DOCKER_REGISTRY_TRIMMED := $(patsubst %/,%,$(DOCKER_REGISTRY))
RELEASE ?= agentmail
NAMESPACE ?= agentmail
VWK8S_CHART := oci://ghcr.io/antoniolago/charts/vaultwarden-kubernetes-secrets
VWK8S_VERSION := 2.1.0
VWK8S_RELEASE ?= vaultwarden-kubernetes-secrets
VWK8S_NAMESPACE ?= vaultwarden-kubernetes-secrets
# One trailing slash is removed so the sync client does not join a double slash.
VAULTWARDEN_SERVER_URL_TRIMMED := $(patsubst %/,%,$(VAULTWARDEN_SERVER_URL))
PROTOC_GEN_GO_VERSION := v1.36.12
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2
HELM_VERSION := v3.19.0

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
export PATH := $(shell $(GO) env GOPATH)/bin:$(PATH)

.PHONY: all postfix dovecot directory server generate clean ensure-tools charts apply teardown push push-check

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
	@test -n "$(VAULTWARDEN_SERVER_URL_TRIMMED)" || { echo "VAULTWARDEN_SERVER_URL is required to render the vaultwarden chart" >&2; exit 1; }
	@printf '%s' '$(VAULTWARDEN_SERVER_URL_TRIMMED)' | grep -Eq '^https://[^[:space:]]+$$' || { echo "VAULTWARDEN_SERVER_URL must be an https URL" >&2; exit 1; }
	@command -v $(HELM) >/dev/null 2>&1 || $(GO) install helm.sh/helm/v3/cmd/helm@$(HELM_VERSION)
	mkdir -p $(ROOT)/dist
	$(HELM) lint $(ROOT)/charts/agentmail \
		--set-string images.registry='$(DOCKER_REGISTRY)' \
		--set-string images.postfix.tag='$(TAG)' \
		--set-string images.dovecot.tag='$(TAG)' \
		--set-string images.directory.tag='$(TAG)'
	$(HELM) template $(RELEASE) $(ROOT)/charts/agentmail \
		--namespace $(NAMESPACE) \
		--set-string images.registry='$(DOCKER_REGISTRY)' \
		--set-string images.postfix.tag='$(TAG)' \
		--set-string images.dovecot.tag='$(TAG)' \
		--set-string images.directory.tag='$(TAG)' \
		> $(ROOT)/dist/agentmail.yaml.tmp
	mv $(ROOT)/dist/agentmail.yaml.tmp $(ROOT)/dist/agentmail.yaml
	@set -eu; \
	chart=$$(mktemp -d); \
	trap 'rm -rf "$$chart"' EXIT; \
	$(HELM) pull $(VWK8S_CHART) --version $(VWK8S_VERSION) --untar --untardir "$$chart"; \
	$(HELM) lint "$$chart/vaultwarden-kubernetes-secrets" \
		--set namespace.create=true \
		--set-string env.config.VAULTWARDEN__SERVERURL='$(VAULTWARDEN_SERVER_URL_TRIMMED)' \
		$(if $(VAULTWARDEN_ORGANIZATION_ID),--set-string env.config.VAULTWARDEN__ORGANIZATIONID='$(VAULTWARDEN_ORGANIZATION_ID)',) \
		$(if $(VAULTWARDEN_COLLECTION_ID),--set-string env.config.VAULTWARDEN__COLLECTIONID='$(VAULTWARDEN_COLLECTION_ID)',); \
	$(HELM) template $(VWK8S_RELEASE) "$$chart/vaultwarden-kubernetes-secrets" \
		--namespace $(VWK8S_NAMESPACE) \
		--set namespace.create=true \
		--set-string env.config.VAULTWARDEN__SERVERURL='$(VAULTWARDEN_SERVER_URL_TRIMMED)' \
		$(if $(VAULTWARDEN_ORGANIZATION_ID),--set-string env.config.VAULTWARDEN__ORGANIZATIONID='$(VAULTWARDEN_ORGANIZATION_ID)',) \
		$(if $(VAULTWARDEN_COLLECTION_ID),--set-string env.config.VAULTWARDEN__COLLECTIONID='$(VAULTWARDEN_COLLECTION_ID)',) \
		> $(ROOT)/dist/vaultwarden-kubernetes-secrets.yaml.tmp; \
	mv $(ROOT)/dist/vaultwarden-kubernetes-secrets.yaml.tmp $(ROOT)/dist/vaultwarden-kubernetes-secrets.yaml

apply: charts
	@command -v $(KUBECTL) >/dev/null 2>&1 || { echo "kubectl is required to apply the chart" >&2; exit 1; }
	$(KUBECTL) create namespace $(VWK8S_NAMESPACE) --dry-run=client -o yaml | $(KUBECTL) apply -f -
	@set -eu; \
	ns='$(VWK8S_NAMESPACE)'; \
	client_id=$${BW_CLIENTID-}; \
	client_secret=$${BW_CLIENTSECRET-}; \
	master=$${VAULTWARDEN__MASTERPASSWORD-}; \
	if [ -z "$$master" ]; then master=$${BW_MASTER_PASSWORD-}; fi; \
	if [ -n "$$client_id" ] || [ -n "$$client_secret" ] || [ -n "$${VAULTWARDEN__MASTERPASSWORD-}" ]; then \
		if [ -z "$$client_id" ] || [ -z "$$client_secret" ] || [ -z "$$master" ]; then \
			echo "BW_CLIENTID, BW_CLIENTSECRET, and VAULTWARDEN__MASTERPASSWORD are required together" >&2; \
			exit 1; \
		fi; \
		umask 077; \
		tmp=$$(mktemp); \
		trap 'rm -f "$$tmp"' EXIT; \
		id_b64=$$(printf '%s' "$$client_id" | openssl base64 -A); \
		secret_b64=$$(printf '%s' "$$client_secret" | openssl base64 -A); \
		master_b64=$$(printf '%s' "$$master" | openssl base64 -A); \
		printf '%s\n' \
			'apiVersion: v1' \
			'kind: Secret' \
			'metadata:' \
			'  name: vaultwarden-kubernetes-secrets' \
			"  namespace: $$ns" \
			'type: Opaque' \
			'data:' \
			"  BW_CLIENTID: $$id_b64" \
			"  BW_CLIENTSECRET: $$secret_b64" \
			"  VAULTWARDEN__MASTERPASSWORD: $$master_b64" \
			> "$$tmp"; \
		$(KUBECTL) apply -f "$$tmp"; \
	elif $(KUBECTL) get secret vaultwarden-kubernetes-secrets -n "$$ns" >/dev/null 2>&1; then \
		:; \
	else \
		echo "BW_CLIENTID, BW_CLIENTSECRET, and VAULTWARDEN__MASTERPASSWORD are required. Secret vaultwarden-kubernetes-secrets was not found in namespace $$ns." >&2; \
		exit 1; \
	fi
	$(KUBECTL) apply -n $(VWK8S_NAMESPACE) -f $(ROOT)/dist/vaultwarden-kubernetes-secrets.yaml
	$(KUBECTL) create namespace $(NAMESPACE) --dry-run=client -o yaml | $(KUBECTL) apply -f -
	$(KUBECTL) apply -f $(ROOT)/dist/agentmail.yaml

teardown:
	@command -v $(KUBECTL) >/dev/null 2>&1 || { echo "kubectl is required to delete the namespace" >&2; exit 1; }
	@mail_ns='$(NAMESPACE)'; \
	sync_ns='$(VWK8S_NAMESPACE)'; \
	release='$(VWK8S_RELEASE)'; \
	label='^[a-z0-9]([-a-z0-9]*[a-z0-9])?$$'; \
	printf '%s' "$$mail_ns" | grep -Eq "$$label" || { echo "NAMESPACE must be a DNS label" >&2; exit 1; }; \
	printf '%s' "$$sync_ns" | grep -Eq "$$label" || { echo "VWK8S_NAMESPACE must be a DNS label" >&2; exit 1; }; \
	printf '%s' "$$release" | grep -Eq "$$label" || { echo "VWK8S_RELEASE must be a DNS label" >&2; exit 1; }; \
	for ns in "$$mail_ns" "$$sync_ns"; do \
		case "$$ns" in \
			default|kube-system|kube-public|kube-node-lease) \
				echo "refusing to delete namespace $$ns" >&2; \
				exit 1; \
				;; \
		esac; \
	done; \
	if [ -f $(ROOT)/dist/vaultwarden-kubernetes-secrets.yaml ]; then \
		$(KUBECTL) delete --ignore-not-found -n "$$sync_ns" -f $(ROOT)/dist/vaultwarden-kubernetes-secrets.yaml; \
	fi
	$(KUBECTL) delete clusterrole $(VWK8S_RELEASE) --ignore-not-found
	$(KUBECTL) delete clusterrolebinding $(VWK8S_RELEASE) --ignore-not-found
	$(KUBECTL) delete namespace $(VWK8S_NAMESPACE) --ignore-not-found
	$(KUBECTL) delete namespace $(NAMESPACE) --ignore-not-found

# push-check is first so a missing registry fails before the image builds.
push: push-check postfix dovecot directory
	@set -eu; \
	for name in agentmail-postfix agentmail-dovecot agentmail-directory; do \
		$(DOCKER) tag "$$name:$(TAG)" "$(DOCKER_REGISTRY_TRIMMED)/$$name:$(TAG)"; \
		$(DOCKER) push "$(DOCKER_REGISTRY_TRIMMED)/$$name:$(TAG)"; \
	done

push-check:
	@registry='$(DOCKER_REGISTRY_TRIMMED)'; \
	if [ -z "$$registry" ]; then \
		echo "DOCKER_REGISTRY is required to push images" >&2; \
		exit 1; \
	fi; \
	case "$$registry" in \
		*://*|*" "*) \
			echo "DOCKER_REGISTRY must be a registry host or host/path, without a URL scheme" >&2; \
			exit 1; \
			;; \
	esac

$(ROOT)/bin/directory: generate
	mkdir -p $(ROOT)/bin
	cd $(ROOT) && CGO_ENABLED=0 $(GO) build -trimpath -o $@ ./cmd/directory

clean:
	rm -rf $(ROOT)/bin
	rm -f $(ROOT)/gen/agentmail/directory/v1/*.pb.go
	rm -f $(ROOT)/dist/agentmail.yaml $(ROOT)/dist/agentmail.yaml.tmp \
		$(ROOT)/dist/vaultwarden-kubernetes-secrets.yaml $(ROOT)/dist/vaultwarden-kubernetes-secrets.yaml.tmp
	@set -eu; \
	for image in agentmail-postfix agentmail-dovecot agentmail-directory; do \
		ids=$$($(DOCKER) ps -aq --filter "ancestor=$$image" || true); \
		if [ -n "$$ids" ]; then $(DOCKER) rm -f $$ids; fi; \
		imgids=$$($(DOCKER) images -q "$$image" || true); \
		if [ -n "$$imgids" ]; then $(DOCKER) rmi -f $$imgids; fi; \
	done
