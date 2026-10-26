# SMTP relay. Runs as uid 5005 and listens on 50054 (mesh) and 50055 (VPN).
# It proxies one transaction at a time and does not queue mail.
# The process is plaintext. The pod sidecar does mutual TLS on the mesh port.
#
# Build from the repo root so COPY stays repo-root-relative:
#   docker build -f relay/Dockerfile -t agentmail-relay .

FROM golang:1.27 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/relay ./cmd/relay

FROM debian:trixie

ENV DEBIAN_FRONTEND=noninteractive \
    RELAY_MESH_LISTEN=:50054 \
    RELAY_VPN_LISTEN=:50055

RUN apt-get update \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends ca-certificates \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 5005 --home-dir /nonexistent --shell /usr/sbin/nologin relay

COPY --from=build /out/relay /usr/local/bin/relay

USER 5005:5005

ENTRYPOINT ["/usr/local/bin/relay"]
