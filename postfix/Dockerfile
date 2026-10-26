# Postfix on Debian trixie. setup.sh runs as root during the image build and
# is deleted before the image is finished. At start, postfix-runtime.sh
# configures Postfix from the environment as root, which the postfix control
# command requires. SMTP listens on 127.0.0.1:POSTFIX_SMTP_PORT (default 25)
# and asks the milter at MILTER_HOST:MILTER_PORT. That listener rejects
# destinations this host does not accept. POSTFIX_SUBMISSION_PORT adds a SASL
# submission listener on every interface and does not use the milter.
#
# List addresses expand through PostgreSQL virtual_alias_maps. Members are
# local mailboxes delivered to Dovecot over LMTP/TCP. Anything else uses the
# error transport, so mail is not relayed to the public internet.

FROM debian:trixie

ENV DEBIAN_FRONTEND=noninteractive \
    PGSSLMODE=prefer \
    POSTFIX_PGSQL_PORT=5432 \
    POSTFIX_SMTP_PORT=25 \
    DOVECOT_LMTP_HOST=dovecot \
    DOVECOT_LMTP_PORT=24

COPY postfix/setup.sh /tmp/postfix-setup.sh
RUN chmod 755 /tmp/postfix-setup.sh \
    && /tmp/postfix-setup.sh \
    && rm -f /tmp/postfix-setup.sh

COPY postfix/runtime.sh /usr/local/bin/postfix-runtime.sh
RUN chmod 755 /usr/local/bin/postfix-runtime.sh

ENTRYPOINT ["/usr/local/bin/postfix-runtime.sh"]
CMD ["postfix", "start-fg"]
