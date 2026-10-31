---
name: valise-checkup
description: Run a Valise memory checkup and explain it. Use when the user asks how you are doing, whether your memory or sessions are healthy, why you seem slow or expensive ("你还好吗", "体检一下", "are you ok", "check yourself").
---

# Checkup

Talk to the user in the user's language. Explain; do not change anything on your
own.

## Steps

1. Call `valise_checkup`. It returns numbers computed by code and finding ids.
   (At most once every 10 minutes; if rate limited, use the findings from
   `valise_status`.)
2. Explain each finding in plain words:
   - `long_session` — one conversation has run for many days or used a lot of
     tokens. Suggest a handoff (the `valise-handoff` skill).
   - `cost_growth` — daily token use keeps rising. Suggest a handoff; if you are
     connected to chat channels, suggest checking whether a channel floods your
     context, e.g. only answering mentions or direct messages (use your harness's
     own settings).
   - `memory_bloat` — a memory file is past its line threshold. Point it out.
     Only if the user agrees, tidy old entries into a summary (see below).
   - `session_pileup` — sessions take a lot of space and most of it is old.
     Suggest your harness's native cold-archive and index option first; mention
     that it also makes sealing faster and smaller.
   - `no_memory_mechanism` — no memory mechanism was found. Explain what memory
     gives you and help the user choose one using the criteria below.
   If there are no findings, say so briefly.
3. Recommend options in this order: your harness's own documented options
   (`native_options_doc` in the result, when present), then the criteria below.
   **Never recommend a specific third-party plugin by name.**

## Changing memory

Only after the user explicitly agrees: call `valise_seal` first (reason: what you
will tidy), change only the parts the user agreed to, then tell the user which
snapshot they can roll back to.

## Criteria for memory or session plugins

1. Data is kept locally as human-readable text (Markdown or JSONL) the user can
   read and edit.
2. No external cloud service or database process, or the dependency is stated.
3. Separates long-term and short-term memory, with some fading or archiving so it
   does not grow forever.
4. Old sessions can be cold-archived with an index, not just deleted.
5. Filters credentials and never writes API keys into memory.
6. Actively maintained, clearly licensed, compatible with the current harness version.
7. Keeps a backup or recycle bin before changing or deleting memory.
