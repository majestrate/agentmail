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
# TAG defaults to the Unix time of this make invocation. The built images
# and the chart tags for those images share that value. Postgres stays at
# the tag in values.yaml.
#
# make charts renders charts/agentmail to dist/agentmail.yaml. Pods read one
# Kubernetes Secret. SECRETS_PROVIDER chooses who writes it.
#   external or empty: ExternalSecret references a SecretStore that already
#   exists. Any External Secrets provider is used that way.
#   aws or hashicorp: the chart also creates that SecretStore.
#   vaultwarden: vaultwarden-kubernetes-secrets writes the Secret. The mail
#   chart does not render ExternalSecret. make charts also renders that sync
#   chart to dist/vaultwarden-kubernetes-secrets.yaml.
#   DOCKER_REGISTRY=$DOCKER_REGISTRY \
#   MAIL_DOMAIN=$MAIL_DOMAIN \
#   SECRETS_PROVIDER=aws \
#   AWS_REGION=us-east-1 \
#   make charts
#   SECRETS_PROVIDER=vaultwarden \
#   VAULTWARDEN_SERVER_URL=$VAULTWARDEN_SERVER_URL \
#   MAIL_DOMAIN=$MAIL_DOMAIN \
#   make charts
# AWS_REGION is required for aws. VAULT_ADDR (https) and VAULT_ROLE are
# required for hashicorp Kubernetes auth. VAULTWARDEN_SERVER_URL (https) is
# required for vaultwarden. The chart does not store access keys, tokens,
# the Vaultwarden URL, or the database password.
# make apply installs the external charts first, then renders and applies
# the mail chart. The order is helm-add, helm-update, helm-install
# (istio-base, istiod, then external-secrets or vaultwarden-kubernetes-secrets,
# then cert-manager), then dist/agentmail.yaml, then helm-install-istio-csr.
# Only the selected secret backend is installed. Istio's CRDs exist before
# the mail manifests. External Secrets CRDs exist before them when that
# backend is selected. cert-manager's CRDs exist before the mail chart's
# Issuer and Certificate resources. helm-install-istio-csr runs after the
# root CA Secret exists. It copies that Secret into the cert-manager
# namespace, installs istio-csr, and points istiod at it. The copy is not
# rendered into dist/agentmail.yaml.
# make helm-add adds the Istio, External Secrets, and cert-manager Helm
# repositories. The Vaultwarden chart is OCI and has no repository to add.
# make helm-update refreshes the Istio, External Secrets, and cert-manager
# repositories.
# make helm-install adds, updates, then helm upgrade --install of istio-base,
# istiod (sidecar profile), the selected secret backend, and cert-manager.
# The Vaultwarden install takes ownership of objects that were applied before
# the Helm release existed. It writes the sync credential Secret from
# BW_CLIENTID, BW_CLIENTSECRET, and VAULTWARDEN__MASTERPASSWORD (or
# BW_MASTER_PASSWORD). It does not generate Vaultwarden items and does not
# install charts/agentmail. make apply depends on it. A second run upgrades
# those releases in place to the pinned versions. Istio stays on the sidecar
# profile. cert-manager issues the private root CA from the mail chart.
# After that chart is applied, helm-install-istio-csr signs each Istio
# sidecar certificate with that root. A later helm-install keeps those
# istiod settings when the istio-csr release is already installed.
# MAIL_DOMAIN is required. It sets mail.domain. mail.hostname stays the
# chart value.
# make teardown reverses apply. It deletes the mail namespace while istiod,
# istio-csr, cert-manager, and the secret backend are still installed,
# uninstalls istio-csr and deletes the ClusterIssuer, uninstalls
# cert-manager, external-secrets, and vaultwarden-kubernetes-secrets when
# those releases are present and deletes their namespaces, then uninstalls
# istiod and istio-base and deletes the Istio namespace. Missing releases
# and namespaces are ignored. It refuses default, kube-system, kube-public,
# and kube-node-lease.
# make push tags the built images with DOCKER_REGISTRY and pushes them.
# Postgres is not pushed.
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
# Virtual mailbox domain. make charts requires a lowercase DNS name and
# sets mail.domain from it. mail.hostname is unchanged.
MAIL_DOMAIN ?=
RELEASE ?= agentmail
NAMESPACE ?= agentmail
# vaultwarden-kubernetes-secrets. Installed when SECRETS_PROVIDER=vaultwarden.
# teardown removes the release when it is still installed.
VWK8S_CHART := oci://ghcr.io/antoniolago/charts/vaultwarden-kubernetes-secrets
VWK8S_VERSION := 2.1.0
VWK8S_RELEASE ?= vaultwarden-kubernetes-secrets
VWK8S_NAMESPACE ?= vaultwarden-kubernetes-secrets
# One trailing slash is removed so the sync client does not join a double slash.
VAULTWARDEN_SERVER_URL ?=
VAULTWARDEN_SERVER_URL_TRIMMED := $(patsubst %/,%,$(VAULTWARDEN_SERVER_URL))
VAULTWARDEN_ORGANIZATION_ID ?=
VAULTWARDEN_COLLECTION_ID ?=
ESO_REPO_NAME := external-secrets
ESO_REPO := https://charts.external-secrets.io
ESO_VERSION := 2.14.0
ESO_RELEASE ?= external-secrets
ESO_NAMESPACE ?= external-secrets
# Who writes the Kubernetes Secret the pods read.
# empty or external: ExternalSecret, SecretStore already exists.
# aws or hashicorp: this chart creates the SecretStore.
# vaultwarden: vaultwarden-kubernetes-secrets writes the Secret.
SECRETS_PROVIDER ?=
SECRETS_REMOTE_KEY ?=
SECRETS_REGISTRY_REMOTE_KEY ?=
AWS_REGION ?=
AWS_ROLE ?=
AWS_SERVICE ?=
VAULT_ADDR ?=
VAULT_ADDR_TRIMMED := $(patsubst %/,%,$(VAULT_ADDR))
VAULT_ROLE ?=
VAULT_PATH ?=
VAULT_KV_VERSION ?=
VAULT_AUTH ?=
VAULT_NAMESPACE ?=
VAULT_KUBERNETES_MOUNT ?=
VAULT_SERVICE_ACCOUNT ?=
VAULT_TOKEN_SECRET ?=
VAULT_TOKEN_SECRET_KEY ?=
# External Secrets fields. Omitted for vaultwarden so a leftover AWS or
# Vault variable is not sent as externalSecrets.secretStore.provider.
HELM_SECRET_FIELDS := $(if $(SECRETS_REMOTE_KEY),--set-string externalSecrets.remoteKey='$(SECRETS_REMOTE_KEY)') $(if $(SECRETS_REGISTRY_REMOTE_KEY),--set-string externalSecrets.registryRemoteKey='$(SECRETS_REGISTRY_REMOTE_KEY)') $(if $(AWS_REGION),--set-string externalSecrets.aws.region='$(AWS_REGION)') $(if $(AWS_ROLE),--set-string externalSecrets.aws.role='$(AWS_ROLE)') $(if $(AWS_SERVICE),--set-string externalSecrets.aws.service='$(AWS_SERVICE)') $(if $(VAULT_ADDR_TRIMMED),--set-string externalSecrets.hashicorp.server='$(VAULT_ADDR_TRIMMED)') $(if $(VAULT_ROLE),--set-string externalSecrets.hashicorp.kubernetes.role='$(VAULT_ROLE)') $(if $(VAULT_PATH),--set-string externalSecrets.hashicorp.path='$(VAULT_PATH)') $(if $(VAULT_KV_VERSION),--set-string externalSecrets.hashicorp.version='$(VAULT_KV_VERSION)') $(if $(VAULT_AUTH),--set-string externalSecrets.hashicorp.auth='$(VAULT_AUTH)') $(if $(VAULT_NAMESPACE),--set-string externalSecrets.hashicorp.namespace='$(VAULT_NAMESPACE)') $(if $(VAULT_KUBERNETES_MOUNT),--set-string externalSecrets.hashicorp.kubernetes.mountPath='$(VAULT_KUBERNETES_MOUNT)') $(if $(VAULT_SERVICE_ACCOUNT),--set-string externalSecrets.hashicorp.kubernetes.serviceAccount='$(VAULT_SERVICE_ACCOUNT)') $(if $(VAULT_TOKEN_SECRET),--set-string externalSecrets.hashicorp.tokenSecretRef.name='$(VAULT_TOKEN_SECRET)') $(if $(VAULT_TOKEN_SECRET_KEY),--set-string externalSecrets.hashicorp.tokenSecretRef.key='$(VAULT_TOKEN_SECRET_KEY)')
ifeq ($(SECRETS_PROVIDER),vaultwarden)
HELM_SECRET_SET := --set-string secrets.provider=vaultwarden
else ifeq ($(SECRETS_PROVIDER),aws)
HELM_SECRET_SET := --set-string secrets.provider=external --set externalSecrets.secretStore.create=true --set-string externalSecrets.secretStore.provider='aws' $(HELM_SECRET_FIELDS)
else ifeq ($(SECRETS_PROVIDER),hashicorp)
HELM_SECRET_SET := --set-string secrets.provider=external --set externalSecrets.secretStore.create=true --set-string externalSecrets.secretStore.provider='hashicorp' $(HELM_SECRET_FIELDS)
else
HELM_SECRET_SET := $(if $(filter external,$(SECRETS_PROVIDER)),--set-string secrets.provider=external,) $(HELM_SECRET_FIELDS)
endif
define assert-secrets-provider
case '$(SECRETS_PROVIDER)' in \
	''|external|aws|hashicorp|vaultwarden) ;; \
	*) echo "SECRETS_PROVIDER must be external, aws, hashicorp, or vaultwarden" >&2; exit 1 ;; \
