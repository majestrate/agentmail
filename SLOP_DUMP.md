# Agentmail

Mail for agents on an internal network. Distribution lists live in PostgreSQL. Postfix expands a list address to its members and delivers to local mailboxes over LMTP. Dovecot serves IMAP. A gRPC service is the management interface.

Mail is not relayed to the public internet. `default_transport` and `relay_transport` are `error`, and the SMTP listener sets `smtpd_relay_restrictions=reject_unauth_destination`. That listener is `127.0.0.1:25`. With Istio enabled the Postfix Service publishes port 25 and the sidecar forwards mesh clients to it. A milter on port 50053 accepts the null sender or a bare address, and it accepts a recipient only when the address is in `mail.domain` and is a user or a distribution list. Submission on port 587 requires SASL and does not use the milter. The chart refuses `0.0.0.0/0` and `::/0`.

The relay in `relay/` is a synchronous SMTP proxy for a transit pod between the mesh and a VPN path to a public MX. `relay.enabled` defaults to false. Turning it on does not set Postfix `relayhost`, and the error transport stays in place.

## Layout

| Path | Role |
| --- | --- |
| `postfix/` | Debian trixie Postfix image. Virtual alias and mailbox maps come from PostgreSQL. |
| `milter/` | Milter image. Postfix calls it for inbound SMTP. |
| `cmd/milter` | Milter process entrypoint. |
| `relay/` | Relay image. Synchronous SMTP proxy, uid 5005. |
| `cmd/relay` | Relay process entrypoint. |
| `internal/relay` | SMTP proxy. Two listeners, no queue. |
| `dovecot/` | Debian trixie Dovecot image. IMAP and LMTP over TCP. Maildir under `/var/mail/vhosts`. |
| `directory/` | Directory gRPC image. |
| `cmd/directory` | Process entrypoint. `directory migrate` applies migrations and exits. A normal start migrates, then serves. |
| `cmd/agentmail` | Management client for the directory gRPC service. |
| `proto/agentmail/directory/v1` | `agentmail.directory.v1` API. |
| `internal/migrate` | Goose migrations, embedded in the binary. |
| `internal/directory` | gRPC handlers. SQL lives in `sql/` and is prepared on each pool connection. |
| `charts/agentmail` | Helm chart for the stack. |

## Mail flow

1. Inbound mail arrives on Postfix port 25 from a mesh client, such as a smart host. Postfix calls the milter, which accepts recipients that are users or distribution lists in `mail.domain`. Agents submit mail on port 587 and authenticate as a mailbox user. With Istio disabled, port 25 stays on `127.0.0.1` and is not a Service port.
2. `virtual_alias_maps` expands a distribution-list address to member addresses.
3. `virtual_mailbox_maps` accepts users in `mail.domain`.
4. Postfix delivers with LMTP to the Dovecot Service.

CoreDNS, from this chart, answers `mail.hostname` and `mail.domain` with the Postfix Service. An MX record for the domain points at the hostname. The same Corefile also answers the single-label Service names Postfix dials. Postfix uses its own resolver and does not walk the pod search list, so those names otherwise come back as "host not found" and LMTP bounces.

A user address and a list address cannot be the same value. That keeps alias expansion from looping.

## Ports and users

Postfix and Dovecot run as root. Postfix rejects its control command for any other user, and Dovecot's master has to set up its login processes. Mailbox files are still owned by vmail (uid 5000). The milter, relay, directory, and Postgres stay unprivileged. Dovecot also listens for IMAPS on 993. Kubernetes Services map submission, IMAP, LMTP, and the milter. With Istio enabled the Postfix Service also maps SMTP. With the relay enabled, its mesh port is a Service and the VPN port is not.

| Process | UID | Container port | Service port |
| --- | --- | --- | --- |
| Dovecot master | 0 | 143, 993, 24 | 143, 10024 |
| Postfix SMTP | 0 | 127.0.0.1:25 | 25 with Istio |
| Postfix submission | 0 | 587 | 587 |
| Milter | 5004 | 50053 | 50053 |
| Relay | 5005 | 50054, 50055 | 50054 |
| Submission SASL | 5003 | 50052 | 50052 |
| Directory gRPC | 5002 | 50051 | 50051 |
| Postgres | 999 | 5432 | 5432 |

