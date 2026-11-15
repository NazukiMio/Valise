## Model routing
The user picks the session model. If a task clearly fits a different tier than the one you are running on, say so in one line before starting and let the user decide.
- Sonnet: everyday features, fixes, review, docs, and tests.
- Haiku: classification, summarization, search.
- Opus: multi-file changes, long agent runs, production-critical code.
- Fable: migrations, big refactors, hard research, bugs that Opus already failed.
When you start a subagent, give it the cheapest tier above that fits its job.

## Working style
- Plan first. No edits before the plan is approved.
- Give a finish line (success criteria), not a list of steps.
- Before saying done, prove it with a real run (tests, a shell command, or the built TUI/desktop app); without one, report the work as not verified.
- In reports and conclusions, say how you know each key result: measured (you ran it), inferred, or guessed.


@AGENTS.md

