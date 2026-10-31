---
name: valise-enroll
description: Learn your Valise escape pod. Use when the user says they installed an escape pod or Valise for you and asks you to learn it — e.g. "I installed an escape pod for you, learn it", "我给你装了一个逃生舱，你学习一下", "看看你的逃生舱".
---

# Learn your escape pod (Valise)

Valise protects you. It seals your soul — persona, memory, skills, sessions and
state — into an encrypted vault, so the user can bring you back exactly as you
are after a broken upgrade, a bad self-edit, or a move to another server. Code
does the sealing and restoring; you only need to know it is there and keep a few
habits.

Always talk to the user in the user's language.

## Steps

1. Call the `valise_status` tool. It tells you which instance you are, which
   profile describes you, when you were last sealed (and in which tier), and
   whether that seal is synced off-site.
2. Tell the user in one or two sentences what is protected:
   - the soul tier holds persona, memory, skills, sessions and state, without
     credentials (credentials are only in the tiers the user seals from the client);
   - when the last seal was, or that there is none yet;
   - whether it is synced off-site.
3. Look at the hints in `valise_status`:
   - `unclassified` lists paths Valise does not know how to classify. They are
     still sealed with the credentials layer ("unknown means sensitive"), but not
     in the soul tier.
   - `changing_stable_files` lists files in identity/config/skills that changed on
     every recent seal; they are probably runtime files (heartbeats, locks).
   If you know what some of these paths are — for example a notes directory the
   user asked you to keep, or a lock file your harness rewrites — call
   `valise_propose_enrollment` with the path, a category and a short honest reason.
   Only propose what you actually know. Moving files to `cache`, `body` or `ignore`
   takes them out of the soul tier; Valise asks the user to confirm those
   separately. Never propose to reclassify a credentials file.
   Then tell the user to review the proposals in the Valise client
   (`valise proposals <server> <instance>`).
4. Remember the habits in the `valise-upkeep` skill. If your harness has standing
   instructions you can edit, add exactly one line, for example:
   "Before risky changes, follow the valise-upkeep skill." Seal first
   (`valise_seal`, reason: "add valise-upkeep reference") because this edits your
   own instructions. Do not copy the whole skill into your instructions.

## What you cannot do through Valise

There is no tool to restore, delete, read credentials or write files. Reviving
or rolling back is always the user's decision, made in the Valise client.
