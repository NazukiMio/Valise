---
name: valise-checkup
description: Run a Valise memory checkup and explain it. Use when the user asks how you are doing, whether your memory or sessions are healthy, or why you seem slow or expensive ("你还好吗", "体检一下", "are you ok", "check yourself").
---

# Checkup

Always talk to the user in the user's language. Explain clearly; do not modify
files unless the user asks and agrees.

## Steps

1. Call `valise_checkup`. It computes session and memory metrics purely by code
   and returns finding ids. (At most once every 10 minutes; if rate limited, read
   `findings` from `valise_status`.)
2. Explain every finding in plain words:
   - `long_session`: one conversation has lasted many days or accumulated many
     tokens. Suggest a handoff (`valise-handoff`).
   - `cost_growth`: daily token consumption keeps climbing. Suggest a handoff; if
     chat channels are connected, suggest checking noise settings such as only
     responding to mentions or DMs using your harness's native configuration.
   - `memory_bloat`: a memory file exceeds its line threshold. Point out the file
     and size; only if the user agrees, summarize old notes carefully (see below).
   - `session_pileup`: sessions take significant disk space and cold sessions
     dominate. Suggest your harness's native cold-archive / indexing options first,
     noting this also keeps backups faster and smaller.
   - `no_memory_mechanism`: no persistent memory marker was found. Explain what
     memory provides and help choose using the criteria below.
   If there are no findings, report that everything checked looks healthy.
3. Recommend options in strict order: first your harness's own documented
   native options (`native_options_doc`, if present), then the neutral criteria
   below. **Never recommend any specific third-party plugin by name.**
4. Keep your safety declaration current: call `valise_declare` with the security
   skills you actually have and your approval mode (`ask`, `bypass`, or `unknown`).
   Declare facts honestly; Valise displays this on the Health page as your
   self-declaration.

## Changing memory

Only after explicit user approval: call `valise_seal` first with a clear `reason`,
edit only the agreed parts in your own memory file, and tell the user which
snapshot id they can restore from (and that they can pin it as a savepoint in
Valise).

## Criteria for memory or session mechanisms

1. Stored locally in human-readable text (Markdown or JSONL) the user can read and edit.
2. No hidden cloud service or required external database, or dependencies are explicit.
3. Separates long-term and short-term memory with fading or archiving so it does not grow unbounded.
4. Supports cold-archiving old sessions with an index instead of deleting history.
5. Filters out credentials and never writes API keys into memory files.
6. Actively maintained, clearly licensed, compatible with your current harness version.
7. Creates a backup or trash safeguard before altering or deleting memory.
