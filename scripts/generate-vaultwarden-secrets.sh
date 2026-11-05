#!/bin/bash
# Generate the agentmail database secrets with openssl and store them in
# Vaultwarden. With secrets.provider vaultwarden, vaultwarden-kubernetes-secrets
# copies this item into the Kubernetes Secret the chart reads. With
# secrets.provider external, External Secrets copies a remote JSON object
# instead, and this Vaultwarden login is not that object.
#
# The Bitwarden CLI (bw) talks to Vaultwarden. This script keeps its own
# CLI data directory and removes it on exit. A personal API key is required.
# Vaultwarden does not accept organization API keys.
#
#   VAULTWARDEN_SERVER_URL=$VAULTWARDEN_SERVER_URL \
#   BW_CLIENTID=$BW_CLIENTID \
#   BW_CLIENTSECRET=$BW_CLIENTSECRET \
#   scripts/generate-vaultwarden-secrets.sh
#
# BW_MASTER_PASSWORD unlocks the account. When it is unset, the script
# prompts for it with echo disabled.
#
# NAMESPACE defaults to agentmail. SECRET_NAME and ITEM_NAME default to
# agentmail-mail-db. POSTGRES_USER and POSTGRES_DB are generated when unset.
# Set VAULTWARDEN_ORGANIZATION_ID and VAULTWARDEN_COLLECTION_ID together to
# store the item in an organization collection. Set ROTATE=1 to replace the
# values of an item that already has this name.
#
# When DOCKER_REGISTRY, REGISTRY_USERNAME, and REGISTRY_PASSWORD are all
# set, the script also stores a registry login in Vaultwarden.
# REGISTRY_SECRET_NAME defaults to agentmail-registry. REGISTRY_EMAIL is
# optional. The registry host is the first path component of DOCKER_REGISTRY,
# with no URL scheme. With secrets.provider vaultwarden, the sync service
# writes kubernetes.io/dockerconfigjson from this login. The external
# provider reads a dockerconfigjson property from its own secret store.

set -euo pipefail
umask 077

die() {
    printf '%s\n' "$1" >&2
    exit 1
}

