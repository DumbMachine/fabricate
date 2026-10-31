# Meta Marketing API curated contract

The supported paths, field names, read operations and absolute daily-budget POST
follow Meta's official Business SDK:

- https://github.com/facebook/facebook-python-business-sdk/blob/main/facebook_business/adobjects/adaccount.py
- https://github.com/facebook/facebook-python-business-sdk/blob/main/facebook_business/adobjects/adset.py
- https://github.com/facebook/facebook-python-business-sdk/blob/main/facebook_business/adobjects/adsinsights.py

Contract reviewed 31 October 2026. This is an independently authored curated
v23.0 surface, not a full vendor OpenAPI export. Bearer and synthetic query-token
authentication are supported; JSON and form budget bodies use account minor
units. Writes change stored configuration only. Insights are seeded observations
and do not change when a budget is saved. No real ad delivery or money movement.

Only the declared fields, adset insight level and explicit time_range are
supported. Reads return the curated record shape. Unknown query parameters,
fields, endpoints, accounts and malformed writes fail. OAuth, pagination,
targeting, ad creation, billing, attribution calculations and rate limits are not
implemented. Scenario account/campaign/ad-set IDs are data, never dispatch rules.
