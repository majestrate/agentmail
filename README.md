# Agentmail

Mail for agents on an internal network. Distribution lists live in PostgreSQL. Postfix expands a list address to its members and delivers to local mailboxes over LMTP. Dovecot serves IMAP. A gRPC service is the management interface.

Mail is not relayed to the public internet. `default_transport` and `relay_transport` are `error`. Postfix accepts SMTP from loopback plus the configured agent CIDR, and refuses `0.0.0.0/0` and `::/0`.

## Layout

| Path | Role |
| --- | --- |
| `postfix/` | Debian trixie Postfix image. Virtual alias and mailbox maps come from PostgreSQL. |
| `dovecot/` | Debian trixie Dovecot image. IMAP and LMTP over TCP. Maildir under `/var/mail/vhosts`. |
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

Postfix and Dovecot run as root. Postfix rejects its control command for any other user, and Dovecot's master has to set up its login processes. Mailbox files are still owned by vmail (uid 5000). Directory and Postgres stay unprivileged. Dovecot also listens for IMAPS on 993. Kubernetes Services map SMTP, IMAP, and LMTP.

| Process | UID | Container port | Service port |
| --- | --- | --- | --- |
| Dovecot master | 0 | 10143, 24 | 143, 24 |
| Postfix SMTP | 0 | 25 | 25 |
| Directory gRPC | 5002 | 50051 | 50051 |
| Postgres | 999 | 5432 | 5432 |

Postfix delivers to Dovecot LMTP on port 24. The directory service mounts the same mail volume as Dovecot. Creating a user makes `/var/mail/vhosts/<domain>/<local>/Maildir` with `cur`, `new`, and `tmp`, owned by uid 5000. Mail and Postgres each have a 10Gi ReadWriteOnce volume, so those Deployments stay at one replica and use the Recreate strategy.

## Build

From the repository root, `make` builds all three images. The Dockerfiles copy files by paths from that root, so the build context stays there. The image tag defaults to the Unix time of that `make` invocation. `make TAG=dev` sets another tag. `make charts` and `make apply` put that same tag on `agentmail-postfix`, `agentmail-dovecot`, and `agentmail-directory`. `make DOCKER=podman` selects the container tool.

```shell
make
```

That produces `agentmail-postfix`, `agentmail-dovecot`, and `agentmail-directory`, and the directory gRPC server at `bin/directory`. `make server` generates the protobuf code and builds only that server. Each image is also its own target: `make postfix`, `make dovecot`, or `make directory`. The directory image target generates the protobuf code first.

`make charts` compiles `charts/agentmail` to `dist/agentmail.yaml` and the Vaultwarden Kubernetes secrets chart 2.1.0 to `dist/vaultwarden-kubernetes-secrets.yaml`. Helm 3.19.0 is installed with `go install` when `helm` is not on `PATH`. `VAULTWARDEN_SERVER_URL` is required and must be an `https` URL. The chart does not store it.

```shell
make charts \
  DOCKER_REGISTRY="$DOCKER_REGISTRY" \
  VAULTWARDEN_SERVER_URL="$VAULTWARDEN_SERVER_URL"
```

`VAULTWARDEN_ORGANIZATION_ID` and `VAULTWARDEN_COLLECTION_ID` are optional. When set, they are passed to the sync chart.

`make apply` renders both charts and creates the `vaultwarden-kubernetes-secrets` namespace when it is missing. It then writes Secret `vaultwarden-kubernetes-secrets` in that namespace from `BW_CLIENTID`, `BW_CLIENTSECRET`, and `VAULTWARDEN__MASTERPASSWORD`. `BW_MASTER_PASSWORD` is used when `VAULTWARDEN__MASTERPASSWORD` is unset. The target applies `dist/vaultwarden-kubernetes-secrets.yaml`, creates the `agentmail` namespace when it is missing, and applies `dist/agentmail.yaml`. `kubectl` must be on `PATH`. A later apply reuses the Secret when those variables are unset.

```shell
make apply \
  DOCKER_REGISTRY="$DOCKER_REGISTRY" \
  VAULTWARDEN_SERVER_URL="$VAULTWARDEN_SERVER_URL" \
  BW_CLIENTID="$BW_CLIENTID" \
  BW_CLIENTSECRET="$BW_CLIENTSECRET" \
  VAULTWARDEN__MASTERPASSWORD="$VAULTWARDEN__MASTERPASSWORD"
```

