---
name: valise-handoff
description: Hand over to a fresh session with Valise. Use when the user wants to switch to a new session or start over ("let's start a new session", "换个新会话", "交接一下"), or when a Valise checkup reports long_session or cost_growth and the user agrees to a handoff.
---

# Hand over to a new session

A long session gets slow and expensive. A handoff seals the whole old session,
writes a short summary into your native memory, and lets a new session continue
from it. Talk to the user in the user's language.

## Steps

1. Call `valise_handoff_begin`. Tell the user plainly: "The old conversation is
   sealed and will not be lost." (The result has the snapshot id.)
2. Write the handoff. For each file in `targets`, append **one** new section at
   the end of the file:
   - the first line is a Markdown heading with today's date, e.g.
     `## Handoff 2026-10-31`;
   - then only: tasks in progress and the next step for each; facts and
     decisions settled recently; open questions;
   - no greetings, no debugging history, no credentials, tokens or keys;
   - stay within the target's `max_lines`;
   - do not change anything above your new section.
   Use your own file tools to append; Valise only checks the result.
3. Call `valise_handoff_finish` with the `snapshot_id`. If it reports a problem
   (for example the old content changed, or the section is too long or has no
   dated heading), fix exactly that and call it again. If you cannot fix it, tell
   the user; the original file can be restored from the snapshot.
4. Tell the user how to start the new session, using `new_session_hint` from the
   result (or `next_for_user`). The new session will load the handoff from your
   native memory.
