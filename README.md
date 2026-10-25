# Agentmail

Mail for agents on an internal network. Distribution lists live in PostgreSQL. Postfix expands a list address to its members and delivers to local mailboxes over LMTP. Dovecot serves IMAP. A gRPC service is the management interface.

Mail is not relayed to the public internet. `default_transport` and `relay_transport` are `error`. Postfix accepts SMTP from loopback plus the configured agent CIDR, and refuses `0.0.0.0/0` and `::/0`.

## Layout

| Path | Role |
| --- | --- |
| `postfix/` | Debian stable Postfix image. Virtual alias and mailbox maps come from PostgreSQL. |
| `dovecot/` | Debian stable Dovecot image. IMAP and LMTP over TCP. Maildir under `/var/mail/vhosts`. |
| `directory/` | Directory gRPC image. |
| `cmd/directory` | Process entrypoint. `directory migrate` applies migrations and exits. A normal start migrates, then serves. |
| `proto/agentmail/directory/v1` | `agentmail.directory.v1` API. |
| `internal/migrate` | Goose migrations, embedded in the binary. |
| `internal/directory` | gRPC handlers. SQL lives in `sql/` and is prepared on each pool connection. |
| `charts/agentmail` | Helm chart for the stack. |

## Mail flow

1. An agent submits SMTP to the Postfix Service.
2. `virtual_alias_maps` expands a distribution-list address to member addresses.
3. `virtual_mailbox_maps` accepts users in `mail.domain`.
4. Postfix delivers with LMTP to the Dovecot Service.

A user address and a list address cannot be the same value. That keeps alias expansion from looping.

## Ports and users

Nothing in the stack runs as root, and nothing binds a privileged port. Kubernetes Services map the mail ports.

| Process | UID | Container port | Service port |
| --- | --- | --- | --- |
| Dovecot IMAP | 5000 | 10143 | 143 |
| Dovecot LMTP | 5000 | 2424 | 24 |
| Postfix SMTP | 5001 | 2525 | 25 |
| Directory gRPC | 5002 | 50051 | 50051 |
| Postgres | 999 | 5432 | 5432 |

Postfix connects to the Dovecot Service port (24), not the container port. Mail and Postgres each have a 10Gi ReadWriteOnce volume, so those Deployments stay at one replica and use the Recreate strategy.

## Build

From the repository root, `make` builds all three images. The Dockerfiles copy files by paths from that root, so the build context stays there. `make TAG=dev` sets the image tag. `make DOCKER=podman` selects the container tool.

```shell
make
```

That produces `agentmail-postfix`, `agentmail-dovecot`, and `agentmail-directory`, and the directory gRPC server at `bin/directory`. `make server` generates the protobuf code and builds only that server. Each image is also its own target: `make postfix`, `make dovecot`, or `make directory`. The directory image target generates the protobuf code first.

`make charts` compiles `charts/agentmail` and writes the rendered manifests to `dist/agentmail.yaml`. Helm 3.19.0 is installed with `go install` when `helm` is not on `PATH`. The chart does not store the Bitwarden server base, so pass it at compile time with the organization and project ids:

```shell
make charts \
  BITWARDEN_SERVER_URL="$BITWARDEN_SERVER_URL" \
  BITWARDEN_ORGANIZATION_ID="$BITWARDEN_ORGANIZATION_ID" \
  BITWARDEN_PROJECT_ID="$BITWARDEN_PROJECT_ID"
```

The chart expects those image names, plus `docker.io/library/postgres:17`.

## Install

External Secrets Operator must be installed with the Bitwarden SDK server. The SDK server is the in-cluster proxy. Pass the Bitwarden server base in `BITWARDEN_SERVER_URL` when Helm renders the chart. The chart calls that base plus `/api` and `/identity`.

```shell
helm install external-secrets external-secrets/external-secrets \
  -n external-secrets --create-namespace \
  --set bitwarden-sdk-server.enabled=true
```

Create these Secrets in the release namespace before the mail pods can start:

- `bitwarden-access-token`, key `token`: Bitwarden Secrets Manager machine-account access token.
- `bitwarden-sdk-server-ca`, key `ca.crt`: CA that signed the SDK server certificate. A SecretStore cannot read a Secret in another namespace.

In the Bitwarden project, create three secrets. Names must match exactly one secret each.

| Bitwarden secret | Kubernetes key | Value |
| --- | --- | --- |
| `agentmail-pgsql-user` | `POSTFIX_PGSQL_USER`, `POSTGRES_USER` | Database role. Letters, digits, `.`, `_`, and `-` only. |
| `agentmail-pgsql-database` | `POSTFIX_PGSQL_DBNAME`, `POSTGRES_DB` | Database name. Same character limits. |
| `agentmail-pgsql-password-b64` | `POSTFIX_PGSQL_PASSWORD_B64` | Standard base64 of the password, one line, no newline. |

The chart decodes that password for `POSTGRES_PASSWORD`. It does not store the password, the role, or the database name.

```shell
printf '%s' 'the-password' | base64 -w0
```

Set the organization and project, and point `networkPolicy.nodeCIDRs` at the node network so kubelet probes are allowed. `networkPolicy.agentCIDRs` is both the NetworkPolicy source and Postfix `mynetworks`.

```shell
helm install agentmail charts/agentmail -n agentmail --create-namespace \
  --set bitwarden.serverURL="$BITWARDEN_SERVER_URL" \
  --set bitwarden.organizationID=ORGANIZATION \
  --set bitwarden.projectID=PROJECT \
  --set networkPolicy.nodeCIDRs[0]=NODE_CIDR
```

`charts/agentmail/values.yaml` holds the mail domain, hostname, image names, ports, volume sizes, and Bitwarden secret names. `mail.domain` defaults to `example.com`.

After install, agents on the agent CIDR use SMTP on Service `agentmail-postfix` port 25, IMAP on `agentmail-dovecot` port 143, and the directory on `agentmail-directory` port 50051.

## Directory API

Package `agentmail.directory.v1`. The service creates, reads, lists, updates, and deletes users and distribution lists. Membership RPCs are `AddMember`, `RemoveMember`, `SetMembers`, `ListMembers`, and `ListUserMemberships`.

Addresses are bare (`user@example.com`). `Name <user@example.com>` is rejected. Values are stored lowercase, using `net/mail.ParseAddress`. List page tokens are standard base64 of the last numeric id. An empty token is the first page. The default page size is 100 and the maximum is 500.

IMAP passwords are bcrypt, stored as Dovecot `{BLF-CRYPT}` hashes. The API never returns them. Passwords in the environment and in Postfix map files are standard base64, decoded at startup. Nothing in this project uses percent-encoding.

`go test ./...` covers address validation, base64, password hashing, embedded SQL, and the migration list. It does not open PostgreSQL.

Schema changes belong in `internal/migrate/migrations/`. The current migration is `00001_directory.sql`.
