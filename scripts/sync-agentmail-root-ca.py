#!/usr/bin/env python3
"""Copy a cert-manager CA secret and its public certificate.

Reads a Secret JSON file. Writes a Secret JSON file containing only tls.crt,
tls.key, and ca.crt, and a ConfigMap JSON file containing only the decoded
ca.crt. Does not print certificate or key material.
"""

import base64
import json
import sys


def main() -> None:
    if len(sys.argv) != 7:
        sys.stderr.write(
            "usage: sync-agentmail-root-ca.py SRC SECRET_OUT CM_OUT "
            "SECRET_NAMESPACE CM_NAMESPACE NAME\n"
        )
        sys.exit(2)
    src, secret_out, cm_out, secret_ns, cm_ns, name = sys.argv[1:]
    with open(src, encoding="utf-8") as handle:
        obj = json.load(handle)
    data = obj.get("data") or {}
    missing = [key for key in ("tls.crt", "tls.key", "ca.crt") if not data.get(key)]
    if missing:
        sys.stderr.write("root secret is missing %s\n" % ", ".join(missing))
        sys.exit(1)
    try:
        pem = base64.b64decode(data["ca.crt"]).decode("ascii")
    except (ValueError, UnicodeDecodeError):
        sys.stderr.write("root ca.crt is not a PEM certificate\n")
        sys.exit(1)
    if "BEGIN CERTIFICATE" not in pem or "END CERTIFICATE" not in pem:
        sys.stderr.write("root ca.crt is not a PEM certificate\n")
        sys.exit(1)
    labels = {
        "app.kubernetes.io/name": name,
        "app.kubernetes.io/part-of": "agentmail",
    }
    secret = {
        "apiVersion": "v1",
        "kind": "Secret",
        "type": "kubernetes.io/tls",
        "metadata": {"name": name, "namespace": secret_ns, "labels": labels},
        "data": {key: data[key] for key in ("tls.crt", "tls.key", "ca.crt")},
    }
    configmap = {
        "apiVersion": "v1",
        "kind": "ConfigMap",
        "metadata": {"name": name, "namespace": cm_ns, "labels": labels},
        "data": {"ca.crt": pem},
    }
    with open(secret_out, "w", encoding="utf-8") as handle:
        json.dump(secret, handle)
    with open(cm_out, "w", encoding="utf-8") as handle:
        json.dump(configmap, handle)


if __name__ == "__main__":
    main()
