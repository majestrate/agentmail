#!/usr/bin/env python3
"""Send messages to one distribution list through Postfix submission.

The list address and sender come from the agentmail CLI. Run this in a
mesh pod. Submission is plaintext to the Service; the sidecar does mutual
TLS. MAIL FROM is the authenticated mailbox.
"""

from __future__ import annotations

import argparse
import os
import smtplib
import sys
from email.message import EmailMessage

from listmail import check_subject_prefix, load_list, message_subject


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--list-id", type=int, required=True, help="distribution list id")
    parser.add_argument("--count", type=int, default=1, help="number of messages to send")
    parser.add_argument(
        "--sender",
        default="",
        help="submission mailbox. Default is the first member returned by the CLI",
    )
    parser.add_argument(
        "--password",
        default="",
        help="mailbox password. Defaults to AGENTMAIL_PASSWORD",
    )
    parser.add_argument(
        "--subject",
        default="",
        help="subject prefix. Each message is <prefix>-NN. Required so a later fetch can match",
    )
    parser.add_argument("--body", default="list message", help="text placed before the subject token")
    parser.add_argument(
        "--smtp-host",
        default=os.environ.get("SMTP_HOST", "agentmail-postfix.agentmail.svc.cluster.local"),
    )
    parser.add_argument(
        "--smtp-port",
        type=int,
        default=int(os.environ.get("SMTP_PORT", "587")),
    )
    parser.add_argument("--cli", default=os.environ.get("AGENTMAIL_CLI", "agentmail"))
    parser.add_argument("--timeout", type=float, default=60)
    args = parser.parse_args(argv)
    if args.list_id <= 0:
        parser.error("--list-id must be positive")
    if not 1 <= args.count <= 1000:
        parser.error("--count must be from 1 to 1000")
    if not 1 <= args.smtp_port <= 65535:
        parser.error("--smtp-port must be a TCP port")
    if not args.subject:
        parser.error("--subject is required")
    args.subject = check_subject_prefix(args.subject)
    if not args.password:
        args.password = os.environ.get("AGENTMAIL_PASSWORD", "")
    if not args.password:
        parser.error("password is required: --password or AGENTMAIL_PASSWORD")
    return args


def choose_sender(members, sender: str) -> str:
    if sender:
        return sender
    if not members:
        raise SystemExit("list has no members to use as the sender")
    return members[0].user_address


def send_messages(args: argparse.Namespace, sender: str, list_address: str) -> None:
    try:
        smtp = smtplib.SMTP(args.smtp_host, args.smtp_port, timeout=args.timeout)
    except OSError as err:
        raise SystemExit(f"submission {args.smtp_host}:{args.smtp_port}: {err}") from err
    try:
        smtp.ehlo()
        smtp.login(sender, args.password)
        for index in range(1, args.count + 1):
            subject = message_subject(args.subject, index, args.count)
            message = EmailMessage()
            message["From"] = sender
            message["To"] = list_address
            message["Subject"] = subject
            message.set_content(f"{args.body}\n{subject}\n")
            refused = smtp.send_message(message)
            if refused:
                raise SystemExit(f"submission refused {subject}: {refused}")
            print(
                f"sent {index}/{args.count} subject={subject} from={sender} to={list_address}",
                flush=True,
            )
        smtp.quit()
    except smtplib.SMTPException as err:
        raise SystemExit(f"submission failed: {err}") from err
    finally:
        try:
            smtp.close()
        except smtplib.SMTPException:
            pass


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    listed, members = load_list(args.cli, args.list_id, args.timeout)
    if not members:
        raise SystemExit(f"list {args.list_id} has no members")
    sender = choose_sender(members, args.sender)
    print(
        f"list id={listed.id} address={listed.address} members={len(members)} "
        f"sender={sender} count={args.count} subject_prefix={args.subject}",
        flush=True,
    )
    send_messages(args, sender, listed.address)
    print(f"sent={args.count}", flush=True)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except SystemExit as err:
        if err.code not in (0, None) and not isinstance(err.code, int):
            print(err.code, file=sys.stderr)
            sys.exit(1)
        raise