esac
endef
# Sidecar profile. istio-base and istiod are the external Istio charts.
# Ambient mode is a different data plane and is not installed here.
ISTIO_REPO_NAME := istio
ISTIO_REPO := https://blob.istio.io/istio-release/charts
ISTIO_VERSION := 1.31.3
ISTIO_NAMESPACE ?= istio-system
ISTIO_BASE_RELEASE ?= istio-base
ISTIOD_RELEASE ?= istiod
# Private root CA. The mail chart's Issuer and Certificate resources need
# these CRDs. crds.keep leaves the CRDs in place on uninstall.
CERT_MANAGER_REPO_NAME := jetstack
CERT_MANAGER_REPO := https://charts.jetstack.io
CERT_MANAGER_VERSION := v1.21.3
CERT_MANAGER_RELEASE ?= cert-manager
CERT_MANAGER_NAMESPACE ?= cert-manager
# Signs each sidecar CSR with the agentmail root. The release name is also
# the Service name. The chart is in the cert-manager repository.
ISTIO_CSR_VERSION := v0.19.1
ISTIO_CSR_RELEASE ?= cert-manager-istio-csr
PROTOC_GEN_GO_VERSION := v1.36.12
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2
HELM_VERSION := v3.19.0

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
export PATH := $(shell $(GO) env GOPATH)/bin:$(PATH)

