#!/bin/bash
# Write Dovecot's SQL auth config and start the master as root. Mailbox files
# stay owned by vmail. The database password arrives as standard base64.
# IMAP, IMAPS, and LMTP may listen on privileged ports.

set -euo pipefail

die() {
    printf '%s\n' "$*" >&2
    exit 1
}

if [[ "$(id -u)" -ne 0 ]]; then
    die "dovecot must run as root"
fi

b64dec() {
    local name=$1 raw=$2 stripped decoded
    stripped=$(printf '%s' "$raw" | tr -d '[:space:]')
    [[ -n "$stripped" ]] || die "${name} is empty"
    if ! decoded=$(printf '%s' "$stripped" | base64 -d 2>/dev/null); then
        die "${name} is not valid base64"
    fi
    case $decoded in
        *$'\n'*) die "${name} must decode to a single line" ;;
    esac
    printf '%s' "$decoded"
}

quote_value() {
    local value=$1 escaped
    escaped=${value//\\/\\\\}
    escaped=${escaped//\"/\\\"}
    printf '"%s"' "$escaped"
}

configure_dovecot() {
    local host port user dbname password imap_port lmtp_port sslmode quoted
    host=${POSTFIX_PGSQL_HOST:?POSTFIX_PGSQL_HOST is required}
    port=${POSTFIX_PGSQL_PORT:-5432}
    user=${POSTFIX_PGSQL_USER:?POSTFIX_PGSQL_USER is required}
    dbname=${POSTFIX_PGSQL_DBNAME:?POSTFIX_PGSQL_DBNAME is required}
    [[ -v POSTFIX_PGSQL_PASSWORD_B64 ]] || die "POSTFIX_PGSQL_PASSWORD_B64 is required"
    password=$(b64dec POSTFIX_PGSQL_PASSWORD_B64 "$POSTFIX_PGSQL_PASSWORD_B64")
    sslmode=${PGSSLMODE:-prefer}
    imap_port=${DOVECOT_IMAP_PORT:-10143}
    lmtp_port=${DOVECOT_LMTP_LISTEN_PORT:-24}

    [[ "$host" =~ ^[A-Za-z0-9._-]+$ ]] || die "POSTFIX_PGSQL_HOST must be a hostname"
    [[ "$port" =~ ^[0-9]+$ ]] || die "POSTFIX_PGSQL_PORT must be a number"
    [[ "$user" =~ ^[A-Za-z0-9._-]+$ ]] || die "POSTFIX_PGSQL_USER must be a plain identifier"
    [[ "$dbname" =~ ^[A-Za-z0-9._-]+$ ]] || die "POSTFIX_PGSQL_DBNAME must be a plain identifier"
    [[ "$imap_port" =~ ^[0-9]+$ && "$imap_port" -ge 1 && "$imap_port" -le 65535 ]] || die "DOVECOT_IMAP_PORT must be a port number"
    [[ "$lmtp_port" =~ ^[0-9]+$ && "$lmtp_port" -ge 1 && "$lmtp_port" -le 65535 ]] || die "DOVECOT_LMTP_LISTEN_PORT must be a port number"
    case "$sslmode" in
        disable|allow|prefer|require|verify-ca|verify-full) ;;
        *) die "invalid PGSSLMODE ${sslmode}" ;;
    esac

    quoted=$(quote_value "$password")
    umask 077
    cat > /etc/dovecot/dovecot.conf <<EOF
dovecot_config_version = 2.4.0
dovecot_storage_version = 2.4.0
protocols = imap lmtp
listen = *
base_dir = /run/dovecot
auth_allow_cleartext = yes
auth_mechanisms = plain login
auth_username_format = %{user | lower}
log_path = /dev/stdout
info_log_path = /dev/stdout

mail_driver = maildir
mail_home = /var/mail/vhosts/%{user | domain}/%{user | username}
mail_path = %{home}/Maildir
mail_uid = vmail
mail_gid = vmail

namespace inbox {
  inbox = yes
}

sql_driver = pgsql
pgsql ${host} {
  parameters {
    host = ${host}
    port = ${port}
    user = ${user}
    password = ${quoted}
    dbname = ${dbname}
    sslmode = ${sslmode}
  }
}

passdb sql {
  default_password_scheme = BLF-CRYPT
  query = SELECT address AS user, password_hash AS password FROM users WHERE address = '%{user}'
}

userdb static {
  fields {
    uid = vmail
    gid = vmail
    home = /var/mail/vhosts/%{user | domain}/%{user | username}
  }
}

service imap-login {
  inet_listener imap {
    port = ${imap_port}
  }
  inet_listener imaps {
    port = 993
  }
}

service lmtp {
  inet_listener lmtp {
    port = ${lmtp_port}
  }
}

protocol lmtp {
  auth_username_format = %{user | lower}
}

!include_try /usr/share/dovecot/protocols.d/*.protocol
EOF

    mkdir -p /run/dovecot /var/mail/vhosts
    doveconf -n >/dev/null
    printf 'dovecot: imap %s, lmtp %s, db %s\n' "$imap_port" "$lmtp_port" "$host"
}

if [[ $# -eq 0 ]]; then
    set -- dovecot -F
fi

case "$1" in
    dovecot)
        configure_dovecot
        ;;
esac

exec "$@"
