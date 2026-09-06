#!/usr/bin/env python3
"""List the Acme Gmail support inbox. Run under `fab run acme-gmail`."""

from __future__ import annotations

import json
import os
import ssl
import sys
import urllib.error
import urllib.parse
import urllib.request

USAGE = """\
gmail-inbox: run this app under Fabricate.

  fab run acme-gmail -- python3 inbox.py
  fab run acme-gmail --proxy -- python3 inbox.py

Optional search: python3 inbox.py INV-4812
"""


def env(*names: str) -> str | None:
    for name in names:
        value = os.environ.get(name)
        if value:
            return value
    return None


def using_proxy() -> bool:
    return bool(env("HTTPS_PROXY", "https_proxy"))


def gmail_base() -> tuple[str, str | None]:
    if using_proxy():
        return "https://gmail.googleapis.com/gmail/v1", None
    url = env("FAB_SUPPORT_MAIL_URL", "FAB_GMAIL_URL")
    token = env("FAB_SUPPORT_MAIL_TOKEN", "FAB_GMAIL_TOKEN")
    if not url or not token:
        raise SystemExit(USAGE)
    return f"{url.rstrip('/')}/gmail/v1", token


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
        raise SystemExit(f"gmail-inbox: {exc.code} {url}\n{detail}") from exc


def header(payload: dict, name: str) -> str:
    for item in payload.get("payload", {}).get("headers", []) or []:
        if str(item.get("name", "")).lower() == name.lower():
            return str(item.get("value") or "")
    return ""


def main() -> None:
    base, token = gmail_base()
    query = " ".join(sys.argv[1:]).strip()
    profile = get_json(f"{base}/users/me/profile", token)
    params = {"userId": "me", "maxResults": "8"}
    if query:
        params["q"] = query
    listed = get_json(f"{base}/users/me/messages?{urllib.parse.urlencode(params)}", token)

    email = profile.get("emailAddress", "unknown")
    total = profile.get("messagesTotal", "?")
    estimate = listed.get("resultSizeEstimate", 0)
    messages = listed.get("messages") or []
    title = f"{email}  ·  {total} messages"
    if query:
        title += f"  ·  search {query!r} ({estimate})"
    print(title)
    print()
    if not messages:
        print("  (no messages)")
        return
    for item in messages:
        message = get_json(f"{base}/users/me/messages/{item['id']}", token)
        subject = header(message, "Subject") or "(no subject)"
        sender = header(message, "From") or "(unknown sender)"
        snippet = (message.get("snippet") or "").strip()
        print(f"  {subject}")
        print(f"    {sender}")
        if snippet:
            print(f"    {snippet}")
        print()


if __name__ == "__main__":
    main()