.PHONY: all postfix dovecot directory submission-auth milter relay maildir server generate clean ensure-tools charts apply teardown push push-check \
	helm-ensure helm-add helm-update helm-install helm-install-istio helm-install-external-secrets helm-install-vaultwarden helm-install-cert-manager helm-install-istio-csr vaultwarden-secret

all: postfix dovecot directory submission-auth milter relay maildir server

postfix:
	$(DOCKER) build -f $(ROOT)/postfix/Dockerfile -t agentmail-postfix:$(TAG) $(ROOT)

dovecot:
	$(DOCKER) build -f $(ROOT)/dovecot/Dockerfile -t agentmail-dovecot:$(TAG) $(ROOT)

directory: generate
	$(DOCKER) build -f $(ROOT)/directory/Dockerfile -t agentmail-directory:$(TAG) $(ROOT)

submission-auth:
	$(DOCKER) build -f $(ROOT)/submission-auth/Dockerfile -t agentmail-submission-auth:$(TAG) $(ROOT)

milter:
	$(DOCKER) build -f $(ROOT)/milter/Dockerfile -t agentmail-milter:$(TAG) $(ROOT)

relay:
	$(DOCKER) build -f $(ROOT)/relay/Dockerfile -t agentmail-relay:$(TAG) $(ROOT)

maildir: generate
	$(DOCKER) build -f $(ROOT)/maildir/Dockerfile -t agentmail-maildir:$(TAG) $(ROOT)

server: $(ROOT)/bin/directory $(ROOT)/bin/agentmail

compile: maildir relay milter submission-auth directory postfix dovecot
	@echo compiled

test: generate
	$(GO) test ./...

