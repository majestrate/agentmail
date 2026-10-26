# Submission SASL daemon. Runs as uid 5003 and listens on 50052.
# Postfix dials it with the Dovecot SASL protocol. The password check
# reads users.password_hash. The process is plaintext. The pod sidecar
# does mutual TLS.
#
# Build from the repo root so COPY stays repo-root-relative:
#   docker build -f submission-auth/Dockerfile -t agentmail-submission-auth .

FROM golang:1.27 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/submission-auth ./cmd/submission-auth

FROM debian:trixie

ENV DEBIAN_FRONTEND=noninteractive \
    PGSSLMODE=prefer \
    SUBMISSION_AUTH_LISTEN=:50052

RUN apt-get update \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends ca-certificates \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 5003 --home-dir /nonexistent --shell /usr/sbin/nologin submission

COPY --from=build /out/submission-auth /usr/local/bin/submission-auth

USER 5003:5003

ENTRYPOINT ["/usr/local/bin/submission-auth"]
