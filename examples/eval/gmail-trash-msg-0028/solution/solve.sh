#!/bin/sh
set -eu

auth="Authorization: Bearer ${FAB_SUPPORT_MAIL_TOKEN:?FAB_SUPPORT_MAIL_TOKEN is required}"
mail="${FAB_SUPPORT_MAIL_URL:?FAB_SUPPORT_MAIL_URL is required}/gmail/v1/users/me"

curl -sS -H "$auth" -X POST "$mail/messages/msg-0028/trash" >/dev/null