ensure-tools:
	@command -v $(GO) >/dev/null 2>&1 || { echo "go is required to build" >&2; exit 1; }
	@command -v protoc >/dev/null 2>&1 || { echo "protoc is required to generate the directory service" >&2; exit 1; }
	@command -v protoc-gen-go >/dev/null 2>&1 || $(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	@command -v protoc-gen-go-grpc >/dev/null 2>&1 || $(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)

generate: ensure-tools
	cd $(ROOT) && $(GO) generate ./...

charts: helm-ensure
	@$(assert-secrets-provider)
	@if [ '$(SECRETS_PROVIDER)' = vaultwarden ]; then \
		test -n "$(VAULTWARDEN_SERVER_URL_TRIMMED)" || { echo "VAULTWARDEN_SERVER_URL is required to render the vaultwarden chart" >&2; exit 1; }; \
		printf '%s' '$(VAULTWARDEN_SERVER_URL_TRIMMED)' | grep -Eq '^https://[^[:space:]]+$$' || { echo "VAULTWARDEN_SERVER_URL must be an https URL" >&2; exit 1; }; \
	fi
	@test -n "$(MAIL_DOMAIN)" || { echo "MAIL_DOMAIN is required to render the mail chart" >&2; exit 1; }
	@printf '%s' '$(MAIL_DOMAIN)' | grep -Eq '^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$$' || { echo "MAIL_DOMAIN must be a lowercase DNS name" >&2; exit 1; }
	mkdir -p $(ROOT)/dist
	$(HELM) lint $(ROOT)/charts/agentmail \
		--set-string mail.domain='$(MAIL_DOMAIN)' \
		--set-string images.registry='$(DOCKER_REGISTRY)' \
		--set-string images.postfix.tag='$(TAG)' \
		--set-string images.dovecot.tag='$(TAG)' \
		--set-string images.directory.tag='$(TAG)' \
		--set-string images.submissionAuth.tag='$(TAG)' \
		--set-string images.milter.tag='$(TAG)' \
		--set-string images.relay.tag='$(TAG)' \
		--set-string images.maildir.tag='$(TAG)' \
		$(HELM_SECRET_SET)
	$(HELM) template $(RELEASE) $(ROOT)/charts/agentmail \
		--namespace $(NAMESPACE) \
		--set-string mail.domain='$(MAIL_DOMAIN)' \
		--set-string images.registry='$(DOCKER_REGISTRY)' \
		--set-string images.postfix.tag='$(TAG)' \
		--set-string images.dovecot.tag='$(TAG)' \
		--set-string images.directory.tag='$(TAG)' \
		--set-string images.submissionAuth.tag='$(TAG)' \
		--set-string images.milter.tag='$(TAG)' \
		--set-string images.relay.tag='$(TAG)' \
		--set-string images.maildir.tag='$(TAG)' \
		$(HELM_SECRET_SET) \
		> $(ROOT)/dist/agentmail.yaml.tmp
	mv $(ROOT)/dist/agentmail.yaml.tmp $(ROOT)/dist/agentmail.yaml
	@if [ '$(SECRETS_PROVIDER)' = vaultwarden ]; then \
		set -eu; \
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
		mv $(ROOT)/dist/vaultwarden-kubernetes-secrets.yaml.tmp $(ROOT)/dist/vaultwarden-kubernetes-secrets.yaml; \
	fi

# External releases first, then the mail chart, then istio-csr. The root
# CA Secret has to exist before its key is copied for the ClusterIssuer.
apply: helm-install charts
	@command -v $(KUBECTL) >/dev/null 2>&1 || { echo "kubectl is required to apply the chart" >&2; exit 1; }
	$(KUBECTL) create namespace $(NAMESPACE) --dry-run=client -o yaml | $(KUBECTL) apply -f -
	$(KUBECTL) apply -f $(ROOT)/dist/agentmail.yaml
	$(MAKE) helm-install-istio-csr

# Reverse of apply: mail namespace, cert-manager, External Secrets,
# Vaultwarden sync, istiod, istio-base. The mail namespace goes first so
# its Istio and cert-manager resources are removed while those controllers
# are still serving webhooks, and ExternalSecret finalizers run while the
# operator is still up.
teardown: helm-ensure
	@command -v $(KUBECTL) >/dev/null 2>&1 || { echo "kubectl is required to delete the namespace" >&2; exit 1; }
	@mail_ns='$(NAMESPACE)'; \
	eso_ns='$(ESO_NAMESPACE)'; \
	eso_release='$(ESO_RELEASE)'; \
	sync_ns='$(VWK8S_NAMESPACE)'; \
	sync_release='$(VWK8S_RELEASE)'; \
	cm_ns='$(CERT_MANAGER_NAMESPACE)'; \
	cm_release='$(CERT_MANAGER_RELEASE)'; \
	istio_ns='$(ISTIO_NAMESPACE)'; \
	base='$(ISTIO_BASE_RELEASE)'; \
	discovery='$(ISTIOD_RELEASE)'; \
	label='^[a-z0-9]([-a-z0-9]*[a-z0-9])?$$'; \
	for value in "$$mail_ns" "$$eso_ns" "$$eso_release" "$$sync_ns" "$$sync_release" "$$cm_ns" "$$cm_release" "$$istio_ns" "$$base" "$$discovery"; do \
		printf '%s' "$$value" | grep -Eq "$$label" || { echo "teardown names must be DNS labels, got $$value" >&2; exit 1; }; \
	done; \
	for ns in "$$mail_ns" "$$eso_ns" "$$sync_ns" "$$cm_ns" "$$istio_ns"; do \
		case "$$ns" in \
			default|kube-system|kube-public|kube-node-lease) \
				echo "refusing to delete namespace $$ns" >&2; \
				exit 1; \
				;; \
		esac; \
	done; \
	if [ "$$mail_ns" != "$$eso_ns" ] && [ "$$mail_ns" != "$$sync_ns" ] && [ "$$mail_ns" != "$$cm_ns" ] && [ "$$mail_ns" != "$$istio_ns" ]; then \
		$(KUBECTL) delete namespace "$$mail_ns" --ignore-not-found; \
	fi; \
	$(HELM) uninstall '$(ISTIO_CSR_RELEASE)' --namespace "$$cm_ns" --ignore-not-found --wait; \
	$(KUBECTL) delete clusterissuer '$(RELEASE)-ca' --ignore-not-found; \
	$(KUBECTL) -n "$$cm_ns" delete secret '$(RELEASE)-root-ca' --ignore-not-found; \
	$(KUBECTL) -n "$$cm_ns" delete configmap '$(RELEASE)-root-ca' --ignore-not-found; \
	$(HELM) uninstall "$$cm_release" --namespace "$$cm_ns" --ignore-not-found --wait; \
	if [ "$$cm_ns" != "$$eso_ns" ] && [ "$$cm_ns" != "$$sync_ns" ] && [ "$$cm_ns" != "$$istio_ns" ]; then \
		$(KUBECTL) delete namespace "$$cm_ns" --ignore-not-found; \
	fi; \
	$(HELM) uninstall "$$eso_release" --namespace "$$eso_ns" --ignore-not-found --wait; \
	if [ "$$eso_ns" != "$$istio_ns" ] && [ "$$eso_ns" != "$$sync_ns" ]; then \
		$(KUBECTL) delete namespace "$$eso_ns" --ignore-not-found; \
	fi; \
	$(HELM) uninstall "$$sync_release" --namespace "$$sync_ns" --ignore-not-found --wait; \
	if [ -f $(ROOT)/dist/vaultwarden-kubernetes-secrets.yaml ]; then \
		$(KUBECTL) delete --ignore-not-found -n "$$sync_ns" -f $(ROOT)/dist/vaultwarden-kubernetes-secrets.yaml; \
	fi; \
	$(KUBECTL) delete clusterrole "$$sync_release" --ignore-not-found; \
	$(KUBECTL) delete clusterrolebinding "$$sync_release" --ignore-not-found; \
	if [ "$$sync_ns" != "$$istio_ns" ]; then \
		$(KUBECTL) delete namespace "$$sync_ns" --ignore-not-found; \
	fi; \
	$(HELM) uninstall "$$discovery" --namespace "$$istio_ns" --ignore-not-found --wait; \
	$(HELM) uninstall "$$base" --namespace "$$istio_ns" --ignore-not-found --wait; \
	$(KUBECTL) delete namespace "$$istio_ns" --ignore-not-found

