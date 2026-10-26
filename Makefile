# Build the agentmail images, the directory gRPC server, and the agentmail client.
#   make
#   make server
#   make charts
#   make apply
#   make helm-add
#   make helm-update
#   make helm-install
#   make teardown
#   make push
#   make ensure-tools
#   make clean
#   make TAG=dev
#   make DOCKER=podman
#
# TAG defaults to the Unix time of this make invocation. The four built
# images and the chart tags for those images share that value. Postgres
# stays at the tag in values.yaml.
#
# make charts renders charts/agentmail to dist/agentmail.yaml and the
# vaultwarden-kubernetes-secrets chart to
# dist/vaultwarden-kubernetes-secrets.yaml.
#   DOCKER_REGISTRY=$DOCKER_REGISTRY \
#   VAULTWARDEN_SERVER_URL=$VAULTWARDEN_SERVER_URL \
#   make charts
# make apply installs the external charts first, then renders and applies
# the mail chart. The order is helm-add, helm-update, helm-install
# (istio-base, istiod, then vaultwarden-kubernetes-secrets), then
# dist/agentmail.yaml. Istio's CRDs exist before the mail manifests.
# The sync credential Secret is created before the Vaultwarden release.
# Credentials come from BW_CLIENTID, BW_CLIENTSECRET, and
# VAULTWARDEN__MASTERPASSWORD (or BW_MASTER_PASSWORD). It does not generate
# Vaultwarden items.
# make helm-add adds the Istio Helm repository. The Vaultwarden chart is OCI
# and has no repository to add.
# make helm-update refreshes that repository.
# make helm-install adds, updates, then helm upgrade --install of every
# external chart: istio-base, istiod (sidecar profile), and
# vaultwarden-kubernetes-secrets. The Vaultwarden install takes ownership
# of objects that were applied before the Helm release existed. It does
# not install charts/agentmail.
# make apply depends on it. A second run upgrades those releases in place
# to the pinned versions. Istio stays on the sidecar profile.
# VAULTWARDEN_SERVER_URL is required.
# make teardown reverses apply. It deletes the mail namespace while istiod
# is still installed, uninstalls vaultwarden-kubernetes-secrets and deletes
# that namespace, then uninstalls istiod and istio-base and deletes the
# Istio namespace. Missing releases and namespaces are ignored. It refuses
# default, kube-system, kube-public, and kube-node-lease.
# make push tags the four built images with DOCKER_REGISTRY and pushes them.
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
# Sidecar profile. istio-base and istiod are the external Istio charts.
# Ambient mode is a different data plane and is not installed here.
ISTIO_REPO_NAME := istio
ISTIO_REPO := https://blob.istio.io/istio-release/charts
ISTIO_VERSION := 1.31.3
ISTIO_NAMESPACE ?= istio-system
ISTIO_BASE_RELEASE ?= istio-base
ISTIOD_RELEASE ?= istiod
# One trailing slash is removed so the sync client does not join a double slash.
VAULTWARDEN_SERVER_URL_TRIMMED := $(patsubst %/,%,$(VAULTWARDEN_SERVER_URL))
PROTOC_GEN_GO_VERSION := v1.36.12
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2
HELM_VERSION := v3.19.0

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
export PATH := $(shell $(GO) env GOPATH)/bin:$(PATH)

.PHONY: all postfix dovecot directory submission-auth server generate clean ensure-tools charts apply teardown push push-check \
	helm-ensure helm-add helm-update helm-install helm-install-istio helm-install-vaultwarden vaultwarden-secret

all: postfix dovecot directory submission-auth server

postfix:
	$(DOCKER) build -f $(ROOT)/postfix/Dockerfile -t agentmail-postfix:$(TAG) $(ROOT)

dovecot:
	$(DOCKER) build -f $(ROOT)/dovecot/Dockerfile -t agentmail-dovecot:$(TAG) $(ROOT)

directory: generate
	$(DOCKER) build -f $(ROOT)/directory/Dockerfile -t agentmail-directory:$(TAG) $(ROOT)