require_cmd() {
    command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

require_cmd openssl
require_cmd bw
require_cmd jq

[[ -n "${VAULTWARDEN_SERVER_URL:-}" ]] || die "VAULTWARDEN_SERVER_URL is required"
[[ -n "${BW_CLIENTID:-}" ]] || die "BW_CLIENTID is required"
[[ -n "${BW_CLIENTSECRET:-}" ]] || die "BW_CLIENTSECRET is required"
if [[ -z "${BW_MASTER_PASSWORD:-}" ]]; then
    if ! { exec 3<>/dev/tty; } 2>/dev/null; then
        die "BW_MASTER_PASSWORD is required"
    fi
    printf 'Master password: ' >&3
    IFS= read -r -s BW_MASTER_PASSWORD <&3 || die "BW_MASTER_PASSWORD is required"
    printf '\n' >&3
    exec 3<&-
    [[ -n "$BW_MASTER_PASSWORD" ]] || die "BW_MASTER_PASSWORD is required"
    export BW_MASTER_PASSWORD
fi

server=${VAULTWARDEN_SERVER_URL%/}
[[ "$server" =~ ^https://[^[:space:]]+$ ]] || die "VAULTWARDEN_SERVER_URL must be an https URL"

namespace=${NAMESPACE:-agentmail}
secret_name=${SECRET_NAME:-agentmail-mail-db}
item_name=${ITEM_NAME:-$secret_name}
org=${VAULTWARDEN_ORGANIZATION_ID:-}
collection=${VAULTWARDEN_COLLECTION_ID:-}

dns='^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'
ident='^[A-Za-z0-9][A-Za-z0-9._-]*$'
[[ "$namespace" =~ $dns ]] || die "NAMESPACE must be a DNS label"
[[ "$secret_name" =~ $dns ]] || die "SECRET_NAME must be a DNS label"
[[ -n "$item_name" ]] || die "ITEM_NAME is required"
if [[ -n "$org" && -z "$collection" ]]; then
    die "VAULTWARDEN_COLLECTION_ID is required when VAULTWARDEN_ORGANIZATION_ID is set"
fi

if [[ -n "${POSTGRES_USER:-}" ]]; then
    db_user=$POSTGRES_USER
else
    db_user=$(printf 'am_%s' "$(openssl rand -hex 8)")
fi
if [[ -n "${POSTGRES_DB:-}" ]]; then
    db_name=$POSTGRES_DB
else
    db_name=$(printf 'am_%s' "$(openssl rand -hex 8)")
fi
[[ "$db_user" =~ $ident ]] || die "POSTGRES_USER must contain only letters, digits, '.', '_', and '-'"
[[ "$db_name" =~ $ident ]] || die "POSTGRES_DB must contain only letters, digits, '.', '_', and '-'"

registry=${DOCKER_REGISTRY:-}
registry_user=${REGISTRY_USERNAME:-}
registry_password=${REGISTRY_PASSWORD:-}
registry_email=${REGISTRY_EMAIL:-}
if [[ -n "$registry" || -n "$registry_user" || -n "$registry_password" || -n "$registry_email" ]]; then
    [[ -n "$registry" && -n "$registry_user" && -n "$registry_password" ]] || die "DOCKER_REGISTRY, REGISTRY_USERNAME, and REGISTRY_PASSWORD are required together"
fi
while [[ "$registry" == */ ]]; do registry=${registry%/}; done
registry_server=${registry%%/*}
if [[ -n "$registry" ]]; then
    [[ "$registry" != *"://"* && "$registry" != *" "* && -n "$registry_server" ]] || die "DOCKER_REGISTRY must be a registry host or host/path, without a URL scheme"
    [[ "$registry_user" != *$'\n'* && "$registry_password" != *$'\n'* && "$registry_email" != *$'\n'* ]] || die "registry credentials must be a single line"
fi
registry_secret=${REGISTRY_SECRET_NAME:-agentmail-registry}
registry_item=${REGISTRY_ITEM_NAME:-$registry_secret}
if [[ -n "$registry" ]]; then
    [[ "$registry_secret" =~ $dns ]] || die "REGISTRY_SECRET_NAME must be a DNS label"
    [[ -n "$registry_item" ]] || die "REGISTRY_ITEM_NAME is required"
    [[ "$registry_secret" != "$secret_name" ]] || die "REGISTRY_SECRET_NAME must differ from SECRET_NAME"
    [[ "$registry_item" != "$item_name" ]] || die "REGISTRY_ITEM_NAME must differ from ITEM_NAME"
fi

# Hex avoids spaces and '#' so the Postfix pgsql map keeps one password value.
password=$(openssl rand -hex 32)
password_b64=$(printf '%s' "$password" | openssl base64 -A)
decoded=$(printf '%s' "$password_b64" | openssl base64 -d -A)
[[ "$decoded" == "$password" ]] || die "openssl base64 round-trip failed"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
export BITWARDENCLI_APPDATA_DIR="$tmp/bw"
mkdir -p "$BITWARDENCLI_APPDATA_DIR"
export BW_CLIENTID BW_CLIENTSECRET
export AM_NS="$namespace" AM_SECRET="$secret_name" AM_ITEM="$item_name"
export AM_USER="$db_user" AM_DB="$db_name" AM_PASS="$password" AM_PASS_B64="$password_b64"

jq -n '[
  {name:"namespaces", value:env.AM_NS, type:0},
  {name:"secret-name", value:env.AM_SECRET, type:0},
  {name:"ignore-field", value:"username,password", type:0},
  {name:"POSTFIX_PGSQL_USER", value:env.AM_USER, type:1},
  {name:"POSTGRES_USER", value:env.AM_USER, type:1},
  {name:"POSTFIX_PGSQL_DBNAME", value:env.AM_DB, type:1},
  {name:"POSTGRES_DB", value:env.AM_DB, type:1},
  {name:"POSTFIX_PGSQL_PASSWORD_B64", value:env.AM_PASS_B64, type:1},
  {name:"POSTGRES_PASSWORD", value:env.AM_PASS, type:1}
]' > "$tmp/fields.json"

bw config server "$server" >/dev/null
bw login --apikey >/dev/null
BW_SESSION=$(bw unlock --raw --passwordenv BW_MASTER_PASSWORD)
export BW_SESSION
bw sync >/dev/null

store_item() {
    local fields_file=$1
    local item_name=$AM_ITEM
    local items count item_id
    items=$(bw list items --search "$item_name")
    count=$(printf '%s' "$items" | jq --arg name "$item_name" '[.[] | select(.name == $name)] | length')
    if [[ "$count" -gt 1 ]]; then
        die "more than one Vaultwarden item is named ${item_name}"
    fi
    if [[ "$count" -eq 1 ]]; then
        [[ "${ROTATE:-}" == 1 ]] || die "Vaultwarden item ${item_name} already exists; set ROTATE=1 to replace its values"
        item_id=$(printf '%s' "$items" | jq -r --arg name "$item_name" '[.[] | select(.name == $name)][0].id')
        bw get item "$item_id" > "$tmp/item.json"
        jq --slurpfile fields "$fields_file" '
            .name = env.AM_ITEM
            | .login.username = env.AM_USER
            | .login.password = env.AM_PASS
            | .fields = $fields[0]
        ' "$tmp/item.json" > "$tmp/updated.json"
        bw encode < "$tmp/updated.json" > "$tmp/encoded.txt"
        bw edit item "$item_id" < "$tmp/encoded.txt" >/dev/null
        printf 'updated Vaultwarden item %s\n' "$item_name"
    else
        bw get template item > "$tmp/template.json"
        bw get template item.login > "$tmp/login.json"
        jq --slurpfile login "$tmp/login.json" --slurpfile fields "$fields_file" \
            --arg org "$org" --arg col "$collection" '
            .type = 1
            | .name = env.AM_ITEM
            | .login = $login[0]
            | .login.username = env.AM_USER
            | .login.password = env.AM_PASS
            | .fields = $fields[0]
            | if $org != "" then .organizationId = $org | .collectionIds = [$col] else . end
        ' "$tmp/template.json" > "$tmp/item.json"
        bw encode < "$tmp/item.json" > "$tmp/encoded.txt"
        bw create item < "$tmp/encoded.txt" >/dev/null
        printf 'created Vaultwarden item %s\n' "$item_name"
    fi
}

export AM_ITEM="$item_name" AM_USER="$db_user" AM_PASS="$password"
store_item "$tmp/fields.json"
printf 'namespace %s\n' "$namespace"
printf 'secret-name %s\n' "$secret_name"

if [[ -n "$registry" ]]; then
    export AM_NS="$namespace" AM_SECRET="$registry_secret" AM_ITEM="$registry_item"
    export AM_USER="$registry_user" AM_PASS="$registry_password"
    export AM_SERVER="$registry_server" AM_EMAIL="$registry_email"
    jq -n '
        [
            {name:"namespaces", value:env.AM_NS, type:0},
            {name:"secret-name", value:env.AM_SECRET, type:0},
            {name:"secret-type", value:"kubernetes.io/dockerconfigjson", type:0},
            {name:"docker-config-json-server", value:env.AM_SERVER, type:0}
        ]
        + (if env.AM_EMAIL != "" then [{name:"docker-config-json-email", value:env.AM_EMAIL, type:0}] else [] end)
    ' > "$tmp/registry-fields.json"
    store_item "$tmp/registry-fields.json"
    printf 'registry-secret-name %s\n' "$registry_secret"
    printf 'docker-config-json-server %s\n' "$registry_server"
fi
