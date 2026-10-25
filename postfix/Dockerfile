# Postfix on Debian stable. The process runs as the unprivileged postfix user
# and listens on POSTFIX_SMTP_PORT (default 2525). A Kubernetes Service maps
# port 25 to that container port.
#
# Mail is accepted only from loopback and POSTFIX_INTERNAL_NETWORKS. List
# addresses expand through PostgreSQL virtual_alias_maps. Members are local
# mailboxes delivered to Dovecot over LMTP/TCP. Anything else uses the error
# transport, so mail is not relayed to the public internet.

FROM debian:stable

ENV DEBIAN_FRONTEND=noninteractive \
    PGSSLMODE=prefer \
    POSTFIX_PGSQL_PORT=5432 \
    POSTFIX_SMTP_PORT=2525 \
    DOVECOT_LMTP_HOST=dovecot \
    DOVECOT_LMTP_PORT=24

RUN printf '#!/bin/sh\nexit 101\n' > /usr/sbin/policy-rc.d \
    && chmod 755 /usr/sbin/policy-rc.d \
    && printf '%s\n' \
        'postfix postfix/main_mailer_type select Local only' \
        'postfix postfix/mailname string localhost' \
        'postfix postfix/destinations string localhost' \
        | debconf-set-selections \
    && apt-get update \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        postfix \
        postfix-pgsql \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* \
    && postconf -m | grep -qx pgsql \
    && groupmod -g 5001 postfix \
    && usermod -u 5001 postfix \
    && postfix set-permissions \
    && chown -R postfix:postfix /etc/postfix /etc/mailname /var/lib/postfix \
    && chown postfix:postfix /var/spool/postfix /var/spool/postfix/pid

COPY postfix/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod 755 /usr/local/bin/docker-entrypoint.sh

USER 5001:5001

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["postfix", "start-fg"]
