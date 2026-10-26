# Maildir cleanup. Runs as the mail user, uid 5000, and listens on
# 127.0.0.1:50056. The directory container in the same pod calls it when
# a user is deleted. The process is plaintext and is not a Service port.
#
# Build from the repo root so COPY stays repo-root-relative:
#   docker build -f maildir/Dockerfile -t agentmail-maildir .

FROM golang:1.27 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/maildir ./cmd/maildir

FROM debian:trixie

ENV DEBIAN_FRONTEND=noninteractive \
    MAILDIR_LISTEN=127.0.0.1:50056 \
    MAIL_HOME_ROOT=/var/mail/vhosts

RUN apt-get update \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends ca-certificates \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --system --gid 5000 vmail \
    && useradd --system --uid 5000 --gid 5000 --home-dir /nonexistent --shell /usr/sbin/nologin vmail

COPY --from=build /out/maildir /usr/local/bin/maildir

USER 5000:5000

ENTRYPOINT ["/usr/local/bin/maildir"]