submission-auth:
	$(DOCKER) build -f $(ROOT)/submission-auth/Dockerfile -t agentmail-submission-auth:$(TAG) $(ROOT)

server: $(ROOT)/bin/directory $(ROOT)/bin/agentmail

ensure-tools:
	@command -v $(GO) >/dev/null 2>&1 || { echo "go is required to build" >&2; exit 1; }
	@command -v protoc >/dev/null 2>&1 || { echo "protoc is required to generate the directory service" >&2; exit 1; }
	@command -v protoc-gen-go >/dev/null 2>&1 || $(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	@command -v protoc-gen-go-grpc >/dev/null 2>&1 || $(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)

generate: ensure-tools
	cd $(ROOT) && $(GO) generate ./...

charts: helm-ensure
	@test -n "$(VAULTWARDEN_SERVER_URL_TRIMMED)" || { echo "VAULTWARDEN_SERVER_URL is required to render the vaultwarden chart" >&2; exit 1; }
	@printf '%s' '$(VAULTWARDEN_SERVER_URL_TRIMMED)' | grep -Eq '^https://[^[:space:]]+$$' || { echo "VAULTWARDEN_SERVER_URL must be an https URL" >&2; exit 1; }
	mkdir -p $(ROOT)/dist
	$(HELM) lint $(ROOT)/charts/agentmail \
		--set-string images.registry='$(DOCKER_REGISTRY)' \
		--set-string images.postfix.tag='$(TAG)' \
		--set-string images.dovecot.tag='$(TAG)' \
		--set-string images.directory.tag='$(TAG)' \
		--set-string images.submissionAuth.tag='$(TAG)'
	$(HELM) template $(RELEASE) $(ROOT)/charts/agentmail \
		--namespace $(NAMESPACE) \
		--set-string images.registry='$(DOCKER_REGISTRY)' \
		--set-string images.postfix.tag='$(TAG)' \
		--set-string images.dovecot.tag='$(TAG)' \
		--set-string images.directory.tag='$(TAG)' \
		--set-string images.submissionAuth.tag='$(TAG)' \
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

# External releases first, then the mail chart. helm-install is
# helm-update then istio-base, istiod, and vaultwarden-kubernetes-secrets.
apply: helm-install charts
	@command -v $(KUBECTL) >/dev/null 2>&1 || { echo "kubectl is required to apply the chart" >&2; exit 1; }
	$(KUBECTL) create namespace $(NAMESPACE) --dry-run=client -o yaml | $(KUBECTL) apply -f -
	$(KUBECTL) apply -f $(ROOT)/dist/agentmail.yaml

# Reverse of apply: mail namespace, Vaultwarden release, istiod, istio-base.
# The mail namespace goes first so its Istio resources are removed while
# the control plane is still serving webhooks.
teardown: helm-ensure
	@command -v $(KUBECTL) >/dev/null 2>&1 || { echo "kubectl is required to delete the namespace" >&2; exit 1; }
	@mail_ns='$(NAMESPACE)'; \
	sync_ns='$(VWK8S_NAMESPACE)'; \
	istio_ns='$(ISTIO_NAMESPACE)'; \
	release='$(VWK8S_RELEASE)'; \
	base='$(ISTIO_BASE_RELEASE)'; \
	discovery='$(ISTIOD_RELEASE)'; \
	label='^[a-z0-9]([-a-z0-9]*[a-z0-9])?$$'; \
	for value in "$$mail_ns" "$$sync_ns" "$$istio_ns" "$$release" "$$base" "$$discovery"; do \
		printf '%s' "$$value" | grep -Eq "$$label" || { echo "teardown names must be DNS labels, got $$value" >&2; exit 1; }; \
	done; \
	for ns in "$$mail_ns" "$$sync_ns" "$$istio_ns"; do \
		case "$$ns" in \
			default|kube-system|kube-public|kube-node-lease) \
				echo "refusing to delete namespace $$ns" >&2; \
				exit 1; \
				;; \
		esac; \
	done; \
	if [ "$$mail_ns" != "$$sync_ns" ] && [ "$$mail_ns" != "$$istio_ns" ]; then \
		$(KUBECTL) delete namespace "$$mail_ns" --ignore-not-found; \
	fi; \
	$(HELM) uninstall "$$release" --namespace "$$sync_ns" --ignore-not-found --wait; \
	if [ -f $(ROOT)/dist/vaultwarden-kubernetes-secrets.yaml ]; then \
		$(KUBECTL) delete --ignore-not-found -n "$$sync_ns" -f $(ROOT)/dist/vaultwarden-kubernetes-secrets.yaml; \
	fi; \
	$(KUBECTL) delete clusterrole "$$release" --ignore-not-found; \
	$(KUBECTL) delete clusterrolebinding "$$release" --ignore-not-found; \
	if [ "$$sync_ns" != "$$istio_ns" ]; then \
		$(KUBECTL) delete namespace "$$sync_ns" --ignore-not-found; \
	fi; \
	$(HELM) uninstall "$$discovery" --namespace "$$istio_ns" --ignore-not-found --wait; \
	$(HELM) uninstall "$$base" --namespace "$$istio_ns" --ignore-not-found --wait; \
	$(KUBECTL) delete namespace "$$istio_ns" --ignore-not-found

