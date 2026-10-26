{{/*
Chart name, overridable.
*/}}
{{- define "agentmail.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Release prefix for resource names. A release named agentmail stays agentmail.
*/}}
{{- define "agentmail.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "agentmail.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "agentmail.labels" -}}
helm.sh/chart: {{ include "agentmail.chart" . }}
app.kubernetes.io/part-of: agentmail
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "agentmail.componentLabels" -}}
{{ include "agentmail.labels" .context }}
app.kubernetes.io/name: {{ .name }}
{{- end }}

{{- define "agentmail.selectorLabels" -}}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/instance: {{ .context.Release.Name }}
{{- end }}

{{- define "agentmail.configMapName" -}}
{{- printf "%s-mail" (include "agentmail.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Secret written by vaultwarden-kubernetes-secrets. An explicit name is used
as given so it stays equal to the item's secret-name field.
*/}}
{{- define "agentmail.secretName" -}}
{{- $name := .Values.vaultwarden.secretName -}}
{{- if $name -}}
{{- $name -}}
{{- else -}}
{{- printf "%s-mail-db" (include "agentmail.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end }}

{{/*
dockerconfigjson Secret written by vaultwarden-kubernetes-secrets. Pods
reference it when images.registry is set. An explicit name stays equal to
the item's secret-name field.
*/}}
{{- define "agentmail.registrySecretName" -}}
{{- $name := .Values.vaultwarden.registrySecretName -}}
{{- if $name -}}
{{- $name -}}
{{- else -}}
{{- printf "%s-registry" (include "agentmail.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end }}

{{- define "agentmail.imagePullSecrets" -}}
{{- if .Values.images.registry }}
imagePullSecrets:
  - name: {{ include "agentmail.registrySecretName" . }}
{{- end }}
{{- end }}

{{- define "agentmail.postgresName" -}}
{{- printf "%s-postgres" (include "agentmail.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "agentmail.dovecotName" -}}
{{- printf "%s-dovecot" (include "agentmail.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "agentmail.postfixName" -}}
{{- printf "%s-postfix" (include "agentmail.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "agentmail.directoryName" -}}
{{- printf "%s-directory" (include "agentmail.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "agentmail.submissionAuthName" -}}
{{- printf "%s-submission-auth" (include "agentmail.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Pod annotations. Directory joins the mesh for gRPC. Postfix and Dovecot
join it for submission and LMTP. The submission auth daemon joins it so
Postfix can reach SASL. Postgres stays out, and Postgres dials bypass the proxy.
*/}}
{{- define "agentmail.meshAnnotations" -}}
{{- if and .context.Values.istio.enabled (or (eq .component "directory") (eq .component "postfix") (eq .component "dovecot") (eq .component "submission-auth")) -}}
sidecar.istio.io/inject: "true"
sidecar.istio.io/nativeSidecar: "true"
traffic.sidecar.istio.io/excludeOutboundPorts: {{ .context.Values.postgres.port | quote }}
{{- else -}}
sidecar.istio.io/inject: "false"
{{- end -}}
{{- end }}

{{/*
Port Postfix dials for LMTP. With Istio this is the high mesh ingress port.
Dovecot itself keeps listening on dovecot.lmtp.containerPort.
*/}}
{{- define "agentmail.lmtpIngressPort" -}}
{{- if .Values.istio.enabled -}}
{{- .Values.istio.lmtpPort -}}
{{- else -}}
{{- .Values.dovecot.lmtp.servicePort -}}
{{- end -}}
{{- end }}

{{/*
Image reference. .registry is the registry host or host/path from
DOCKER_REGISTRY. .image is one images.* entry. An empty registry leaves the
repository unchanged. A repository that already names a registry host (a
component with a dot or colon, or localhost) is left unchanged. The registry
is prepended only to a short name, such as agentmail-postfix.
*/}}
{{- define "agentmail.image" -}}
{{- $registry := trimSuffix "/" (default "" .registry) -}}
{{- $repo := .image.repository -}}
{{- $first := first (splitList "/" $repo) -}}
{{- if and $registry (not (or (contains "." $first) (contains ":" $first) (eq $first "localhost"))) -}}
{{- printf "%s/%s:%s" $registry $repo .image.tag -}}
{{- else -}}
{{- printf "%s:%s" .image.repository .image.tag -}}
{{- end -}}
{{- end }}

{{- define "agentmail.containerSecurityContext" -}}
allowPrivilegeEscalation: false
capabilities:
  drop:
    - ALL
{{- end }}

{{- define "agentmail.internalNetworks" -}}
{{- join ", " .Values.networkPolicy.agentCIDRs }}
{{- end }}

{{/*
Database role, database name, and base64 password from the synced Secret.
*/}}
{{- define "agentmail.pgsqlSecretEnv" -}}
- name: POSTFIX_PGSQL_USER
  valueFrom:
    secretKeyRef:
      name: {{ include "agentmail.secretName" . }}
      key: {{ .Values.vaultwarden.keys.user }}
- name: POSTFIX_PGSQL_DBNAME
  valueFrom:
    secretKeyRef:
      name: {{ include "agentmail.secretName" . }}
      key: {{ .Values.vaultwarden.keys.database }}
- name: POSTFIX_PGSQL_PASSWORD_B64
  valueFrom:
    secretKeyRef:
      name: {{ include "agentmail.secretName" . }}
      key: {{ .Values.vaultwarden.keys.passwordB64 }}
{{- end }}

{{- define "agentmail.migrateContainer" -}}
- name: migrate
  image: {{ include "agentmail.image" (dict "registry" .Values.images.registry "image" .Values.images.directory) }}
  imagePullPolicy: {{ .Values.images.directory.pullPolicy }}
  args:
    - migrate
  envFrom:
    - configMapRef:
        name: {{ include "agentmail.configMapName" . }}
  env:
    {{- include "agentmail.pgsqlSecretEnv" . | nindent 4 }}
  securityContext:
    runAsUser: {{ .Values.directory.runAsUser }}
    runAsGroup: {{ .Values.directory.runAsGroup }}
    allowPrivilegeEscalation: false
    capabilities:
      drop:
        - ALL
{{- end }}

{{/*
Reject values the containers would refuse.
*/}}
{{- define "agentmail.validate" -}}
{{- $mailPorts := list .Values.postfix.containerPort .Values.postfix.submission.containerPort .Values.dovecot.imap.containerPort .Values.dovecot.lmtp.containerPort }}
{{- range $mailPorts }}
{{- if or (lt (int .) 1) (gt (int .) 65535) }}
{{- fail (printf "mail listen ports must be between 1 and 65535, got %v" .) }}
{{- end }}
{{- end }}
{{- if or (lt (int .Values.directory.containerPort) 1024) (gt (int .Values.directory.containerPort) 65535) }}
{{- fail (printf "directory listen port must be between 1024 and 65535, got %v" .Values.directory.containerPort) }}
{{- end }}
{{- if ne (int .Values.postfix.submission.containerPort) (int .Values.postfix.submission.servicePort) }}
{{- fail "postfix submission containerPort and servicePort must match" }}
{{- end }}
{{- if eq (int .Values.postfix.containerPort) (int .Values.postfix.submission.containerPort) }}
{{- fail "postfix submission port must differ from the smtp port" }}
{{- end }}
{{- if or (lt (int .Values.submissionAuth.containerPort) 1024) (gt (int .Values.submissionAuth.containerPort) 65535) }}
{{- fail (printf "submission auth listen port must be between 1024 and 65535, got %v" .Values.submissionAuth.containerPort) }}
{{- end }}
{{- if ne (int .Values.submissionAuth.containerPort) (int .Values.submissionAuth.servicePort) }}
{{- fail "submission auth containerPort and servicePort must match" }}
{{- end }}
{{- if or (eq (int .Values.submissionAuth.containerPort) (int .Values.postfix.submission.containerPort)) (eq (int .Values.submissionAuth.containerPort) (int .Values.postfix.containerPort)) (eq (int .Values.submissionAuth.containerPort) (int .Values.directory.containerPort)) }}
{{- fail "submission auth port must differ from smtp, submission, and directory" }}
{{- end }}
{{- range $cidr := concat .Values.networkPolicy.agentCIDRs .Values.networkPolicy.nodeCIDRs }}
{{- if or (eq $cidr "0.0.0.0/0") (eq $cidr "::/0") }}
{{- fail (printf "refusing world-open network %s" $cidr) }}
{{- end }}
{{- end }}
{{- if not .Values.networkPolicy.agentCIDRs }}
{{- fail "networkPolicy.agentCIDRs is required" }}
{{- end }}
{{- $secretName := .Values.vaultwarden.secretName }}
{{- if and $secretName (or (gt (len $secretName) 63) (not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$" $secretName))) }}
{{- fail "vaultwarden.secretName must be a DNS label of at most 63 characters, matching the item secret-name" }}
{{- end }}
{{- $registrySecretName := .Values.vaultwarden.registrySecretName }}
{{- if and $registrySecretName (or (gt (len $registrySecretName) 63) (not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$" $registrySecretName))) }}
{{- fail "vaultwarden.registrySecretName must be a DNS label of at most 63 characters, matching the item secret-name" }}
{{- end }}
{{- $keys := list .Values.vaultwarden.keys.user .Values.vaultwarden.keys.database .Values.vaultwarden.keys.passwordB64 .Values.vaultwarden.keys.postgresUser .Values.vaultwarden.keys.postgresDatabase .Values.vaultwarden.keys.postgresPassword }}
{{- range $keys }}
{{- if not (regexMatch "^[A-Za-z0-9][A-Za-z0-9._-]*$" .) }}
{{- fail (printf "vaultwarden secret key %q must contain only letters, digits, '.', '_', and '-'" .) }}
{{- end }}
{{- end }}
{{- $ssl := list "disable" "allow" "prefer" "require" "verify-ca" "verify-full" }}
{{- if not (has .Values.mail.pgsslmode $ssl) }}
{{- fail (printf "mail.pgsslmode %s is not a libpq sslmode" .Values.mail.pgsslmode) }}
{{- end }}
{{- if .Values.images.registry }}
{{- if or (contains "://" .Values.images.registry) (contains " " .Values.images.registry) }}
{{- fail "images.registry must be a registry host or host/path, without a URL scheme; set it from DOCKER_REGISTRY" }}
{{- end }}
{{- end }}
{{- if .Values.istio.enabled }}
{{- $istioNamespace := .Values.istio.systemNamespace }}
{{- if or (gt (len $istioNamespace) 63) (not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$" $istioNamespace)) }}
{{- fail "istio.systemNamespace must be a DNS label" }}
{{- end }}
{{- if or (lt (int .Values.istio.xdsPort) 1) (gt (int .Values.istio.xdsPort) 65535) }}
{{- fail (printf "istio.xdsPort must be between 1 and 65535, got %v" .Values.istio.xdsPort) }}
{{- end }}
{{- if or (lt (int .Values.istio.lmtpPort) 1024) (gt (int .Values.istio.lmtpPort) 65535) }}
{{- fail (printf "istio.lmtpPort must be a high port between 1024 and 65535, got %v" .Values.istio.lmtpPort) }}
{{- end }}
{{- end }}
{{- end }}
