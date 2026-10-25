#!/bin/bash
# Configure Postfix from the environment, then start it. Runs as the
# unprivileged postfix user. Database passwords and custom SQL arrive as
# standard base64. Kubernetes publishes container port 2525 as SMTP port 25.

set -euo pipefail

ALIAS_MAP=/etc/postfix/pgsql-virtual-alias-maps.cf
MAILBOX_MAP=/etc/postfix/pgsql-virtual-mailbox-maps.cf
ALIAS_QUERY="SELECT destination FROM virtual_alias_maps WHERE source = lower('%s')"
MAILBOX_QUERY="SELECT address FROM users WHERE address = lower('%s')"

die() {
    printf '%s\n' "$*" >&2
    exit 1
}

if [[ "$(id -u)" -eq 0 ]]; then
    die "refusing to run Postfix as root; map SMTP port 25 to POSTFIX_SMTP_PORT with a Service"
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

require_line() {
    local name=$1 value=$2
    [[ -n "$value" ]] || die "${name} is empty"
    case $value in
        *$'\n'*) die "${name} must be a single line" ;;
    esac
}

valid_network() {
    local net=$1
    [[ "$net" != "0.0.0.0/0" && "$net" != "::/0" ]] || die "refusing world-open network ${net}"
    [[ "$net" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}/[0-9]{1,2}$ || "$net" =~ ^[0-9A-Fa-f:]+/[0-9]{1,3}$ ]] \
        || die "invalid network ${net}"
}

write_map() {
    local path=$1 query=$2
    umask 077
    {
        printf 'hosts = %s\n' "$hosts"
        printf 'user = %s\n' "$user"
        printf 'password = %s\n' "$password"
        printf 'dbname = %s\n' "$dbname"
        printf 'encoding = UTF8\n'
        printf 'query = %s\n' "$query"
    } > "$path"
}

configure_postfix() {
    local host port user dbname password query hosts myhostname
    local smtp_port lmtp_host lmtp_port domains domain joined net
    local -a nets=()

    host=${POSTFIX_PGSQL_HOST:?POSTFIX_PGSQL_HOST is required}
    port=${POSTFIX_PGSQL_PORT:-5432}
    user=${POSTFIX_PGSQL_USER:?POSTFIX_PGSQL_USER is required}
    dbname=${POSTFIX_PGSQL_DBNAME:?POSTFIX_PGSQL_DBNAME is required}
    [[ -v POSTFIX_PGSQL_PASSWORD_B64 ]] || die "POSTFIX_PGSQL_PASSWORD_B64 is required"
    password=$(b64dec POSTFIX_PGSQL_PASSWORD_B64 "$POSTFIX_PGSQL_PASSWORD_B64")
    require_line POSTFIX_PGSQL_HOST "$host"
    require_line POSTFIX_PGSQL_PORT "$port"
    require_line POSTFIX_PGSQL_USER "$user"
    require_line POSTFIX_PGSQL_DBNAME "$dbname"
    [[ "$port" =~ ^[0-9]+$ ]] || die "POSTFIX_PGSQL_PORT must be a number"
    [[ -n "${MAIL_DOMAIN:-}" ]] || die "MAIL_DOMAIN is required"
    [[ -n "${POSTFIX_INTERNAL_NETWORKS:-}" ]] || die "POSTFIX_INTERNAL_NETWORKS is required"

    smtp_port=${POSTFIX_SMTP_PORT:-2525}
    [[ "$smtp_port" =~ ^[0-9]+$ && "$smtp_port" -ge 1024 && "$smtp_port" -le 65535 ]] \
        || die "POSTFIX_SMTP_PORT must be an unprivileged port (1024-65535)"

    lmtp_host=${DOVECOT_LMTP_HOST:-dovecot}
    lmtp_port=${DOVECOT_LMTP_PORT:-24}
    require_line DOVECOT_LMTP_HOST "$lmtp_host"
    [[ "$lmtp_port" =~ ^[0-9]+$ ]] || die "DOVECOT_LMTP_PORT must be a number"

    if [[ -v POSTFIX_PGSQL_QUERY_B64 ]]; then
        query=$(b64dec POSTFIX_PGSQL_QUERY_B64 "$POSTFIX_PGSQL_QUERY_B64")
    else
        query=$ALIAS_QUERY
    fi

    if [[ "$host" == *:* ]]; then
        hosts=$host
    else
        hosts="${host}:${port}"
    fi

    write_map "$ALIAS_MAP" "$query"
    write_map "$MAILBOX_MAP" "$MAILBOX_QUERY"

    joined=""
    domains=${MAIL_DOMAIN//,/ }
    for domain in $domains; do
        require_line MAIL_DOMAIN "$domain"
        [[ "$domain" =~ ^[A-Za-z0-9.-]+$ ]] || die "invalid MAIL_DOMAIN ${domain}"
        if [[ -n "$joined" ]]; then
            joined+=", "
        fi
        joined+="$domain"
    done

    mynetworks="127.0.0.0/8, [::1]/128"
    for net in ${POSTFIX_INTERNAL_NETWORKS//,/ }; do
        valid_network "$net"
        nets+=("$net")
        mynetworks+=", ${net}"
    done

    myhostname=${POSTFIX_MYHOSTNAME:-$(hostname)}
    require_line POSTFIX_MYHOSTNAME "$myhostname"
    printf '%s\n' "$myhostname" > /etc/mailname

    # No public relay. Virtual mailboxes go to Dovecot; every other destination
    # is bounced by the error transport.
    postconf -e "myhostname=${myhostname}"
    postconf -e 'myorigin=$myhostname'
    postconf -e 'mydestination=localhost'
    postconf -e 'inet_interfaces=all'
    postconf -e "mynetworks=${mynetworks}"
    postconf -e 'smtpd_relay_restrictions=permit_mynetworks, reject_unauth_destination'
    postconf -e 'default_transport=error'
    postconf -e 'relay_transport=error'
    postconf -e 'relayhost='
    postconf -e "virtual_mailbox_domains=${joined}"
    postconf -e 'virtual_alias_domains='
    postconf -e "virtual_alias_maps=proxy:pgsql:${ALIAS_MAP}"
    postconf -e "virtual_mailbox_maps=proxy:pgsql:${MAILBOX_MAP}"
    postconf -e "virtual_transport=lmtp:inet:${lmtp_host}:${lmtp_port}"
    postconf -e 'maillog_file=/dev/stdout'
    postconf -e 'smtpd_banner=$myhostname ESMTP'
    postconf -F '*/*/chroot=n'

    # The packaged "smtp" service is port 25. Replace it with an unprivileged port.
    postconf -M "${smtp_port}/inet=${smtp_port} inet n - n - - smtpd"
    postconf -MX smtp/inet || true

    local imports
    imports=$(postconf -h import_environment)
    case " ${imports} " in
        *" PGSSLMODE "*) ;;
        *) postconf -e "import_environment=${imports} PGSSLMODE PGSSLROOTCERT" ;;
    esac

    postfix check
    printf 'postfix: listen %s, lmtp %s:%s, domains %s, agent networks %s\n' \
        "$smtp_port" "$lmtp_host" "$lmtp_port" "$joined" "${nets[*]}"
}

if [[ $# -eq 0 ]]; then
    set -- postfix start-fg
fi

case "$1" in
    postfix)
        configure_postfix
        ;;
esac

exec "$@"
