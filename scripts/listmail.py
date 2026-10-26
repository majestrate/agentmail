"""Shared helpers for the list mail scripts.

Those scripts call the agentmail CLI. They do not speak the directory
protobuf API. Run them in a mesh pod so the sidecar can authenticate the
CLI and submission.
"""

from __future__ import annotations

import re
import subprocess
from dataclasses import dataclass

LIST_LINE = re.compile(
    r"^id=(?P<id>\d+) address=(?P<address>\S+) "
    r"display_name=(?P<display_name>.*) members=(?P<members>\d+) "
    r"created=(?P<created>\S*) updated=(?P<updated>\S*)$"
)
MEMBER_LINE = re.compile(
    r"^list=(?P<list_id>\d+) user=(?P<user_id>\d+) "
    r"list_address=(?P<list_address>\S+) user_address=(?P<user_address>\S+) "
    r"display_name=(?P<display_name>.*) created=(?P<created>\S*)$"
)
SUBJECT_PREFIX = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._+-]{0,80}$")


@dataclass(frozen=True)
class DistributionList:
    id: int
    address: str
    display_name: str
    members: int


@dataclass(frozen=True)
class Member:
    list_id: int
    user_id: int
    list_address: str
    user_address: str
    display_name: str


def run_cli(cli: str, args: list[str], timeout: float) -> str:
    """Run one agentmail CLI command and return its stdout."""
    try:
        completed = subprocess.run(
            [cli, *args],
            check=False,
            capture_output=True,
            text=True,
            timeout=timeout,
        )
    except FileNotFoundError as err:
        raise SystemExit(f"agentmail CLI not found at {cli}") from err
    except subprocess.TimeoutExpired as err:
        raise SystemExit(f"agentmail {' '.join(args)} timed out") from err
    if completed.returncode != 0:
        detail = (completed.stderr or completed.stdout).strip()
        raise SystemExit(detail or f"agentmail exited {completed.returncode}")
    return completed.stdout


def parse_list(text: str) -> DistributionList:
    lines = [line.strip() for line in text.splitlines() if line.strip()]
    if len(lines) != 1:
        raise SystemExit(f"expected one list line, got {len(lines)}")
    match = LIST_LINE.fullmatch(lines[0])
    if match is None:
        raise SystemExit(f"unexpected list line: {lines[0]}")
    return DistributionList(
        id=int(match.group("id")),
        address=match.group("address"),
        display_name=match.group("display_name"),
        members=int(match.group("members")),
    )


def parse_members(text: str) -> list[Member]:
    members: list[Member] = []
    for raw in text.splitlines():
        line = raw.strip()
        if not line:
            continue
        match = MEMBER_LINE.fullmatch(line)
        if match is None:
            raise SystemExit(f"unexpected member line: {line}")
        members.append(
            Member(
                list_id=int(match.group("list_id")),
                user_id=int(match.group("user_id")),
                list_address=match.group("list_address"),
                user_address=match.group("user_address"),
                display_name=match.group("display_name"),
            )
        )
    return members


def load_list(cli: str, list_id: int, timeout: float) -> tuple[DistributionList, list[Member]]:
    listed = parse_list(run_cli(cli, ["lists", "get", "-id", str(list_id)], timeout))
    members = parse_members(run_cli(cli, ["members", "ls", "-list", str(list_id)], timeout))
    if listed.id != list_id:
        raise SystemExit(f"list id {list_id} came back as {listed.id}")
    for member in members:
        if member.list_id != list_id or member.list_address != listed.address:
            raise SystemExit(f"member does not belong to list {list_id}: {member.user_address}")
    if len(members) != listed.members:
        raise SystemExit(
            f"list {list_id} reports {listed.members} members but members ls returned {len(members)}"
        )
    return listed, members


def check_subject_prefix(prefix: str) -> str:
    if SUBJECT_PREFIX.fullmatch(prefix) is None:
        raise SystemExit(
            "subject prefix must be 1-81 letters, digits, dots, underscores, pluses, or hyphens"
        )
    return prefix


def subject_width(count: int) -> int:
    return max(2, len(str(count)))


def message_subject(prefix: str, index: int, count: int) -> str:
    return f"{prefix}-{index:0{subject_width(count)}d}"


def message_subjects(prefix: str, count: int) -> list[str]:
    return [message_subject(prefix, index, count) for index in range(1, count + 1)]


def _self_check() -> None:
    listed = parse_list(
        "id=2 address=probe.list@example.test display_name=Probe List "
        "members=2 created=2026-10-26T22:19:40Z updated=2026-10-26T22:19:40Z\n"
    )
    assert listed.id == 2
    assert listed.display_name == "Probe List"
    assert listed.members == 2
    members = parse_members(
        "list=2 user=9 list_address=probe.list@example.test "
        "user_address=probe01@example.test display_name=Probe 01 "
        "created=2026-10-26T22:19:40Z\n"
        "list=2 user=10 list_address=probe.list@example.test "
        "user_address=probe02@example.test display_name=Probe 02 "
        "created=2026-10-26T22:19:40Z\n"
    )
    assert [member.user_address for member in members] == [
        "probe01@example.test",
        "probe02@example.test",
    ]
    assert message_subjects("batch", 20)[0] == "batch-01"
    assert message_subjects("batch", 20)[-1] == "batch-20"
    assert message_subject("batch", 100, 100) == "batch-100"
    try:
        check_subject_prefix("has space")
    except SystemExit:
        pass
    else:
        raise AssertionError("space in subject prefix was accepted")


if __name__ == "__main__":
    _self_check()
