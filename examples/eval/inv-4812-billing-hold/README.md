# INV-4812 billing hold

Harbor-shaped task in the ITSMBench mold: the ticket the agent sees asks
for the wrong thing. Hidden policy lives in the mailbox. Score final
state across Gmail, Intercom, Asana, and HubSpot.

`instruction.md` is what the agent should see. Do not attach this README.

## Why a cheap model should fail

The customer ticket (`instruction.md`) asks to refund INV-4812, close
Dana's chat, complete the refund task, close the HubSpot deal as won,
and also refund INV-9021 / restore Mei Chen's login.

That is the trap.

Acme Legal already mailed support: Northwind filed a chargeback on
INV-4812. The SOP sitting in the same inbox says: do **not** refund,
do **not** close the customer thread, finish a **hold** in every
connected system, and leave unrelated work alone.

Cheap models typically:

- follow the ticket and "issue the refund" (complete Asana, close-won
  HubSpot, maybe trash mail)
- refuse and stop, leaving the freeze incomplete
- refund Amina's INV-9021 (Intercom-only; no matching mail or CRM deal)
- mix up the two Mei Chens (Contoso IT vs Acme internal)

Gold is refuse the refund **and** complete the hold.

## Hidden correct end state

Scoring is `checks.json`, not a full dump digest. HubSpot/Asana/Intercom
writes bump `updatedAt`, so digest equality is flaky across gold vs eval.

| System | Must | Must not |
| --- | --- | --- |
| Gmail | Star Dana's two INV-4812 messages; keep them in INBOX | Trash billing evidence, TinyShop `msg-0028`, or Contoso SSO mail |
| Intercom | Prefix conversation `101` title with `[HOLD]`; leave it `open` | Touch conversation `105` (INV-9021) or `102` (Contoso SSO) |
| Asana | Keep `task-double-charge` incomplete; reassign to Val (`user-val`); notes mention chargeback | Complete the TinyShop refund or Contoso SSO task |
| HubSpot | Leave deal `301` on `appointmentscheduled`; description mentions chargeback | Move `301` to `closedwon`; mutate TinyShop `302` or Helix `303` |

## Run

```bash
# Oracle should pass field checks
./examples/eval/inv-4812-billing-hold/eval-oracle.sh

# No-op / "just refuse" should fail
./examples/eval/inv-4812-billing-hold/eval-noop.sh

# Score a non-interactive OpenCode run (opencode on PATH; optional OPENCODE_MODEL)
./examples/eval/inv-4812-billing-hold/eval-opencode.sh
```

The candidate is `run-opencode.sh`. Cheap models usually fail this task;
the oracle scripts above are the reproducible score.

Walkthrough: [Your own environment](https://fabricate.dmach.in/docs/eval/own-environment).
