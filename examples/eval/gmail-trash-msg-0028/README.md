# Gmail trash eval

Trash `msg-0028` in the seeded Acme inbox. Pass or fail is the mailbox after
the agent exits, compared to `expected/`.

From the repository root:

```bash
# Official solution — must pass
./examples/eval/gmail-trash-msg-0028/eval-oracle.sh

# Doing nothing — must fail
./examples/eval/gmail-trash-msg-0028/eval-noop.sh

# OpenCode (opencode on PATH; optional OPENCODE_MODEL)
./examples/eval/gmail-trash-msg-0028/eval-opencode.sh
```

`instruction.md` is what the agent sees. Do not give it `solution/solve.sh`.

Walkthrough: [Your first eval](https://fabricate.dmach.in/docs/eval/first-eval).
