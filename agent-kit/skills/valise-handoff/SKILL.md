---
name: valise-handoff
description: Hand over to a fresh session with Valise. Use when the user wants to switch to a new session or start over ("let's start a new session", "换个新会话", "交接一下"), or when a Valise checkup reports long_session or cost_growth and the user agrees to a handoff.
---

# Hand over to a new session

A long session gets slow and expensive. A handoff backs up the full current session
(`soul` tier, rolling `kind: auto` in the current date period), appends a short
summary into your native memory, and lets a new session pick up cleanly.

Always talk to the user in the user's language.

## Steps

1. Call `valise_handoff_begin`. It creates a rolling `soul`-tier backup first and
   returns `snapshot_id`, current session stats, `targets`, and `new_session_hint`.
   Tell the user plainly: `"The old conversation is backed up (s_…) and will not
   be lost."`
2. Write the handoff. For each file in `targets`, append **one** new section at
   the end of the file:
   - the first non-empty line must be a Markdown heading containing today's
     `YYYY-MM-DD` date, e.g. `## Handoff 2026-11-19`;
   - include only: tasks in progress with each next step, recently settled facts
     and decisions, and open questions;
   - never include greetings, debugging noise, API keys, tokens, or passwords;
   - stay within `max_lines` and **do not edit anything above the new section**.
   Use your normal file tools to append; Valise validates the diff afterward.
3. Call `valise_handoff_finish` with `snapshot_id`. If validation reports an issue
   (e.g. earlier lines changed, missing dated heading, or `max_lines` exceeded),
   fix only that section and call `valise_handoff_finish` again; if you cannot fix
   it, tell the user they can restore the file from `snapshot_id`.
4. Give the user the exact steps to start the new session (`new_session_hint` or
   `next_for_user`), noting the new session will load the handoff automatically.
