# Dovecot IMAP and LMTP on Debian trixie. The master runs as root so it can
# set up its login processes. Mailbox files are owned by vmail (uid 5000).
# LMTP listens on port 24. A Kubernetes Service maps 143 to the IMAP port.
# Mailboxes are Maildir files under /var/mail,
# which is a persistent volume.

FROM debian:trixie

ENV DEBIAN_FRONTEND=noninteractive \
    PGSSLMODE=prefer \
    DOVECOT_IMAP_PORT=10143 \
    DOVECOT_LMTP_LISTEN_PORT=24 \
    POSTFIX_PGSQL_PORT=5432

RUN apt-get update \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        dovecot-core \
        dovecot-imapd \
        dovecot-lmtpd \
        dovecot-pgsql \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --system --gid 5000 vmail \
    && useradd --system --uid 5000 --gid 5000 --home-dir /var/mail --shell /usr/sbin/nologin vmail \
    && mkdir -p /var/mail/vhosts /run/dovecot \
    && chown -R vmail:vmail /var/mail /run/dovecot /etc/dovecot

COPY dovecot/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod 755 /usr/local/bin/docker-entrypoint.sh

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["dovecot", "-F"]
