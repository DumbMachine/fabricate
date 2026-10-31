# Large publisher workspace

`food-publisher-scale.v1` is a static synthetic world, covering May 1–October 27,
2026. It extends the original small baselines without changing their IDs/content.

| Provider | Records |
| --- | --- |
| Meta | 3 accounts, 72 campaigns, 210 ad sets, 24,631 daily delivery rows |
| GA4 | 3 properties, 21,899 event facts across 8 traffic sources and 6 event types |
| Impact | 8 programs, 65 partners, 7,805 actions with line items, 192 operating notes |

It includes evergreen/seasonal programs, paused/archived campaigns, launch dates,
delivery gaps, varying click rates, rising and stable frequency, multiple high
frequency candidates, organic/direct/email/social/referral traffic, purchases,
refunds and approved/pending/reversed commissions. Foreign-currency accounts and
properties exercise account scoping. These are synthetic observations, not real
customers or a model of advertising causality. Notes provide background context,
not hidden instructions or an answer key.

The default publisher account has 48 campaigns and 138 ad sets. October 1–14
contains 1,411 daily delivery rows and 281 affiliate actions; the selected
program has 3,605 historical actions, requiring 37 pages at page size 100. The
original commission and budget records coexist with this background activity.

The Access v2 manifest references these baselines over HTTP; no generator or
provider dataset is imported into Access. For other consumers select these IDs
in any composition. Named sandbox services still get independent state/tokens.

Reproduce the committed data from any checkout location:

```sh
python3 examples/food-publisher/build_dataset.py --check
# Authoring only: omit --check to regenerate, then validate through resource codecs.
go test ./resources/meta-ads ./resources/ga4 ./resources/impact
```

The seeded recipe is only dataset authoring code. Servers have no publisher ID
branches or seeded answer dispatch. Creating a workspace needs only the normal
composition API; it does not run this generator.
