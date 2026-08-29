#!/usr/bin/env python3
"""Build a case file for INV-4812 across Gmail, Intercom, Asana, and HubSpot."""

from __future__ import annotations

import json
import os
import ssl
import urllib.error
import urllib.parse
import urllib.request

USAGE = """\
support-desk: run this app under Fabricate.

  fab run acme-support-desk -- python3 casefile.py
  fab run acme-support-desk --proxy -- python3 casefile.py
"""

CASE = "INV-4812"
INTERCOM_CONVERSATION = "101"
ASANA_TASK = "task-double-charge"
HUBSPOT_DEAL = "301"


def env(*names: str) -> str | None:
    for name in names:
        value = os.environ.get(name)
        if value:
            return value
    return None


def using_proxy() -> bool:
    return bool(env("HTTPS_PROXY", "https_proxy"))


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
        raise SystemExit(f"support-desk: {exc.code} {url}\n{detail}") from exc


def service(url_names: tuple[str, ...], token_names: tuple[str, ...], proxy_base: str, direct_suffix: str = "") -> tuple[str, str | None]:
    if using_proxy():
        return proxy_base.rstrip("/"), None
    url = env(*url_names)
    token = env(*token_names)
    if not url or not token:
        raise SystemExit(USAGE)
    return url.rstrip("/") + direct_suffix, token


def header(payload: dict, name: str) -> str:
    for item in payload.get("payload", {}).get("headers", []) or []:
        if str(item.get("name", "")).lower() == name.lower():
            return str(item.get("value") or "")
    return ""


def unwrap(body: object) -> object:
    if isinstance(body, dict) and "data" in body:
        return body["data"]
    return body


def section(title: str) -> None:
    print(title)
    print("-" * len(title))


def main() -> None:
    gmail_base, gmail_token = service(
        ("FAB_SUPPORT_MAIL_URL", "FAB_GMAIL_URL"),
        ("FAB_SUPPORT_MAIL_TOKEN", "FAB_GMAIL_TOKEN"),
        "https://gmail.googleapis.com/gmail/v1",
        "/gmail/v1",
    )
    intercom_base, intercom_token = service(
        ("FAB_INBOX_URL", "FAB_INTERCOM_URL"),
        ("FAB_INBOX_TOKEN", "FAB_INTERCOM_TOKEN"),
        "https://api.intercom.io",
    )
    asana_base, asana_token = service(
        ("FAB_BOARD_URL", "FAB_ASANA_URL"),
        ("FAB_BOARD_TOKEN", "FAB_ASANA_TOKEN"),
        "https://app.asana.com/api/1.0",
    )
    hubspot_base, hubspot_token = service(
        ("FAB_CRM_URL", "FAB_HUBSPOT_URL"),
        ("FAB_CRM_TOKEN", "FAB_HUBSPOT_TOKEN"),
        "https://api.hubapi.com",
    )

    print(f"Case file  {CASE}")
    print()

    listed = get_json(
        f"{gmail_base}/users/me/messages?{urllib.parse.urlencode({'q': CASE, 'maxResults': '5'})}",
        gmail_token,
    )
    messages = listed.get("messages") or []
    section("Gmail")
    if not messages:
        print("  (no messages)")
    for item in messages:
        message = get_json(f"{gmail_base}/users/me/messages/{item['id']}", gmail_token)
        subject = header(message, "Subject") or "(no subject)"
        sender = header(message, "From") or "(unknown)"
        print(f"  {subject}")
        print(f"    {sender}")
        snippet = (message.get("snippet") or "").strip()
        if snippet:
            print(f"    {snippet}")
    print()

    conversation = get_json(f"{intercom_base}/conversations/{INTERCOM_CONVERSATION}", intercom_token)
    contacts = ((conversation.get("contacts") or {}).get("contacts") or [])
    contact_id = (contacts[0] or {}).get("id") if contacts else None
    contact = get_json(f"{intercom_base}/contacts/{contact_id}", intercom_token) if contact_id else {}
    section("Intercom")
    print(f"  {conversation.get('title') or '(no title)'}  ·  {conversation.get('state') or '?'}")
    who = contact.get("name") or contact_id or "unknown"
    email = contact.get("email")
    print(f"    {who}" + (f"  <{email}>" if email else ""))
    print()

    task = unwrap(get_json(f"{asana_base}/tasks/{ASANA_TASK}", asana_token))
    section("Asana")
    assignee = (task.get("assignee") or {}).get("name") or "unassigned"
    status = "done" if task.get("completed") else "open"
    print(f"  {task.get('name')}  ·  {status}  ·  {assignee}")
    notes = (task.get("notes") or "").strip()
    if notes:
        print(f"    {notes.splitlines()[0]}")
    print()

    deal = get_json(f"{hubspot_base}/crm/v3/objects/deals/{HUBSPOT_DEAL}", hubspot_token)
    props = deal.get("properties") or {}
    section("HubSpot")
    print(f"  {props.get('dealname') or deal.get('id')}  ·  {props.get('dealstage')}")
    extras = [props.get("invoice"), props.get("amount") and f"${props['amount']}"]
    extras = [item for item in extras if item]
    if extras:
        print(f"    {'  ·  '.join(extras)}")
    if props.get("description"):
        print(f"    {props['description']}")
    print()


if __name__ == "__main__":
    main()