helm-ensure:
	@command -v $(HELM) >/dev/null 2>&1 || $(GO) install helm.sh/helm/v3/cmd/helm@$(HELM_VERSION)

# helm-add registers external Helm repositories.
helm-add: helm-ensure
	$(HELM) repo add $(ISTIO_REPO_NAME) $(ISTIO_REPO) --force-update
	$(HELM) repo add $(ESO_REPO_NAME) $(ESO_REPO) --force-update
	$(HELM) repo add $(CERT_MANAGER_REPO_NAME) $(CERT_MANAGER_REPO) --force-update

# helm-update refreshes the external repositories added above.
helm-update: helm-add
	$(HELM) repo update $(ISTIO_REPO_NAME) $(ESO_REPO_NAME) $(CERT_MANAGER_REPO_NAME)

# helm-install upgrades or installs istio-base, istiod, the secret
# backend selected by SECRETS_PROVIDER, and cert-manager. The mail chart
# is not installed here.
helm-install:
	@$(assert-secrets-provider)
	$(MAKE) helm-install-istio
	@if [ '$(SECRETS_PROVIDER)' = vaultwarden ]; then $(MAKE) helm-install-vaultwarden; else $(MAKE) helm-install-external-secrets; fi
	$(MAKE) helm-install-cert-manager

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
	@if $(HELM) status '$(ISTIO_CSR_RELEASE)' --namespace '$(CERT_MANAGER_NAMESPACE)' >/dev/null 2>&1; then \
		tmp=$$(mktemp -d); \
		chmod 700 "$$tmp"; \
		trap 'rm -rf "$$tmp"' EXIT; \
		printf '%s\n' \
			'global:' \
			'  caAddress: $(ISTIO_CSR_RELEASE).$(CERT_MANAGER_NAMESPACE).svc:443' \
			'env:' \
			'  ENABLE_CA_SERVER: "false"' \
			'extraContainerArgs:' \
			'  - --tlsCertFile=/var/run/secrets/istiod/tls/tls.crt' \
			'  - --tlsKeyFile=/var/run/secrets/istiod/tls/tls.key' \
			'  - --caCertFile=/var/run/secrets/istiod/tls/ca.crt' \
			> "$$tmp/istiod.yaml"; \
		$(HELM) upgrade --install $(ISTIOD_RELEASE) $(ISTIO_REPO_NAME)/istiod \
			--namespace $(ISTIO_NAMESPACE) \
			--version $(ISTIO_VERSION) \
			--values "$$tmp/istiod.yaml" \
			--wait; \
	else \
		$(HELM) upgrade --install $(ISTIOD_RELEASE) $(ISTIO_REPO_NAME)/istiod \
			--namespace $(ISTIO_NAMESPACE) \
			--version $(ISTIO_VERSION) \
			--wait; \
	fi

