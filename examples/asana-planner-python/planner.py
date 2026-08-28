#!/usr/bin/env python3
"""Print the Acme Checkout reliability board. Run under `fab run acme-asana`."""

from __future__ import annotations

import json
import os
import ssl
import urllib.error
import urllib.request

USAGE = """\
asana-planner: run this app under Fabricate.

  fab run acme-asana -- python3 planner.py
  fab run acme-asana --proxy -- python3 planner.py
"""

PROJECT_GID = "proj-checkout"


def env(*names: str) -> str | None:
    for name in names:
        value = os.environ.get(name)
        if value:
            return value
    return None


def using_proxy() -> bool:
    return bool(env("HTTPS_PROXY", "https_proxy"))


def asana_base() -> tuple[str, str | None]:
    if using_proxy():
        return "https://app.asana.com/api/1.0", None
    url = env("FAB_ASANA_URL", "FAB_BOARD_URL")
    token = env("FAB_ASANA_TOKEN", "FAB_BOARD_TOKEN")
    if not url or not token:
        raise SystemExit(USAGE)
    return url.rstrip("/"), token


def ssl_context(url: str) -> ssl.SSLContext | None:
    if not url.startswith("https://"):
        return None
    ctx = ssl.create_default_context()
    ca = env("SSL_CERT_FILE", "REQUESTS_CA_BUNDLE")
    if ca:
        ctx.load_verify_locations(ca)
    return ctx


def get_json(url: str, token: str | None) -> object:
    headers = {"Accept": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    request = urllib.request.Request(url, headers=headers)
    try:
        with urllib.request.urlopen(request, context=ssl_context(url), timeout=30) as response:
            return json.load(response)
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", "replace")
        raise SystemExit(f"asana-planner: {exc.code} {url}\n{detail}") from exc


def unwrap(body: object) -> object:
    if isinstance(body, dict) and "data" in body:
        return body["data"]
    return body


def main() -> None:
    base, token = asana_base()
    me = unwrap(get_json(f"{base}/users/me", token))
    project = unwrap(get_json(f"{base}/projects/{PROJECT_GID}", token))
    listed = unwrap(get_json(f"{base}/projects/{PROJECT_GID}/tasks", token)) or []

    who = me.get("name") if isinstance(me, dict) else "unknown"
    board = project.get("name") if isinstance(project, dict) else PROJECT_GID
    print(f"{board}  ·  as {who}")
    print()

    if not listed:
        print("  (no tasks)")
        return

    open_tasks = []
    done_tasks = []
    for item in listed:
        gid = item.get("gid")
        task = unwrap(get_json(f"{base}/tasks/{gid}", token))
        bucket = done_tasks if task.get("completed") else open_tasks
        bucket.append(task)

    def show(title: str, tasks: list) -> None:
        print(title)
        if not tasks:
            print("  (none)")
            print()
            return
        for task in tasks:
            name = task.get("name") or "(untitled)"
            assignee = (task.get("assignee") or {}).get("name") or "unassigned"
            due = task.get("due_on") or task.get("dueOn") or ""
            line = f"  {name}  ·  {assignee}"
            if due:
                line += f"  ·  due {due}"
            print(line)
            notes = (task.get("notes") or "").strip()
            if notes:
                print(f"    {notes.splitlines()[0]}")
        print()

    show("Open", open_tasks)
    show("Done", done_tasks)


if __name__ == "__main__":
    main()
