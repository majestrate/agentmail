# Postfix on Debian stable. setup.sh runs as root during the image build and
# is deleted before the image is finished. At start, postfix-runtime.sh
# configures Postfix from the environment as the unprivileged postfix user
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

COPY postfix/setup.sh /tmp/postfix-setup.sh
RUN chmod 755 /tmp/postfix-setup.sh \
    && /tmp/postfix-setup.sh \
    && rm -f /tmp/postfix-setup.sh

COPY postfix/runtime.sh /usr/local/bin/postfix-runtime.sh
RUN chmod 755 /usr/local/bin/postfix-runtime.sh

USER 5001:5001

ENTRYPOINT ["/usr/local/bin/postfix-runtime.sh"]
CMD ["postfix", "start-fg"]