`make teardown` deletes the `agentmail` namespace and the resources in it, including the mail and Postgres volumes. It also deletes the `vaultwarden-kubernetes-secrets` namespace, including the sync Deployment and its credential Secret, and that release's ClusterRole and ClusterRoleBinding. `kubectl` must be on `PATH`. `NAMESPACE` selects the mail namespace and must be a DNS label. The default is `agentmail`.

```shell
make teardown
```

`DOCKER_REGISTRY` is the registry host or host/path for the images this repository builds. The chart does not store it. An empty value leaves every repository as written. `agentmail-postfix`, `agentmail-dovecot`, and `agentmail-directory` are prefixed with it. `docker.io/library/postgres:17` stays on Docker Hub.

`make push` builds `agentmail-postfix`, `agentmail-dovecot`, and `agentmail-directory`, tags each as `$DOCKER_REGISTRY/<name>:$TAG`, and pushes those tags. `DOCKER_REGISTRY` is required. It is a registry host or host/path, with no URL scheme. One trailing slash is removed.

```shell
make push DOCKER_REGISTRY="$DOCKER_REGISTRY"
```

## Install

Database credentials come from [vaultwarden-kubernetes-secrets](https://github.com/antoniolago/vaultwarden-kubernetes-secrets). That service copies a Vaultwarden item into a Kubernetes Secret. This chart reads the Secret and does not store a Vaultwarden server URL, the database role, the database name, or the password.

`make apply` installs this sync service from `dist/vaultwarden-kubernetes-secrets.yaml` and writes its credential Secret from `BW_CLIENTID`, `BW_CLIENTSECRET`, and `VAULTWARDEN__MASTERPASSWORD`. Vaultwarden organization API keys do not work with its API, so the client id belongs to a user. The same install through Helm is:

```shell
helm upgrade -i vaultwarden-kubernetes-secrets \
  oci://ghcr.io/antoniolago/charts/vaultwarden-kubernetes-secrets \
  --version 2.1.0 \
  -n vaultwarden-kubernetes-secrets --create-namespace \
  --set namespace.create=true \
  --set-string env.config.VAULTWARDEN__SERVERURL="$VAULTWARDEN_SERVER_URL"
```

`make charts` writes the same release to `dist/vaultwarden-kubernetes-secrets.yaml`. The sync Deployment reads Secret `vaultwarden-kubernetes-secrets` (`BW_CLIENTID`, `BW_CLIENTSECRET`, and `VAULTWARDEN__MASTERPASSWORD`). `make apply` creates that Secret before it applies the Deployment.

`scripts/generate-vaultwarden-secrets.sh` generates the database role, database name, and password with `openssl` and stores them as one Vaultwarden login. The password is hex, so the Postfix map line stays a single value. `POSTFIX_PGSQL_PASSWORD_B64` is that password in standard base64, one line. `POSTGRES_PASSWORD` is the password itself. The script needs the Bitwarden CLI (`bw`) and a personal API key. When `BW_MASTER_PASSWORD` is unset, the script prompts for the master password and does not echo it.

```shell
VAULTWARDEN_SERVER_URL="$VAULTWARDEN_SERVER_URL" \
BW_CLIENTID="$BW_CLIENTID" \
BW_CLIENTSECRET="$BW_CLIENTSECRET" \
BW_MASTER_PASSWORD="$BW_MASTER_PASSWORD" \
scripts/generate-vaultwarden-secrets.sh
```

The item name and `secret-name` default to `agentmail-mail-db`. `namespaces` defaults to `agentmail`. Set `ROTATE=1` to replace an item that already has that name. Set `VAULTWARDEN_ORGANIZATION_ID` and `VAULTWARDEN_COLLECTION_ID` together to put the item in an organization collection. The custom fields the sync service copies are:

| Custom field | Value |
| --- | --- |
| `namespaces` | Release namespace. |
| `secret-name` | Kubernetes Secret name. |
| `POSTFIX_PGSQL_USER` | Database role. Letters, digits, `.`, `_`, and `-` only. |
| `POSTGRES_USER` | The same role. |
| `POSTFIX_PGSQL_DBNAME` | Database name. Same character limits. |
| `POSTGRES_DB` | The same database name. |
| `POSTFIX_PGSQL_PASSWORD_B64` | Standard base64 of the password, one line, no newline. |
| `POSTGRES_PASSWORD` | The password itself. The Postgres image reads this directly. |

When `DOCKER_REGISTRY`, `REGISTRY_USERNAME`, and `REGISTRY_PASSWORD` are set, the same script stores a second login for the registry. `REGISTRY_EMAIL` is optional. `REGISTRY_SECRET_NAME` defaults to `agentmail-registry`. The sync service writes a `kubernetes.io/dockerconfigjson` Secret from that login. The item username and password are the registry login. `docker-config-json-server` is the registry host, the first path component of `DOCKER_REGISTRY`.

```shell
DOCKER_REGISTRY="$DOCKER_REGISTRY" \
REGISTRY_USERNAME="$REGISTRY_USERNAME" \
REGISTRY_PASSWORD="$REGISTRY_PASSWORD" \
VAULTWARDEN_SERVER_URL="$VAULTWARDEN_SERVER_URL" \
BW_CLIENTID="$BW_CLIENTID" \
BW_CLIENTSECRET="$BW_CLIENTSECRET" \
BW_MASTER_PASSWORD="$BW_MASTER_PASSWORD" \
scripts/generate-vaultwarden-secrets.sh
```

| Custom field | Value |
| --- | --- |
| `namespaces` | Release namespace. |
| `secret-name` | Kubernetes Secret name. Defaults to `agentmail-registry`. |
| `secret-type` | `kubernetes.io/dockerconfigjson`. |
| `docker-config-json-server` | Registry host. |
| `docker-config-json-email` | Optional account email. |

Point `networkPolicy.nodeCIDRs` at the node network so kubelet probes are allowed. `networkPolicy.agentCIDRs` is both the NetworkPolicy source and Postfix `mynetworks`.

```shell
helm install agentmail charts/agentmail -n agentmail --create-namespace \
  --set networkPolicy.nodeCIDRs[0]=NODE_CIDR \
  --set images.registry="$DOCKER_REGISTRY"
```

`DOCKER_REGISTRY` is a registry host or host/path, with no URL scheme and no trailing slash. It is prefixed onto `agentmail-postfix`, `agentmail-dovecot`, and `agentmail-directory`. The migration init container uses the same directory image. `docker.io/library/postgres:17` is left unchanged. Pods that pull those three images reference image pull Secret `agentmail-registry` (override with `vaultwarden.registrySecretName`). That Secret is the docker config the sync service writes. The chart does not store the registry username or password.

`charts/agentmail/values.yaml` holds the mail domain, hostname, image names, ports, volume sizes, and the Vaultwarden Secret key names. `mail.domain` defaults to `example.com`. A release whose name contains `agentmail` keeps that name, so the default Secret is `agentmail-mail-db`. Set `vaultwarden.secretName` when the item uses another `secret-name`.

After install, agents on the agent CIDR use SMTP on Service `agentmail-postfix` port 25, IMAP on `agentmail-dovecot` port 143, and the directory on `agentmail-directory` port 50051.

## Directory API

Package `agentmail.directory.v1`. The service creates, reads, lists, updates, and deletes users and distribution lists. Membership RPCs are `AddMember`, `RemoveMember`, `SetMembers`, `ListMembers`, and `ListUserMemberships`.

Addresses are bare (`user@example.com`). `Name <user@example.com>` is rejected. Values are stored lowercase, using `net/mail.ParseAddress`. List page tokens are standard base64 of the last numeric id. An empty token is the first page. The default page size is 100 and the maximum is 500.

IMAP passwords are bcrypt, stored as Dovecot `{BLF-CRYPT}` hashes. The API never returns them. Passwords in the environment and in Postfix map files are standard base64, decoded at startup. Nothing in this project uses percent-encoding.

`go test ./...` covers address validation, base64, password hashing, embedded SQL, and the migration list. It does not open PostgreSQL.

Schema changes belong in `internal/migrate/migrations/`. The current migration is `00001_directory.sql`.
