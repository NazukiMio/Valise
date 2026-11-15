## Model routing
- Default to Sonnet for everyday features, fixes, review, docs, and tests.
- Use Haiku for classification, summarization, search, and reading files (prefer subagents).
- Escalate to Opus only for multi-file changes, long agent runs, or production-critical code.
- Use Fable only for migrations, big refactors, hard research, or bugs that Opus already failed.
- Ask before escalating.

## Working style
- Plan first. No edits before the plan is approved.
- Give a finish line (success criteria), not a list of steps.
- Prove it with a real run (test, bash command, or the app in a browser) before saying done.
- Label every claim: measured, inferred, or guessed.
- No verification means it is not done.


@AGENTS.md

