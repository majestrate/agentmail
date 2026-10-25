#!/bin/bash
# Provision the Postfix image. The Dockerfile runs this as root at build time.
# Postfix itself is configured at container start from the environment.

set -euo pipefail

if [[ "$(id -u)" -ne 0 ]]; then
    printf '%s\n' "postfix setup must run as root" >&2
    exit 1
fi

printf '#!/bin/sh\nexit 101\n' > /usr/sbin/policy-rc.d
chmod 755 /usr/sbin/policy-rc.d
printf '%s\n' \
    'postfix postfix/main_mailer_type select Local only' \
    'postfix postfix/mailname string localhost' \
    'postfix postfix/destinations string localhost' \
    | debconf-set-selections

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get upgrade -y
apt-get install -y --no-install-recommends \
    ca-certificates \
    postfix \
    postfix-pgsql
apt-get clean
rm -rf /var/lib/apt/lists/*

postconf -m | grep -qx pgsql

groupmod -g 5001 postfix
usermod -u 5001 postfix
postfix set-permissions

chown -R postfix:postfix /etc/postfix /var/lib/postfix
chown postfix:postfix /var/spool/postfix /var/spool/postfix/pid
# The runtime user replaces this with POSTFIX_MYHOSTNAME.
printf '%s\n' localhost > /etc/mailname
chown postfix:postfix /etc/mailname
chmod 644 /etc/mailname