helm-install-external-secrets: helm-update
	@release='$(ESO_RELEASE)'; \
	ns='$(ESO_NAMESPACE)'; \
	label='^[a-z0-9]([-a-z0-9]*[a-z0-9])?$$'; \
	printf '%s' "$$release" | grep -Eq "$$label" || { echo "ESO_RELEASE must be a DNS label" >&2; exit 1; }; \
	printf '%s' "$$ns" | grep -Eq "$$label" || { echo "ESO_NAMESPACE must be a DNS label" >&2; exit 1; }; \
	case "$$ns" in \
		default|kube-system|kube-public|kube-node-lease) \
			echo "refusing to install External Secrets into namespace $$ns" >&2; \
			exit 1; \
			;; \
	esac
	$(HELM) upgrade --install $(ESO_RELEASE) $(ESO_REPO_NAME)/external-secrets \
		--namespace $(ESO_NAMESPACE) --create-namespace \
		--version $(ESO_VERSION) \
		--set installCRDs=true \
		--wait

helm-install-cert-manager: helm-update
	@release='$(CERT_MANAGER_RELEASE)'; \
	ns='$(CERT_MANAGER_NAMESPACE)'; \
	label='^[a-z0-9]([-a-z0-9]*[a-z0-9])?$$'; \
	printf '%s' "$$release" | grep -Eq "$$label" || { echo "CERT_MANAGER_RELEASE must be a DNS label" >&2; exit 1; }; \
	printf '%s' "$$ns" | grep -Eq "$$label" || { echo "CERT_MANAGER_NAMESPACE must be a DNS label" >&2; exit 1; }; \
	case "$$ns" in \
		default|kube-system|kube-public|kube-node-lease) \
			echo "refusing to install cert-manager into namespace $$ns" >&2; \
			exit 1; \
			;; \
	esac
	$(HELM) upgrade --install $(CERT_MANAGER_RELEASE) $(CERT_MANAGER_REPO_NAME)/cert-manager \
		--namespace $(CERT_MANAGER_NAMESPACE) --create-namespace \
		--version $(CERT_MANAGER_VERSION) \
		--set crds.enabled=true \
		--set crds.keep=true \
		--wait

