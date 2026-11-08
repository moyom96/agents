---
title: "feat: open sessions and phone handoffs in Herdr"
type: feat
status: active
date: 2026-11-06
---

# feat: open sessions and phone handoffs in Herdr

## Goal

Let `agents` run without tmux. A new `herdr-tab` launch target opens a resumed
session in a new Herdr tab, and with that target, phone handoffs run in a
Herdr space named `agents-remote`, one tab per session. tmux targets keep
working as they do today.

## Decisions

- **Target selection.** `launch.target` gains `herdr-tab`. With no configured
  target, the default is `herdr-tab` when the browser runs inside a Herdr
  terminal (`HC_TERMINAL_ID` set), otherwise `tmux-window`. Only the tmux
  targets require `TMUX`.
- **Local open uses `herdr-app do tab.new`.** It goes into the browser's own
  space (the space whose tab holds `$HC_TERMINAL_ID`, from `herdr-app ls`), named
  `<client>:<project>`, with the resume argv. Herdr runs argv in the user's
  interactive shell, and that shell stays open after the client exits, the same
  behavior the tmux window has. The new terminal gets the labels
  `agents.kind=local` and `agents.session=<client>:<id>`, so a handoff can detect
  the session as `open_in_agents_window`.
- **Phone handoffs follow the launch target.** `herdr-tab` uses the Herdr
  backend; the tmux targets use the existing `agents-remote` tmux session.
  Sessions started under one backend are not visible to the other.
- **Remote terminals use `hc run`, not `tab.new`.** `tab.new` wraps argv in an
  interactive shell that outlives the client, which hides a failed startup.
  `hc run --bg --no-parent --no-notify` with `herdr.space` / `herdr.tab` labels
  puts the payload straight into a Herdr tab. When Claude exits, the terminal is
  `finished` with its screen intact until `hc rm`, which replaces tmux's
  `remain-on-exit`. Labels: `agents.kind=remote`, `agents.session=<key>`.
- **The space.** Reuse the `herdr.space` of any live remote terminal, else the
  local-machine space named `agents-remote` in `herdr-app ls`, else a new id.
  New spaces and tabs get names through `space.rename` / `tab.rename`, retried
  briefly while Herdr adopts the terminal. Naming is cosmetic, so it is best
  effort and never fails a handoff. Herdr may switch focus to the new tab;
  that's fine.
- **One backend interface** (`remoteBackend`) with tmux and Herdr
  implementations. It covers list, create, kill, screen + dead, local windows,
  and attach. The handoff logic in `remote*.go` stays backend-neutral.
- **New statuses** `herdr_missing` (exit 5: `hc` or `herdr-app` not on PATH) and
  `herdr_failed` (exit 5). `open_in_agents_window` wording becomes
  backend-neutral.

## Tasks

1. Settings: `herdr-tab` target, environment-aware default, settings dialog option.
2. Local open: `herdr-tab` branch in `openSession`, tmux checks only for tmux targets.
3. `remoteBackend` interface; move tmux code behind it with no behavior change.
4. Herdr backend, with tests driven by a fake `herdr-app` / `hc` runner.
5. TUI attach (Enter on a remote session selects its Herdr tab), dialog text.
6. Docs: README, `docs/mobile-handoff.md`, `skills/agents/SKILL.md` statuses.

## Verification

`go test ./...`, `go vet ./...`, plus a manual run inside Herdr: Enter opens a
tab; `agents remote <id>` creates the `agents-remote` space; `--list` / `--stop`
work.