helm-ensure:
	@command -v $(HELM) >/dev/null 2>&1 || $(GO) install helm.sh/helm/v3/cmd/helm@$(HELM_VERSION)

# helm-add registers external Helm repositories. OCI charts have no repo.
helm-add: helm-ensure
	$(HELM) repo add $(ISTIO_REPO_NAME) $(ISTIO_REPO) --force-update

# helm-update refreshes the external repositories added above.
helm-update: helm-add
	$(HELM) repo update $(ISTIO_REPO_NAME)

# helm-install upgrades or installs every external chart. istio-base lands
# before istiod. The Vaultwarden chart is pulled by OCI reference.
# The server URL is checked before either release is touched.
helm-install:
	@test -n "$(VAULTWARDEN_SERVER_URL_TRIMMED)" || { echo "VAULTWARDEN_SERVER_URL is required to install the vaultwarden chart" >&2; exit 1; }
	@printf '%s' '$(VAULTWARDEN_SERVER_URL_TRIMMED)' | grep -Eq '^https://[^[:space:]]+$$' || { echo "VAULTWARDEN_SERVER_URL must be an https URL" >&2; exit 1; }
	$(MAKE) helm-install-istio
	$(MAKE) helm-install-vaultwarden

helm-install-istio: helm-update
	@ns='$(ISTIO_NAMESPACE)'; \
	base='$(ISTIO_BASE_RELEASE)'; \
	discovery='$(ISTIOD_RELEASE)'; \
	label='^[a-z0-9]([-a-z0-9]*[a-z0-9])?$$'; \
	printf '%s' "$$ns" | grep -Eq "$$label" || { echo "ISTIO_NAMESPACE must be a DNS label" >&2; exit 1; }; \
	printf '%s' "$$base" | grep -Eq "$$label" || { echo "ISTIO_BASE_RELEASE must be a DNS label" >&2; exit 1; }; \
	printf '%s' "$$discovery" | grep -Eq "$$label" || { echo "ISTIOD_RELEASE must be a DNS label" >&2; exit 1; }; \
	case "$$ns" in \
		default|kube-system|kube-public|kube-node-lease) \
			echo "refusing to install Istio into namespace $$ns" >&2; \
			exit 1; \
			;; \
	esac
	$(HELM) upgrade --install $(ISTIO_BASE_RELEASE) $(ISTIO_REPO_NAME)/base \
		--namespace $(ISTIO_NAMESPACE) --create-namespace \
		--version $(ISTIO_VERSION) \
		--set defaultRevision=default \
		--wait
	$(HELM) upgrade --install $(ISTIOD_RELEASE) $(ISTIO_REPO_NAME)/istiod \
		--namespace $(ISTIO_NAMESPACE) \
		--version $(ISTIO_VERSION) \
		--wait

