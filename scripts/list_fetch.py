#!/usr/bin/env python3
"""Fetch each member mailbox and confirm list messages arrived.

Membership comes from the agentmail CLI. IMAP is plaintext to the Dovecot
Service. The mailbox is selected read-only.
"""

from __future__ import annotations

import argparse
import imaplib
import os
import sys
import time

from listmail import check_subject_prefix, load_list, message_subjects


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--list-id", type=int, required=True, help="distribution list id")
    parser.add_argument("--count", type=int, required=True, help="messages expected in each mailbox")
    parser.add_argument(
        "--subject",
        required=True,
        help="subject prefix used by list_send.py",
    )
    parser.add_argument(
        "--password",
        default="",
        help="password used for every member. Defaults to AGENTMAIL_PASSWORD",
    )
    parser.add_argument(
        "--imap-host",
        default=os.environ.get("IMAP_HOST", "agentmail-dovecot.agentmail.svc.cluster.local"),
    )
    parser.add_argument(
        "--imap-port",
        type=int,
        default=int(os.environ.get("IMAP_PORT", "143")),
    )
    parser.add_argument("--mailbox", default="INBOX")
    parser.add_argument("--wait", type=float, default=120, help="seconds to wait for delivery")
    parser.add_argument("--interval", type=float, default=3, help="seconds between IMAP retries")
    parser.add_argument("--cli", default=os.environ.get("AGENTMAIL_CLI", "agentmail"))
    parser.add_argument("--timeout", type=float, default=60)
    args = parser.parse_args(argv)
    if args.list_id <= 0:
        parser.error("--list-id must be positive")
    if not 1 <= args.count <= 1000:
        parser.error("--count must be from 1 to 1000")
    if not 1 <= args.imap_port <= 65535:
        parser.error("--imap-port must be a TCP port")
    if args.wait < 0 or args.interval <= 0:
        parser.error("--wait must be >= 0 and --interval must be positive")
    args.subject = check_subject_prefix(args.subject)
    if not args.password:
        args.password = os.environ.get("AGENTMAIL_PASSWORD", "")
    if not args.password:
        parser.error("password is required: --password or AGENTMAIL_PASSWORD")
    return args


def fetch_blob(data) -> bytes:
    chunks: list[bytes] = []
    for item in data or []:
        if item is None:
            continue
        if isinstance(item, tuple):
            parts = item
        else:
            parts = (item,)
        for part in parts:
            if isinstance(part, (bytes, bytearray)):
                chunks.append(bytes(part))
    return b"\n".join(chunks)


def message_ids(data) -> list[str]:
    if not data or data[0] in (None, b""):
        return []
    raw = data[0]
    if not isinstance(raw, (bytes, bytearray)):
        return []
    return raw.decode("ascii", "replace").split()


def present(imap: imaplib.IMAP4, subject: str) -> bool:
    status, data = imap.search(None, "SUBJECT", subject)
    if status != "OK":
        return False
    wanted = f"Subject: {subject}".encode()
    token = subject.encode()
    for message_id in message_ids(data):
        status, fetched = imap.fetch(message_id, "(BODY.PEEK[HEADER.FIELDS (SUBJECT)] BODY.PEEK[TEXT])")
        if status != "OK":
            continue
        blob = fetch_blob(fetched)
        # The subject token is in the header and again in the body.
        if wanted in blob and blob.count(token) >= 2:
            return True
    return False


def missing_subjects(args: argparse.Namespace, address: str, subjects: list[str]) -> list[str] | None:
    """Return subjects still absent. None means the IMAP session failed."""
    try:
        imap = imaplib.IMAP4(args.imap_host, args.imap_port, timeout=args.timeout)
    except OSError as err:
        print(f"imap {address}: {err}", file=sys.stderr, flush=True)
        return None
    try:
        imap.login(address, args.password)
        status, _ = imap.select(args.mailbox, readonly=True)
        if status != "OK":
            print(f"imap {address}: select {args.mailbox} failed", file=sys.stderr, flush=True)
            return None
        return [subject for subject in subjects if not present(imap, subject)]
    except imaplib.IMAP4.error as err:
        print(f"imap {address}: {err}", file=sys.stderr, flush=True)
        return None
    finally:
        try:
            imap.logout()
        except imaplib.IMAP4.error:
            pass


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    listed, members = load_list(args.cli, args.list_id, args.timeout)
    if not members:
        raise SystemExit(f"list {args.list_id} has no members")
    subjects = message_subjects(args.subject, args.count)
    print(
        f"list id={listed.id} address={listed.address} members={len(members)} "
        f"expect={args.count} subject_prefix={args.subject}",
        flush=True,
    )
    pending = list(members)
    found: dict[str, int] = {}

    def check(batch: list) -> list:
        still = []
        for member in batch:
            missing = missing_subjects(args, member.user_address, subjects)
            if missing is None:
                still.append(member)
                continue
            have = args.count - len(missing)
            found[member.user_address] = have
            if missing:
                still.append(member)
            else:
                print(f"{member.user_address} ok={have}", flush=True)
        return still

    # The first pass can be slower than delivery. --wait is time to retry
    # after that pass, so a long scan does not consume the delivery budget.
    pending = check(pending)
    deadline = time.monotonic() + args.wait
    while pending and time.monotonic() < deadline:
        time.sleep(min(args.interval, deadline - time.monotonic()))
        pending = check(pending)
    if pending:
        for member in pending:
            have = found.get(member.user_address, 0)
            print(f"{member.user_address} have={have} expect={args.count}", flush=True)
        print(f"missing mailboxes={len(pending)}", flush=True)
        return 1
    print(f"ok mailboxes={len(members)} messages={args.count}", flush=True)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except SystemExit as err:
        if err.code not in (0, None) and not isinstance(err.code, int):
            print(err.code, file=sys.stderr)
            sys.exit(1)
        raise