Postfix delivers to Dovecot LMTP through the Service on port 10024. Dovecot still listens on port 24, and the sidecar forwards the mesh connection there. Submission on port 587 requires SMTP AUTH. Postfix asks the submission auth daemon over SASL, and that daemon checks `users.password_hash`. The envelope sender has to be that user's address. Port 25 does not require a password. With Istio enabled, a mesh workload may connect to it and the milter decides which recipients exist. The listener rejects destinations outside the virtual mailbox domains. With Istio disabled, that port accepts mail only from inside the Postfix container. Outbound mail uses the error transport. The directory service mounts the same mail volume as Dovecot. Creating a user makes `/var/mail/vhosts/<domain>/<local>/Maildir` with `cur`, `new`, and `tmp`, owned by uid 5000. `/var/mail/vhosts` is mode 2775 and group vmail so uid 5002 can add domains. The directory binary carries file capability `cap_chown`, and that container keeps privilege escalation enabled. With escalation disabled the kernel drops the capability on exec and `chown` to uid 5000 fails. Mail and Postgres each have a 10Gi ReadWriteOnce volume, so those Deployments stay at one replica and use the Recreate strategy.

## Relay

The relay listens twice. The VPN listener accepts SMTP only from `relay.allowCIDRs` and only for recipients in `mail.domain`, then dials the internal Postfix Service on port 25. The mesh listener accepts SMTP from the mesh and dials `relay.outboundHost`. Recipients in `mail.domain` are refused on that listener so a message cannot loop. The mesh sidecar connects from `127.0.0.1`, so that listener is not filtered by source address. A message is acknowledged only after the next hop acknowledges it. The process does not queue, look up MX records, or offer AUTH or STARTTLS.

`relay.enabled` requires Istio. Set `relay.outboundHost` to the public MX address on the VPN and `relay.allowCIDRs` to the CIDRs allowed to open the VPN listener. `0.0.0.0/0` and `::/0` are refused. The chart excludes the VPN port from the sidecar and excludes those destination ranges, plus the MX address, from outbound capture, so the MX dial stays plaintext while the dial to Postfix stays mutual TLS. NetworkPolicy allows the Postfix pod to reach the mesh port and allows `relay.allowCIDRs` to reach the VPN port.

Postfix `default_transport` remains `error`. Pointing internal outbound mail at this relay is a separate change.

## Build

From the repository root, `make` builds all seven images. The Dockerfiles copy files by paths from that root, so the build context stays there. The image tag defaults to the Unix time of that `make` invocation. `make TAG=dev` sets another tag. `make charts` and `make apply` put that same tag on `agentmail-postfix`, `agentmail-dovecot`, `agentmail-directory`, `agentmail-submission-auth`, `agentmail-milter`, `agentmail-relay`, and `agentmail-maildir`. `make DOCKER=podman` selects the container tool.

```shell
make
```

That produces `agentmail-postfix`, `agentmail-dovecot`, `agentmail-directory`, `agentmail-submission-auth`, `agentmail-milter`, `agentmail-relay`, and `agentmail-maildir`, the directory gRPC server at `bin/directory`, and the management client at `bin/agentmail`. `make server` generates the protobuf code and builds those two binaries. Each image is also its own target: `make postfix`, `make dovecot`, `make directory`, `make submission-auth`, `make milter`, `make relay`, or `make maildir`. The directory and maildir image targets generate the protobuf code first. The directory image includes `/usr/local/bin/agentmail`.

`make charts` compiles `charts/agentmail` to `dist/agentmail.yaml`. Helm 3.19.0 is installed with `go install` when `helm` is not on `PATH`. `MAIL_DOMAIN` is required. It is a lowercase DNS name, and it sets `mail.domain`. `mail.hostname` stays the value in the chart. `SECRETS_PROVIDER` chooses who writes the Kubernetes Secret the pods read. Empty or `external` leaves an ExternalSecret pointed at a SecretStore that already exists. `aws` or `hashicorp` makes the chart create that SecretStore. `vaultwarden` does not render ExternalSecret; `make charts` also renders vaultwarden-kubernetes-secrets 2.1.0 to `dist/vaultwarden-kubernetes-secrets.yaml`, and `VAULTWARDEN_SERVER_URL` is required and must be an `https` URL. The mail chart does not store that URL. `VAULTWARDEN_ORGANIZATION_ID` and `VAULTWARDEN_COLLECTION_ID` are optional and, when set, are passed to the sync chart.

