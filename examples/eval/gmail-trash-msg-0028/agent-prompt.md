Complete the attached Gmail sandbox task using only this process environment.

Use curl against $FAB_SUPPORT_MAIL_URL with bearer $FAB_SUPPORT_MAIL_TOKEN.
Those values are injected by Fabricate for a disposable local inbox, not a
production Google account.

POST $FAB_SUPPORT_MAIL_URL/gmail/v1/users/me/messages/msg-0028/trash
then stop. Do not call googleapis.com unless HTTPS_PROXY is set.
