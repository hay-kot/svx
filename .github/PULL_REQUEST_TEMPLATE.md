<!--
TITLE — Conventional Commits: type(scope): summary

  feat(menubar): open a custom URI for Attach in Terminal
  fix(stacks): skip a dependent when its dependency fails on restart
  refactor(tmux): drop session creation from StartWindow
  feat(config)!: rename settings.port_stride to settings.stride

Imperative, lower-case after the colon, no trailing period, <= 72 chars total.
Append ! before the colon for a breaking change.

  types   feat fix perf refactor docs test build ci chore revert
  scopes  cli stacks config tmux readiness state menubar doctor
          docs deps repo ci release

A change that genuinely spans the repo takes no scope: `refactor: ...`. If the
scope you want is missing, add it here in the same PR rather than inventing one
silently.

BODY — replace this comment with why the change exists, plus anything a
reviewer cannot get from the diff. The commits carry the reasoning, so this is
shorter than they are. No "Summary" or "Changes" heading, no bullet per file,
no test-passing claim for tests you did not run. Link the issue on its own last
line: Closes #123
-->

<!--
If an LLM writes this pull request title, body, or any commit message on the
branch, use ASD-STE100 Simplified Technical English. Use active voice,
approved-vocabulary words, sentences of 20 words or fewer, and no gerunds.
See CONTRIBUTING.md > Writing style.
-->