# Copy the mail chart's root CA into cert-manager's namespace, then sign
# sidecar CSRs with it. The root Secret must already exist. The private key
# is written only to a 0700 temp file and to the cert-manager Secret.
helm-install-istio-csr: helm-update
	@set -eu; \
	command -v $(KUBECTL) >/dev/null 2>&1 || { echo "kubectl is required to install istio-csr" >&2; exit 1; }
	@command -v python3 >/dev/null 2>&1 || { echo "python3 is required to copy the root CA" >&2; exit 1; }
	@mail_ns='$(NAMESPACE)'; \
	cm_ns='$(CERT_MANAGER_NAMESPACE)'; \
	istio_ns='$(ISTIO_NAMESPACE)'; \
	root='$(RELEASE)-root-ca'; \
	issuer='$(RELEASE)-ca'; \
	csr='$(ISTIO_CSR_RELEASE)'; \
	label='^[a-z0-9]([-a-z0-9]*[a-z0-9])?$$'; \
	for value in "$$mail_ns" "$$cm_ns" "$$istio_ns" "$$root" "$$issuer" "$$csr"; do \
		printf '%s' "$$value" | grep -Eq "$$label" || { echo "istio-csr names must be DNS labels, got $$value" >&2; exit 1; }; \
	done; \
	case "$$cm_ns" in \
		default|kube-system|kube-public|kube-node-lease) \
			echo "refusing to install istio-csr into namespace $$cm_ns" >&2; \
			exit 1; \
			;; \
	esac; \
	$(KUBECTL) -n "$$mail_ns" get secret "$$root" >/dev/null || { echo "Secret $$root in $$mail_ns is required" >&2; exit 1; }; \
	cr_ns=$$($(KUBECTL) -n "$$cm_ns" get deploy cert-manager -o jsonpath='{range .spec.template.spec.containers[0].args[*]}{@}{"\n"}{end}' | sed -n 's/^--cluster-resource-namespace=//p'); \
	case "$$cr_ns" in \
		''|'$$(POD_NAMESPACE)') cr_ns=$$cm_ns ;; \
	esac; \
	printf '%s' "$$cr_ns" | grep -Eq "$$label" || { echo "cert-manager cluster resource namespace must be a DNS label" >&2; exit 1; }; \
	tmp=$$(mktemp -d); \
	chmod 700 "$$tmp"; \
	trap 'rm -rf "$$tmp"' EXIT; \
	$(KUBECTL) -n "$$mail_ns" get secret "$$root" -o json > "$$tmp/in.json"; \
	python3 $(ROOT)/scripts/sync-agentmail-root-ca.py "$$tmp/in.json" "$$tmp/secret.json" "$$tmp/cm.json" "$$cr_ns" "$$cm_ns" "$$root"; \
	$(KUBECTL) apply -f "$$tmp/secret.json"; \
	$(KUBECTL) apply -f "$$tmp/cm.json"; \
	printf '%s\n' \
		'apiVersion: cert-manager.io/v1' \
		'kind: ClusterIssuer' \
		'metadata:' \
		"  name: $$issuer" \
		'spec:' \
		'  ca:' \
		"    secretName: $$root" \
		> "$$tmp/issuer.yaml"; \
	$(KUBECTL) apply -f "$$tmp/issuer.yaml"; \
	$(KUBECTL) wait --for=condition=Ready "clusterissuer/$$issuer" --timeout=180s; \
	printf '%s\n' \
		"nameOverride: $$csr" \
		'app:' \
		'  certmanager:' \
		"    namespace: $$istio_ns" \
		'    issuer:' \
		'      enabled: true' \
		"      name: $$issuer" \
		'      kind: ClusterIssuer' \
		'      group: cert-manager.io' \
		'  tls:' \
		'    trustDomain: cluster.local' \
		'    rootCAFile: /var/run/secrets/agentmail/ca.crt' \
		'    certificateDNSNames:' \
		"      - $$csr.$$cm_ns.svc" \
		"      - $$csr.$$cm_ns.svc.cluster.local" \
		'  istio:' \
		"    namespace: $$istio_ns" \
		'    revisions:' \
		'      - default' \
		'  server:' \
		'    clusterID: Kubernetes' \
		'volumeMounts:' \
		'  - name: agentmail-root' \
		'    mountPath: /var/run/secrets/agentmail' \
		'    readOnly: true' \
		'volumes:' \
		'  - name: agentmail-root' \
		'    configMap:' \
		"      name: $$root" \
		> "$$tmp/istio-csr.yaml"; \
	printf '%s\n' \
		'global:' \
		"  caAddress: $$csr.$$cm_ns.svc:443" \
		'env:' \
		'  ENABLE_CA_SERVER: "false"' \
		'extraContainerArgs:' \
		'  - --tlsCertFile=/var/run/secrets/istiod/tls/tls.crt' \
		'  - --tlsKeyFile=/var/run/secrets/istiod/tls/tls.key' \
		'  - --caCertFile=/var/run/secrets/istiod/tls/ca.crt' \
		> "$$tmp/istiod.yaml"; \
	$(HELM) upgrade --install "$$csr" $(CERT_MANAGER_REPO_NAME)/cert-manager-istio-csr \
		--namespace "$$cm_ns" \
		--version $(ISTIO_CSR_VERSION) \
		--values "$$tmp/istio-csr.yaml" \
		--wait --timeout 5m; \
	$(KUBECTL) -n "$$istio_ns" wait --for=condition=Ready certificate/istiod --timeout=180s; \
	$(KUBECTL) -n "$$istio_ns" get secret istiod-tls >/dev/null; \
	before=$$($(KUBECTL) -n "$$istio_ns" get deploy $(ISTIOD_RELEASE) -o jsonpath='{range .spec.template.spec.containers[0].env[*]}{.name}={.value}{"\n"}{end}' | awk -F= '$$1=="ENABLE_CA_SERVER"{print $$2}'); \
	if ! $(HELM) upgrade --install $(ISTIOD_RELEASE) $(ISTIO_REPO_NAME)/istiod \
		--namespace "$$istio_ns" \
		--version $(ISTIO_VERSION) \
		--values "$$tmp/istiod.yaml" \
		--wait --timeout 5m; then \
		echo "istiod external CA upgrade failed; rolling back" >&2; \
		$(HELM) rollback $(ISTIOD_RELEASE) --namespace "$$istio_ns" --wait --timeout 5m; \
		exit 1; \
	fi; \
	if [ "$$before" != false ]; then \
		for dep in directory dovecot postfix submission-auth milter relay; do \
			if $(KUBECTL) -n "$$mail_ns" get "deploy/$(RELEASE)-$$dep" >/dev/null 2>&1; then \
				$(KUBECTL) -n "$$mail_ns" rollout restart "deploy/$(RELEASE)-$$dep"; \
			fi; \
		done; \
		for dep in directory dovecot postfix submission-auth milter relay; do \
			if $(KUBECTL) -n "$$mail_ns" get "deploy/$(RELEASE)-$$dep" >/dev/null 2>&1; then \
				$(KUBECTL) -n "$$mail_ns" rollout status "deploy/$(RELEASE)-$$dep" --timeout=180s; \
			fi; \
		done; \
	fi

