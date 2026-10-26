# gRPC directory service. Runs as uid 5002 and listens on 50051.
# Migrations and SQL queries are embedded in the binary.
# The image also ships the agentmail management client. The server and
# the client speak plaintext. The pod sidecar does mutual TLS.
#
# Build from the repo root so COPY stays repo-root-relative:
#   docker build -f directory/Dockerfile -t agentmail-directory .

FROM golang:1.27 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/directory ./cmd/directory \
    && CGO_ENABLED=0 go build -trimpath -o /out/agentmail ./cmd/agentmail

FROM debian:trixie

ENV DEBIAN_FRONTEND=noninteractive \
    PGSSLMODE=prefer \
    DIRECTORY_LISTEN=:50051

RUN apt-get update \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends ca-certificates libcap2-bin \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 5002 --home-dir /nonexistent --shell /usr/sbin/nologin directory

COPY --from=build /out/directory /usr/local/bin/directory
COPY --from=build /out/agentmail /usr/local/bin/agentmail

# uid 5002 chowns new mailboxes to the mail user. The capability has to be
# on the file. A non-root exec drops CAP_CHOWN when it is only in the
# bounding set. The chart leaves allowPrivilegeEscalation true so the
# kernel honors this file capability.
RUN setcap cap_chown=ep /usr/local/bin/directory

USER 5002:5002

ENTRYPOINT ["/usr/local/bin/directory"]