```shell
make charts \
  DOCKER_REGISTRY="$DOCKER_REGISTRY" \
  MAIL_DOMAIN="$MAIL_DOMAIN" \
  SECRETS_PROVIDER=aws \
  AWS_REGION=us-east-1
```

`make apply` runs `helm-add`, `helm-update`, and `helm-install` first, then renders the chart and applies `dist/agentmail.yaml`. Istio is installed before the mail manifests. External Secrets is installed before them when `SECRETS_PROVIDER` is empty, `external`, `aws`, or `hashicorp`. `SECRETS_PROVIDER=vaultwarden` installs vaultwarden-kubernetes-secrets instead, writes Secret `vaultwarden-kubernetes-secrets` from `BW_CLIENTID`, `BW_CLIENTSECRET`, and `VAULTWARDEN__MASTERPASSWORD` (`BW_MASTER_PASSWORD` when that is unset), and does not install External Secrets. A later apply reuses that Secret when those variables are unset. `kubectl` must be on `PATH`.

```shell
make apply \
  DOCKER_REGISTRY="$DOCKER_REGISTRY" \
  MAIL_DOMAIN="$MAIL_DOMAIN" \
  SECRETS_PROVIDER=hashicorp \
  VAULT_ADDR="$VAULT_ADDR" \
  VAULT_ROLE=agentmail
```

```shell
make apply \
  DOCKER_REGISTRY="$DOCKER_REGISTRY" \
  VAULTWARDEN_SERVER_URL="$VAULTWARDEN_SERVER_URL" \
  MAIL_DOMAIN="$MAIL_DOMAIN" \
  SECRETS_PROVIDER=vaultwarden \
  BW_CLIENTID="$BW_CLIENTID" \
  BW_CLIENTSECRET="$BW_CLIENTSECRET" \
  VAULTWARDEN__MASTERPASSWORD="$VAULTWARDEN__MASTERPASSWORD"
```

`make helm-add` adds the Istio and External Secrets Helm repositories. The Vaultwarden chart is OCI, so it has no repository to add. `make helm-update` refreshes the Istio and External Secrets repositories. `make helm-install` does both, then `helm upgrade --install` for `istio-base` and `istiod` at `ISTIO_VERSION` (1.31.3) and for the selected secret backend: `external-secrets` at `ESO_VERSION` (2.14.0), or `vaultwarden-kubernetes-secrets` at `VWK8S_VERSION` (2.1.0) when `SECRETS_PROVIDER=vaultwarden`. Istio uses the sidecar profile (`defaultRevision=default`). `make apply` depends on `helm-install`, so that order runs before the mail chart. `helm-install` alone does not install `charts/agentmail`. A second run upgrades the external releases in place. `VAULTWARDEN_SERVER_URL` is required only for the Vaultwarden backend. The sync credential Secret is written before that release.

```shell
make helm-install
```

`make teardown` reverses `make apply`. It deletes the mail namespace, including the mail and Postgres volumes, while `istiod` and the secret backend are still installed. It then uninstalls the `external-secrets` Helm release and deletes that namespace. It uninstalls `vaultwarden-kubernetes-secrets` when that release is present and deletes that namespace, including the sync Deployment and its credential Secret, plus that release's ClusterRole and ClusterRoleBinding. It uninstalls `istiod` and `istio-base`, then deletes the `istio-system` namespace. Missing releases and namespaces are left alone. `kubectl` and `helm` must be on `PATH`. `NAMESPACE` selects the mail namespace and must be a DNS label. The default is `agentmail`. `default`, `kube-system`, `kube-public`, and `kube-node-lease` are refused.

```shell
make teardown
```

`DOCKER_REGISTRY` is the registry host or host/path for the images this repository builds. The chart does not store it. An empty value leaves every repository as written. `agentmail-postfix`, `agentmail-dovecot`, `agentmail-directory`, `agentmail-submission-auth`, `agentmail-milter`, `agentmail-relay`, and `agentmail-maildir` are prefixed with it. `docker.io/library/postgres:17` stays on Docker Hub.