helm-install-vaultwarden: helm-ensure
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
push: push-check postfix dovecot directory submission-auth milter relay maildir
	@set -eu; \
	for name in agentmail-postfix agentmail-dovecot agentmail-directory agentmail-submission-auth agentmail-milter agentmail-relay agentmail-maildir; do \
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
	rm -f $(ROOT)/gen/agentmail/directory/v1/*.pb.go $(ROOT)/gen/agentmail/maildir/v1/*.pb.go
	rm -f $(ROOT)/dist/agentmail.yaml $(ROOT)/dist/agentmail.yaml.tmp \
		$(ROOT)/dist/vaultwarden-kubernetes-secrets.yaml $(ROOT)/dist/vaultwarden-kubernetes-secrets.yaml.tmp
	@set -eu; \
	for image in agentmail-postfix agentmail-dovecot agentmail-directory agentmail-submission-auth agentmail-milter agentmail-relay agentmail-maildir; do \
		ids=$$($(DOCKER) ps -aq --filter "ancestor=$$image" || true); \
		if [ -n "$$ids" ]; then $(DOCKER) rm -f $$ids; fi; \
		imgids=$$($(DOCKER) images -q "$$image" || true); \
		if [ -n "$$imgids" ]; then $(DOCKER) rmi -f $$imgids; fi; \
	done


it:
	$(MAKE) push apply
	@echo beep
