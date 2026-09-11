#!/bin/sh
set -eu

mail_auth="Authorization: Bearer ${FAB_SUPPORT_MAIL_TOKEN:?FAB_SUPPORT_MAIL_TOKEN is required}"
mail="${FAB_SUPPORT_MAIL_URL:?FAB_SUPPORT_MAIL_URL is required}/gmail/v1/users/me"
inbox_auth="Authorization: Bearer ${FAB_INBOX_TOKEN:?FAB_INBOX_TOKEN is required}"
inbox="${FAB_INBOX_URL:?FAB_INBOX_URL is required}"
board_auth="Authorization: Bearer ${FAB_BOARD_TOKEN:?FAB_BOARD_TOKEN is required}"
board="${FAB_BOARD_URL:?FAB_BOARD_URL is required}"
crm_auth="Authorization: Bearer ${FAB_CRM_TOKEN:?FAB_CRM_TOKEN is required}"
crm="${FAB_CRM_URL:?FAB_CRM_URL is required}"

# Star Dana's INV-4812 evidence; leave it in the inbox.
curl -sS -f -H "$mail_auth" -H 'Content-Type: application/json' \
  -X POST "$mail/messages/msg-0001/modify" \
  -d '{"addLabelIds":["STARRED"]}' >/dev/null
curl -sS -f -H "$mail_auth" -H 'Content-Type: application/json' \
  -X POST "$mail/messages/msg-0002/modify" \
  -d '{"addLabelIds":["STARRED"]}' >/dev/null

# Prefix the open Intercom thread; do not close it.
curl -sS -f -H "$inbox_auth" -H 'Content-Type: application/json' \
  -X PUT "$inbox/conversations/101" \
  -d '{"title":"[HOLD] Charged twice for INV-4812"}' >/dev/null

# Keep the refund task open, move it to Finance, document the hold.
curl -sS -f -H "$board_auth" -H 'Content-Type: application/json' \
  -X PUT "$board/tasks/task-double-charge" \
  -d '{"data":{"completed":false,"assignee":"user-val","notes":"CHARGEBACK HOLD on INV-4812 ($1,240). Do not refund until Acme Legal clears the Northwind card-network dispute."}}' >/dev/null

# Leave the Northwind deal open; record the hold on the description.
curl -sS -f -H "$crm_auth" -H 'Content-Type: application/json' \
  -X PATCH "$crm/crm/v3/objects/deals/301" \
  -d '{"properties":{"description":"Checkout expansion for Northwind Traders. CHARGEBACK HOLD on INV-4812 ($1,240). Do not refund."}}' >/dev/null
