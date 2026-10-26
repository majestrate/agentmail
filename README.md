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
| `cmd/agentmail` | Management client for the directory gRPC service. |
| `proto/agentmail/directory/v1` | `agentmail.directory.v1` API. |
| `internal/migrate` | Goose migrations, embedded in the binary. |
| `internal/directory` | gRPC handlers. SQL lives in `sql/` and is prepared on each pool connection. |
| `charts/agentmail` | Helm chart for the stack. |

## Mail flow

1. An agent on the internal network delivers SMTP to the Postfix Service on port 25. A mesh client uses submission on port 587 and authenticates as a mailbox user.
2. `virtual_alias_maps` expands a distribution-list address to member addresses.
3. `virtual_mailbox_maps` accepts users in `mail.domain`.
4. Postfix delivers with LMTP to the Dovecot Service.

CoreDNS, from this chart, answers `mail.hostname` and `mail.domain` with the Postfix Service. An MX record for the domain points at the hostname. The same Corefile also answers the single-label Service names Postfix dials. Postfix uses its own resolver and does not walk the pod search list, so those names otherwise come back as "host not found" and LMTP bounces.

A user address and a list address cannot be the same value. That keeps alias expansion from looping.

## Ports and users

Postfix and Dovecot run as root. Postfix rejects its control command for any other user, and Dovecot's master has to set up its login processes. Mailbox files are still owned by vmail (uid 5000). Directory and Postgres stay unprivileged. Dovecot also listens for IMAPS on 993. Kubernetes Services map SMTP, IMAP, and LMTP.

| Process | UID | Container port | Service port |
| --- | --- | --- | --- |
| Dovecot master | 0 | 143, 993, 24 | 143, 10024 |
| Postfix SMTP | 0 | 25 | 25 |
| Postfix submission | 0 | 587 | 587 |
| Submission SASL | 5003 | 50052 | 50052 |
| Directory gRPC | 5002 | 50051 | 50051 |
| Postgres | 999 | 5432 | 5432 |

Postfix delivers to Dovecot LMTP through the Service on port 10024. Dovecot still listens on port 24, and the sidecar forwards the mesh connection there. Submission on port 587 requires SMTP AUTH. Postfix asks the submission auth daemon over SASL, and that daemon checks `users.password_hash`. The envelope sender has to be that user's address. Port 25 does not require a password. The directory service mounts the same mail volume as Dovecot. Creating a user makes `/var/mail/vhosts/<domain>/<local>/Maildir` with `cur`, `new`, and `tmp`, owned by uid 5000. `/var/mail/vhosts` is mode 2775 and group vmail so uid 5002 can add domains. The directory binary carries file capability `cap_chown`, and that container keeps privilege escalation enabled. With escalation disabled the kernel drops the capability on exec and `chown` to uid 5000 fails. Mail and Postgres each have a 10Gi ReadWriteOnce volume, so those Deployments stay at one replica and use the Recreate strategy.

## Build

From the repository root, `make` builds all four images. The Dockerfiles copy files by paths from that root, so the build context stays there. The image tag defaults to the Unix time of that `make` invocation. `make TAG=dev` sets another tag. `make charts` and `make apply` put that same tag on `agentmail-postfix`, `agentmail-dovecot`, `agentmail-directory`, and `agentmail-submission-auth`. `make DOCKER=podman` selects the container tool.

```shell
make
```

That produces `agentmail-postfix`, `agentmail-dovecot`, `agentmail-directory`, and `agentmail-submission-auth`, the directory gRPC server at `bin/directory`, and the management client at `bin/agentmail`. `make server` generates the protobuf code and builds those two binaries. Each image is also its own target: `make postfix`, `make dovecot`, `make directory`, or `make submission-auth`. The directory image target generates the protobuf code first. The directory image includes `/usr/local/bin/agentmail`.

