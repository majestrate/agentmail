# Milter daemon. Runs as uid 5004 and listens on 50053.
# Postfix dials it with the Sendmail milter protocol for inbound SMTP.
# The recipient check reads users and distribution_lists. The process is
# plaintext. The pod sidecar does mutual TLS.
#
# Build from the repo root so COPY stays repo-root-relative:
#   docker build -f milter/Dockerfile -t agentmail-milter .

FROM golang:1.27 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/milter ./cmd/milter

FROM debian:trixie

ENV DEBIAN_FRONTEND=noninteractive \
    PGSSLMODE=prefer \
    MILTER_LISTEN=:50053

RUN apt-get update \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends ca-certificates \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 5004 --home-dir /nonexistent --shell /usr/sbin/nologin milter

COPY --from=build /out/milter /usr/local/bin/milter

USER 5004:5004

ENTRYPOINT ["/usr/local/bin/milter"]