helm-install-vaultwarden: helm-update
	@test -n "$(VAULTWARDEN_SERVER_URL_TRIMMED)" || { echo "VAULTWARDEN_SERVER_URL is required to install the vaultwarden chart" >&2; exit 1; }
	@printf '%s' '$(VAULTWARDEN_SERVER_URL_TRIMMED)' | grep -Eq '^https://[^[:space:]]+$$' || { echo "VAULTWARDEN_SERVER_URL must be an https URL" >&2; exit 1; }
	@release='$(VWK8S_RELEASE)'; \
	ns='$(VWK8S_NAMESPACE)'; \
	label='^[a-z0-9]([-a-z0-9]*[a-z0-9])?$$'; \
	printf '%s' "$$release" | grep -Eq "$$label" || { echo "VWK8S_RELEASE must be a DNS label" >&2; exit 1; }; \
	printf '%s' "$$ns" | grep -Eq "$$label" || { echo "VWK8S_NAMESPACE must be a DNS label" >&2; exit 1; }; \
	case "$$ns" in \
		default|kube-system|kube-public|kube-node-lease) \
			echo "refusing to install into namespace $$ns" >&2; \
			exit 1; \
			;; \
	esac
	$(MAKE) vaultwarden-secret
	# --take-ownership adopts the namespace and the objects that
	# kubectl apply created before this release existed.
	$(HELM) upgrade --install $(VWK8S_RELEASE) $(VWK8S_CHART) \
		--version $(VWK8S_VERSION) \
		--namespace $(VWK8S_NAMESPACE) --create-namespace \
		--take-ownership \
		--set namespace.create=true \
		--set-string env.config.VAULTWARDEN__SERVERURL='$(VAULTWARDEN_SERVER_URL_TRIMMED)' \
		$(if $(VAULTWARDEN_ORGANIZATION_ID),--set-string env.config.VAULTWARDEN__ORGANIZATIONID='$(VAULTWARDEN_ORGANIZATION_ID)',) \
		$(if $(VAULTWARDEN_COLLECTION_ID),--set-string env.config.VAULTWARDEN__COLLECTIONID='$(VAULTWARDEN_COLLECTION_ID)',) \
		--wait

vaultwarden-secret:
	@command -v $(KUBECTL) >/dev/null 2>&1 || { echo "kubectl is required to apply the chart" >&2; exit 1; }
	@ns='$(VWK8S_NAMESPACE)'; \
	label='^[a-z0-9]([-a-z0-9]*[a-z0-9])?$$'; \
	printf '%s' "$$ns" | grep -Eq "$$label" || { echo "VWK8S_NAMESPACE must be a DNS label" >&2; exit 1; }; \
	case "$$ns" in \
		default|kube-system|kube-public|kube-node-lease) \
			echo "refusing to write the sync Secret in namespace $$ns" >&2; \
			exit 1; \
			;; \
	esac
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

# push-check is first so a missing registry fails before the image builds.
push: push-check postfix dovecot directory submission-auth
	@set -eu; \
	for name in agentmail-postfix agentmail-dovecot agentmail-directory agentmail-submission-auth; do \
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

$(ROOT)/bin/agentmail: generate
	mkdir -p $(ROOT)/bin
	cd $(ROOT) && CGO_ENABLED=0 $(GO) build -trimpath -o $@ ./cmd/agentmail

clean:
	rm -rf $(ROOT)/bin
	rm -f $(ROOT)/gen/agentmail/directory/v1/*.pb.go
	rm -f $(ROOT)/dist/agentmail.yaml $(ROOT)/dist/agentmail.yaml.tmp \
		$(ROOT)/dist/vaultwarden-kubernetes-secrets.yaml $(ROOT)/dist/vaultwarden-kubernetes-secrets.yaml.tmp
	@set -eu; \
	for image in agentmail-postfix agentmail-dovecot agentmail-directory agentmail-submission-auth; do \
		ids=$$($(DOCKER) ps -aq --filter "ancestor=$$image" || true); \
		if [ -n "$$ids" ]; then $(DOCKER) rm -f $$ids; fi; \
		imgids=$$($(DOCKER) images -q "$$image" || true); \
		if [ -n "$$imgids" ]; then $(DOCKER) rmi -f $$imgids; fi; \
	done