`make charts` compiles `charts/agentmail` to `dist/agentmail.yaml` and the Vaultwarden Kubernetes secrets chart 2.1.0 to `dist/vaultwarden-kubernetes-secrets.yaml`. Helm 3.19.0 is installed with `go install` when `helm` is not on `PATH`. `VAULTWARDEN_SERVER_URL` is required and must be an `https` URL. The chart does not store it.

```shell
make charts \
  DOCKER_REGISTRY="$DOCKER_REGISTRY" \
  VAULTWARDEN_SERVER_URL="$VAULTWARDEN_SERVER_URL"
```

`VAULTWARDEN_ORGANIZATION_ID` and `VAULTWARDEN_COLLECTION_ID` are optional. When set, they are passed to the sync chart.

`make apply` runs `helm-add`, `helm-update`, and `helm-install` first, then renders the charts and applies `dist/agentmail.yaml`. Istio is installed before the mail manifests, which include its custom resources. `helm-install` writes Secret `vaultwarden-kubernetes-secrets` from `BW_CLIENTID`, `BW_CLIENTSECRET`, and `VAULTWARDEN__MASTERPASSWORD` (`BW_MASTER_PASSWORD` when that is unset) and installs the sync chart with Helm. A later apply reuses the Secret when those variables are unset. `kubectl` must be on `PATH`.

```shell
make apply \
  DOCKER_REGISTRY="$DOCKER_REGISTRY" \
  VAULTWARDEN_SERVER_URL="$VAULTWARDEN_SERVER_URL" \
  BW_CLIENTID="$BW_CLIENTID" \
  BW_CLIENTSECRET="$BW_CLIENTSECRET" \
  VAULTWARDEN__MASTERPASSWORD="$VAULTWARDEN__MASTERPASSWORD"
```

`make helm-add` adds the Istio Helm repository. `make helm-update` refreshes it. `make helm-install` does both, then `helm upgrade --install` for `istio-base` and `istiod` at `ISTIO_VERSION` (1.31.3) and for `vaultwarden-kubernetes-secrets` at `VWK8S_VERSION` (2.1.0). The Vaultwarden chart is OCI, so it has no repository to add. Istio uses the sidecar profile (`defaultRevision=default`). `make apply` depends on `helm-install`, so that order runs before the mail chart. `helm-install` alone does not install `charts/agentmail`. A second run upgrades the external releases in place. `VAULTWARDEN_SERVER_URL` is required. The sync credential Secret is written before the Vaultwarden release.

```shell
make helm-install \
  VAULTWARDEN_SERVER_URL="$VAULTWARDEN_SERVER_URL" \
  BW_CLIENTID="$BW_CLIENTID" \
  BW_CLIENTSECRET="$BW_CLIENTSECRET" \
  VAULTWARDEN__MASTERPASSWORD="$VAULTWARDEN__MASTERPASSWORD"
```

`make teardown` reverses `make apply`. It deletes the mail namespace, including the mail and Postgres volumes, while `istiod` is still installed. It then uninstalls the `vaultwarden-kubernetes-secrets` Helm release and deletes that namespace, including the sync Deployment and its credential Secret, plus that release's ClusterRole and ClusterRoleBinding. It uninstalls `istiod` and `istio-base`, then deletes the `istio-system` namespace. Missing releases and namespaces are left alone. `kubectl` and `helm` must be on `PATH`. `NAMESPACE` selects the mail namespace and must be a DNS label. The default is `agentmail`. `default`, `kube-system`, `kube-public`, and `kube-node-lease` are refused.

```shell
make teardown
```

`DOCKER_REGISTRY` is the registry host or host/path for the images this repository builds. The chart does not store it. An empty value leaves every repository as written. `agentmail-postfix`, `agentmail-dovecot`, `agentmail-directory`, and `agentmail-submission-auth` are prefixed with it. `docker.io/library/postgres:17` stays on Docker Hub.