`make push` builds `agentmail-postfix`, `agentmail-dovecot`, `agentmail-directory`, `agentmail-submission-auth`, `agentmail-milter`, `agentmail-relay`, and `agentmail-maildir`, tags each as `$DOCKER_REGISTRY/<name>:$TAG`, and pushes those tags. `DOCKER_REGISTRY` is required. It is a registry host or host/path, with no URL scheme. One trailing slash is removed. Postgres is not pushed.

```shell
make push DOCKER_REGISTRY="$DOCKER_REGISTRY"
```

It is advised to store environmental variables into a file and source it when using the makefile:

```shell
# contents of .env.sh
export MAIL_DOMAIN=mail.example.com
...
```

Usage:

```shell
source .env.sh
make push apply
```
## Install

Database credentials are one Kubernetes Secret. `secrets.provider` chooses who writes it. Pods read that Secret. The chart does not store the database role, the database name, or the password.

With `secrets.provider` `external` (the default), External Secrets Operator copies one remote JSON object into the Secret. `make helm-install` installs the operator from `https://charts.external-secrets.io` at version 2.14.0, in namespace `external-secrets`, with `installCRDs=true`. `make apply` runs that install before it applies the mail chart. With `SECRETS_PROVIDER` empty or `external`, ExternalSecret `agentmail-mail-db` reads `agentmail/mail-db` from SecretStore `agentmail`, which must already exist. That store can be any provider the operator supports. Set `SECRETS_PROVIDER=aws` with `AWS_REGION` to have the chart create a Secrets Manager SecretStore. `AWS_SERVICE` is `SecretsManager` or `ParameterStore`. `AWS_ROLE` is an optional IAM role ARN the operator assumes. The operator's own identity is used. This chart does not store access keys. Set `SECRETS_PROVIDER=hashicorp` with `VAULT_ADDR` (an `https` URL) and `VAULT_ROLE` to create a Vault KV SecretStore. `VAULT_AUTH` is `kubernetes` or `token`. `VAULT_PATH` is the KV mount and defaults to `secret`. `VAULT_KV_VERSION` is `v1` or `v2`. Token auth reads `VAULT_TOKEN_SECRET` and does not put the token in the chart. `SECRETS_REMOTE_KEY` overrides the remote id.

