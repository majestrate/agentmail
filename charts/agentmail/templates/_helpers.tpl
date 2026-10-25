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

{{- define "agentmail.secretName" -}}
{{- printf "%s-mail-db" (include "agentmail.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "agentmail.secretStoreName" -}}
{{- printf "%s-bitwarden" (include "agentmail.fullname" .) | trunc 63 | trimSuffix "-" }}
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

{{/*
Self-hosted Bitwarden base with no trailing slash.
*/}}
{{- define "agentmail.bitwardenBase" -}}
{{- trimSuffix "/" .Values.bitwarden.serverURL }}
{{- end }}

{{- define "agentmail.image" -}}
{{- printf "%s:%s" .repository .tag }}
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
Database role, database name, and base64 password. Synced from Bitwarden.
*/}}
{{- define "agentmail.pgsqlSecretEnv" -}}
- name: POSTFIX_PGSQL_USER
  valueFrom:
    secretKeyRef:
      name: {{ include "agentmail.secretName" . }}
      key: POSTFIX_PGSQL_USER
- name: POSTFIX_PGSQL_DBNAME
  valueFrom:
    secretKeyRef:
      name: {{ include "agentmail.secretName" . }}
      key: POSTFIX_PGSQL_DBNAME
- name: POSTFIX_PGSQL_PASSWORD_B64
  valueFrom:
    secretKeyRef:
      name: {{ include "agentmail.secretName" . }}
      key: POSTFIX_PGSQL_PASSWORD_B64
{{- end }}

{{- define "agentmail.migrateContainer" -}}
- name: migrate
  image: {{ include "agentmail.image" .Values.images.directory }}
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
Reject values the containers or Bitwarden store would refuse.
*/}}
{{- define "agentmail.validate" -}}
{{- $ports := list .Values.postfix.containerPort .Values.dovecot.imap.containerPort .Values.dovecot.lmtp.containerPort .Values.directory.containerPort }}
{{- range $ports }}
{{- if or (lt (int .) 1024) (gt (int .) 65535) }}
{{- fail (printf "container listen ports must be between 1024 and 65535, got %v" .) }}
{{- end }}
{{- end }}
{{- range $cidr := concat .Values.networkPolicy.agentCIDRs .Values.networkPolicy.nodeCIDRs }}
{{- if or (eq $cidr "0.0.0.0/0") (eq $cidr "::/0") }}
{{- fail (printf "refusing world-open network %s" $cidr) }}
{{- end }}
{{- end }}
{{- if not .Values.networkPolicy.agentCIDRs }}
{{- fail "networkPolicy.agentCIDRs is required" }}
{{- end }}
{{- $base := include "agentmail.bitwardenBase" . }}
{{- if not $base }}
{{- fail "bitwarden.serverURL is required when rendering the chart; set it from BITWARDEN_SERVER_URL" }}
{{- end }}
{{- if not (hasPrefix "https://" $base) }}
{{- fail "bitwarden.serverURL must be an https URL" }}
{{- end }}
{{- if not .Values.bitwarden.organizationID }}
{{- fail "bitwarden.organizationID is required" }}
{{- end }}
{{- if not .Values.bitwarden.projectID }}
{{- fail "bitwarden.projectID is required" }}
{{- end }}
{{- if or (not .Values.bitwarden.authSecret.name) (not .Values.bitwarden.authSecret.key) }}
{{- fail "bitwarden.authSecret.name and bitwarden.authSecret.key are required" }}
{{- end }}
{{- if not (has .Values.bitwarden.ca.type (list "Secret" "ConfigMap")) }}
{{- fail "bitwarden.ca.type must be Secret or ConfigMap" }}
{{- end }}
{{- if or (not .Values.bitwarden.ca.name) (not .Values.bitwarden.ca.key) }}
{{- fail "bitwarden.ca.name and bitwarden.ca.key are required" }}
{{- end }}
{{- if or (not .Values.bitwarden.secrets.user) (not .Values.bitwarden.secrets.database) (not .Values.bitwarden.secrets.passwordB64) }}
{{- fail "bitwarden.secrets.user, database, and passwordB64 are required" }}
{{- end }}
{{- $ssl := list "disable" "allow" "prefer" "require" "verify-ca" "verify-full" }}
{{- if not (has .Values.mail.pgsslmode $ssl) }}
{{- fail (printf "mail.pgsslmode %s is not a libpq sslmode" .Values.mail.pgsslmode) }}
{{- end }}
{{- end }}