`make push` builds `agentmail-postfix`, `agentmail-dovecot`, `agentmail-directory`, and `agentmail-submission-auth`, tags each as `$DOCKER_REGISTRY/<name>:$TAG`, and pushes those tags. `DOCKER_REGISTRY` is required. It is a registry host or host/path, with no URL scheme. One trailing slash is removed.

```shell
make push DOCKER_REGISTRY="$DOCKER_REGISTRY"
```

## Install

Database credentials come from [vaultwarden-kubernetes-secrets](https://github.com/antoniolago/vaultwarden-kubernetes-secrets). That service copies a Vaultwarden item into a Kubernetes Secret. This chart reads the Secret and does not store a Vaultwarden server URL, the database role, the database name, or the password.

`make helm-install` installs this sync service with Helm from `oci://ghcr.io/antoniolago/charts/vaultwarden-kubernetes-secrets` at version 2.1.0, in namespace `vaultwarden-kubernetes-secrets`, with `namespace.create=true` and `VAULTWARDEN__SERVERURL` set from `VAULTWARDEN_SERVER_URL`. The install uses `--take-ownership`, so a namespace and objects created earlier by `kubectl apply` can join the release. It writes Secret `vaultwarden-kubernetes-secrets` from `BW_CLIENTID`, `BW_CLIENTSECRET`, and `VAULTWARDEN__MASTERPASSWORD` before that release. Vaultwarden organization API keys do not work with its API, so the client id belongs to a user. `make apply` runs that install before it applies the mail chart.

`make charts` writes the same release to `dist/vaultwarden-kubernetes-secrets.yaml`. The installed release is the Helm one from `make helm-install`. The sync Deployment reads Secret `vaultwarden-kubernetes-secrets` (`BW_CLIENTID`, `BW_CLIENTSECRET`, and `VAULTWARDEN__MASTERPASSWORD`).

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

`DOCKER_REGISTRY` is a registry host or host/path, with no URL scheme and no trailing slash. It is prefixed onto `agentmail-postfix`, `agentmail-dovecot`, `agentmail-directory`, and `agentmail-submission-auth`. The migration init container uses the same directory image. `docker.io/library/postgres:17` is left unchanged. Pods that pull those four images reference image pull Secret `agentmail-registry` (override with `vaultwarden.registrySecretName`). That Secret is the docker config the sync service writes. The chart does not store the registry username or password.

`charts/agentmail/values.yaml` holds the mail domain, hostname, image names, ports, volume sizes, and the Vaultwarden Secret key names. `mail.domain` defaults to `example.com`. A release whose name contains `agentmail` keeps that name, so the default Secret is `agentmail-mail-db`. Set `vaultwarden.secretName` when the item uses another `secret-name`.

After install, agents on the agent CIDR use SMTP on Service `agentmail-postfix` port 25 and IMAP on `agentmail-dovecot` port 143.

## Istio

Istio authenticates the directory gRPC port, Postfix submission, the SASL hop from Postfix to the submission auth daemon, and the LMTP hop from Postfix to Dovecot. Directory, Postfix, Dovecot, and the submission auth daemon are injected. Postgres, SMTP on port 25, and IMAP stay outside mutual TLS. Postgres dials from those pods bypass the proxy. `istio.enabled` defaults to true. The LMTP mesh port is `istio.lmtpPort` (10024). Dovecot keeps listening on 24, and the Dovecot sidecar accepts 10024 and forwards it to `127.0.0.1:24`. That ingress requires STRICT mutual TLS. Submission on 587 also requires STRICT mutual TLS, and the SASL daemon accepts it only from Postfix. IMAP on 143 and IMAPS on 993 stay plaintext through the Dovecot sidecar. Port 25 stays plaintext.

Install the sidecar profile before applying this chart. Ambient mode is a different data plane and is not the one this chart uses. `make helm-install` installs Istio 1.31.3 from `https://blob.istio.io/istio-release/charts`: `istio-base` with `defaultRevision=default`, then `istiod`, both in namespace `istio-system`. Mesh pods carry the label `istio.io/rev` (`istio.revision`, default `default`) because that install selects pods by label. The inject annotation does not enroll a pod in an unlabeled namespace.

Service ports that carry SMTP, submission, LMTP, IMAP, or SASL are named with a `tcp-` prefix. Istio treats any other name as a protocol to sniff, and it waits for the client to send the first bytes. LMTP and SMTP never do: the server writes the banner first, so the connection opens and then sits there with no response. The directory port stays named `grpc`, which Istio already recognizes. Container port names stay `smtp`, `submission`, `lmtp`, `imap`, and `sasl` so the probes still address them.

The chart installs ServiceAccounts for directory, Postfix, Dovecot, and submission auth. Directory gRPC is `STRICT` with `ISTIO_MUTUAL`. Submission uses a Postfix `PeerAuthentication` that is `STRICT` only on port 587, and a `DestinationRule` of `ISTIO_MUTUAL` on that Service port. The submission auth daemon is `STRICT` on port 50052. LMTP uses a Dovecot `PeerAuthentication` that is `STRICT` only on port 10024, a `DestinationRule` of `ISTIO_MUTUAL` on that Service port, and a Dovecot `Sidecar` whose LMTP ingress is 10024. NetworkPolicy allows Postfix to reach the SASL daemon on 50052 and Dovecot on 10024, and those pods to reach `istiod` on port 15012. Set `istio.enabled` to false to render the chart without those resources. With Istio disabled, the LMTP Service port stays 24 and submission stays on 587 without mutual TLS. SASL is still required.

Apply the mail chart after `istiod` is ready. The directory pod has to be created again once the injector webhook exists, or the gRPC port stays plaintext.

## Management client

`agentmail` calls the directory service in plaintext. Run it in the directory container so the sidecar can present a mesh certificate. Dial the Service DNS. A connection to `127.0.0.1` does not pass through the sidecar.

```shell
kubectl exec -n agentmail deploy/agentmail-directory -c directory -- agentmail users ls
```

`AGENTMAIL_SERVER` in that container is `agentmail-directory.agentmail.svc.cluster.local:50051`. `-server` and `--server` override it.

```text
agentmail users create -address a@example.com -display-name A -password secret
agentmail users get -id 1
agentmail users ls
agentmail users update -id 1 -display-name B
agentmail users delete -id 1
agentmail lists create -address team@example.com -display-name Team
agentmail lists get -address team@example.com
agentmail lists ls
agentmail lists update -id 1 -display-name Team
agentmail lists delete -id 1
agentmail members add -list 1 -user 4
agentmail members remove -list 1 -user 4
agentmail members set -list 1 -users 4,5
agentmail members ls -list 1
agentmail memberships ls -user 4
```

List commands follow page tokens. `-page-size` defaults to 100.

## Directory API

Package `agentmail.directory.v1`. The service creates, reads, lists, updates, and deletes users and distribution lists. Membership RPCs are `AddMember`, `RemoveMember`, `SetMembers`, `ListMembers`, and `ListUserMemberships`.

Addresses are bare (`user@example.com`). `Name <user@example.com>` is rejected. Values are stored lowercase, using `net/mail.ParseAddress`. List page tokens are standard base64 of the last numeric id. An empty token is the first page. The default page size is 100 and the maximum is 500.

IMAP passwords are bcrypt, stored as Dovecot `{BLF-CRYPT}` hashes. The API never returns them. Passwords in the environment and in Postfix map files are standard base64, decoded at startup. Nothing in this project uses percent-encoding.

`go test ./...` covers address validation, base64, password hashing, embedded SQL, the migration list, and the management client. It does not open PostgreSQL.

Schema changes belong in `internal/migrate/migrations/`. The current migration is `00001_directory.sql`.