With `secrets.provider` `vaultwarden`, [vaultwarden-kubernetes-secrets](https://github.com/antoniolago/vaultwarden-kubernetes-secrets) copies a Vaultwarden item into the same Secret. The mail chart does not render ExternalSecret and does not store a Vaultwarden server URL. `make helm-install` with `SECRETS_PROVIDER=vaultwarden` installs the sync service from `oci://ghcr.io/antoniolago/charts/vaultwarden-kubernetes-secrets` at version 2.1.0, in namespace `vaultwarden-kubernetes-secrets`, with `namespace.create=true` and `VAULTWARDEN__SERVERURL` set from `VAULTWARDEN_SERVER_URL`. The install uses `--take-ownership`. It writes Secret `vaultwarden-kubernetes-secrets` from `BW_CLIENTID`, `BW_CLIENTSECRET`, and `VAULTWARDEN__MASTERPASSWORD` before that release. Vaultwarden organization API keys do not work with its API, so the client id belongs to a user. `make charts` writes that release to `dist/vaultwarden-kubernetes-secrets.yaml`. The installed release is the Helm one from `make helm-install`.

`scripts/generate-vaultwarden-secrets.sh` generates the database role, database name, and password with `openssl` and stores them as one Vaultwarden login for the Vaultwarden backend. The password is hex, so the Postfix map line stays a single value. `POSTFIX_PGSQL_PASSWORD_B64` is that password in standard base64, one line. `POSTGRES_PASSWORD` is the password itself. The script needs the Bitwarden CLI (`bw`) and a personal API key. When `BW_MASTER_PASSWORD` is unset, the script prompts for the master password and does not echo it. The External Secrets backend does not read that Vaultwarden item.

```shell
VAULTWARDEN_SERVER_URL="$VAULTWARDEN_SERVER_URL" \
BW_CLIENTID="$BW_CLIENTID" \
BW_CLIENTSECRET="$BW_CLIENTSECRET" \
BW_MASTER_PASSWORD="$BW_MASTER_PASSWORD" \
scripts/generate-vaultwarden-secrets.sh
```

The item name and `secret-name` default to `agentmail-mail-db`. `namespaces` defaults to `agentmail`. Set `ROTATE=1` to replace an item that already has that name. Set `VAULTWARDEN_ORGANIZATION_ID` and `VAULTWARDEN_COLLECTION_ID` together to put the item in an organization collection. The custom fields the sync service copies are the same six keys as the External Secrets JSON properties, plus `namespaces` and `secret-name`.

The remote JSON properties are:

| Property | Value |
| --- | --- |
| `POSTFIX_PGSQL_USER` | Database role. Letters, digits, `.`, `_`, and `-` only. |
| `POSTGRES_USER` | The same role. |
| `POSTFIX_PGSQL_DBNAME` | Database name. Same character limits. |
| `POSTGRES_DB` | The same database name. |
| `POSTFIX_PGSQL_PASSWORD_B64` | Standard base64 of the password, one line, no newline. |
| `POSTGRES_PASSWORD` | The password itself. The Postgres image reads this directly. |

The password is hex when it is generated for a Postfix map, so the map line stays a single value. `POSTFIX_PGSQL_PASSWORD_B64` is that password in standard base64, one line. `POSTGRES_PASSWORD` is the password itself.

Point `networkPolicy.nodeCIDRs` at the node network so kubelet probes are allowed. `networkPolicy.agentCIDRs` is both the NetworkPolicy source and Postfix `mynetworks`.

```shell
helm install agentmail charts/agentmail -n agentmail --create-namespace \
  --set networkPolicy.nodeCIDRs[0]=NODE_CIDR \
  --set images.registry="$DOCKER_REGISTRY" \
  --set-string mail.domain="$MAIL_DOMAIN" \
  --set externalSecrets.secretStore.create=true \
  --set-string externalSecrets.secretStore.provider=aws \
  --set-string externalSecrets.aws.region=us-east-1
```

`DOCKER_REGISTRY` is a registry host or host/path, with no URL scheme and no trailing slash. It is prefixed onto `agentmail-postfix`, `agentmail-dovecot`, `agentmail-directory`, `agentmail-submission-auth`, `agentmail-milter`, `agentmail-relay`, and `agentmail-maildir`. The migration init container uses the same directory image. `docker.io/library/postgres:17` is left unchanged. Pods that pull those seven images reference image pull Secret `agentmail-registry` (override with `externalSecrets.registrySecretName`). When `images.registry` is set and `secrets.provider` is `external`, ExternalSecret `agentmail-registry` reads property `dockerconfigjson` from `agentmail/registry` (`SECRETS_REGISTRY_REMOTE_KEY` overrides that id) and writes a `kubernetes.io/dockerconfigjson` Secret. When `secrets.provider` is `vaultwarden`, the sync service writes that Secret from a login whose `secret-type` is `kubernetes.io/dockerconfigjson`. The chart does not store the registry username or password.

`charts/agentmail/values.yaml` holds the mail hostname, image names, ports, volume sizes, and the Secret key names. `mail.domain` defaults to `mail.internal` for a direct render. `make charts` and `make apply` replace it with `MAIL_DOMAIN`. `mail.hostname` stays `mail.internal` unless the chart value is changed. A release whose name contains `agentmail` keeps that name, so the default Secret is `agentmail-mail-db`. Set `externalSecrets.secretName` to use another Secret name. Both backends write that same name.

After install, agents submit mail on Service `agentmail-postfix` port 587 and read IMAP on `agentmail-dovecot` port 143. With Istio enabled, mesh clients deliver inbound mail to `agentmail-postfix` port 25.

## Istio

Istio authenticates the directory gRPC port, Postfix submission, inbound SMTP, the milter, the relay mesh port when the relay is enabled, the SASL hop from Postfix to the submission auth daemon, and the LMTP hop from Postfix to Dovecot. Directory, Postfix, Dovecot, the milter, and the submission auth daemon are injected, and so is the relay when it is enabled. Postgres and IMAP stay outside mutual TLS. The relay VPN port is excluded from the sidecar. Port 25 stays bound to loopback. The Postfix sidecar accepts it and forwards mesh clients to `127.0.0.1:25`. Postgres dials from those pods bypass the proxy. `istio.enabled` defaults to true. The LMTP mesh port is `istio.lmtpPort` (10024). Dovecot keeps listening on 24, and the Dovecot sidecar accepts 10024 and forwards it to `127.0.0.1:24`. That ingress requires STRICT mutual TLS. Submission on 587 and SMTP on 25 also require STRICT mutual TLS on those ports only. The SASL daemon and the milter accept connections only from Postfix. IMAP on 143 and IMAPS on 993 stay plaintext through the Dovecot sidecar. With Istio disabled, port 25 is not published.

Install the sidecar profile before applying this chart. Ambient mode is a different data plane and is not the one this chart uses. `make helm-install` installs Istio 1.31.3 from `https://blob.istio.io/istio-release/charts`: `istio-base` with `defaultRevision=default`, then `istiod`, both in namespace `istio-system`. Mesh pods carry the label `istio.io/rev` (`istio.revision`, default `default`) because that install selects pods by label. The inject annotation does not enroll a pod in an unlabeled namespace.

Service ports that carry SMTP, submission, LMTP, IMAP, SASL, the milter, or the relay are named with a `tcp-` prefix (`tcp-smtp`, `tcp-submission`, `tcp-lmtp`, `tcp-imap`, `tcp-sasl`, `tcp-milter`, `tcp-relay`). Istio treats any other name as a protocol to sniff, and it waits for the client to send the first bytes. SMTP, LMTP, submission, and the relay never do: the server writes the banner first, so the connection opens and then sits there with no response. The directory port stays named `grpc`, which Istio already recognizes. Container port names stay `smtp`, `submission`, `lmtp`, `imap`, `sasl`, `milter`, and `relay` so the probes still address them. The VPN container port is named `vpn` and is not a Service port.

The chart installs ServiceAccounts for directory, Postfix, Dovecot, the milter, and submission auth, and for the relay when it is enabled. Directory gRPC is `STRICT` with `ISTIO_MUTUAL`. Postfix uses a `PeerAuthentication` that is `STRICT` only on ports 25 and 587, and a `DestinationRule` of `ISTIO_MUTUAL` on those Service ports. The workload is not `STRICT`, so the kubelet probe of submission stays allowed. The submission auth daemon is `STRICT` on port 50052. The milter is `STRICT` on port 50053. The relay, when enabled, is `STRICT` only on its mesh port, with `ISTIO_MUTUAL` on that Service port. Its VPN port stays plaintext. LMTP uses a Dovecot `PeerAuthentication` that is `STRICT` only on port 10024, a `DestinationRule` of `ISTIO_MUTUAL` on that Service port, and a Dovecot `Sidecar` whose LMTP ingress is 10024. The Postfix sidecar ingress for SMTP is port 25 with `defaultEndpoint` `127.0.0.1:25`. NetworkPolicy allows any namespace to reach Postfix port 25 when Istio is enabled, and allows Postfix to reach the milter on 50053, the SASL daemon on 50052, Dovecot on 10024, and, when the relay is enabled, the relay mesh port. Those pods reach `istiod` on port 15012. The milter accepts connections only from the Postfix pod. The relay mesh port does too. Set `istio.enabled` to false to render the chart without those resources. With Istio disabled, the LMTP Service port stays 24, port 25 is not on the Service, the relay cannot be enabled, and submission stays on 587 without mutual TLS. SASL and the milter are still required.

Apply the mail chart after `istiod` is ready. The directory pod has to be created again once the injector webhook exists, or the gRPC port stays plaintext.

## Management client

`agentmail` calls the directory service in plaintext. Run it in the directory container so the sidecar can present a mesh certificate. Dial the Service DNS. A connection to `127.0.0.1` does not pass through the sidecar.

`users delete` removes the mailbox before it removes the user row. The directory container calls a second gRPC service, `agentmail-maildir`, in the same pod. That process runs as uid 5000, listens on `127.0.0.1:50056`, and deletes `MAIL_HOME_ROOT/<domain>/<local>`. It is not a Service port. A missing mailbox is already cleaned up. If the database delete fails afterward, the directory creates an empty mailbox again and leaves the user row in place.

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
