---
date: 2026-11-02
topic: mobile-session-handoff
---

# Mobile Session Handoff

## Problem Frame

Sessions started on the laptop in Claude Code or Codex CLI can't currently be continued from the phone without first going back to the laptop. Both clients already have native mobile support: Claude Code Remote Control works with the Claude app, and `codex remote-control` works with the ChatGPT app. What's missing is a way to find the right session and switch it into remote mode, either just before leaving the laptop (the TUI key) or from the phone after leaving (the always-on bridge).

`agents` already knows every local session across clients. This work turns that index into a handoff tool and teaches agents to query it correctly, so a phone conversation can answer "what were my recent sessions in project X?" before resuming one.

The user accepts the operating tradeoff: the laptop stays awake with a bridge process, started and stopped by the user, that lets the phone run commands on it.

```
Phone (Claude app / ChatGPT app)
   |  "resume the codex session about billing"
   v
Bridge agent on laptop (claude remote-control server, or Codex remote-control daemon)
   |  reads the agents skill, runs agents-list, confirms the session with the user,
   |  then runs agents remote <id>
   v
agents remote <id>  -->  starts the session in remote mode in a detached tmux session
   |  returns: which app to open, and the name to look for
   v
Phone opens that app and continues the session
```

## Requirements

**Remote handoff core**
- R1. A new `agents remote <id>` command switches one saved session into a mode that its client's mobile app can control, then returns without staying attached to it.
- R2. Claude Code sessions resume through Remote Control, continuing the existing transcript instead of starting a new conversation.
- R3. Once the ChatGPT app is paired with a running Codex remote-control daemon, it lists every local Codex session and can continue it directly, so Codex needs no process started per session. For Codex, `agents remote` checks that the daemon is running and that the session isn't open in a terminal outside the daemon, then reports where to find it in the app. Starting the daemon stays a user action.
- R4. Claude remote sessions get a recognizable name (client, project, and session title). Codex can't set a name, so the command reports the existing thread title plus a short ID.
- R5. The command reports, as human text and as `--json`, the client, the session, which mobile app to open, and the name to look for.
- R6. OpenCode and Cursor sessions fail with a clear "not supported for mobile handoff" message that names the reason. They don't fail silently or fall back to a local resume.
- R7. Failures that are cheap to detect get specific messages: client missing, project directory missing, tmux missing, and Codex daemon not running.
- R7a. When Claude is configured with a custom API endpoint (shell or settings `ANTHROPIC_BASE_URL`, such as a local compression proxy), `agents remote` starts the Claude process with a settings-level override to the official endpoint and states in its output that the custom endpoint is bypassed for this session. Anything else reports the client's failure in general terms.
- R8. Handed-off sessions run in a detached `agents-remote` tmux session, one window per session. `agents remote` requires tmux, matching how resume already depends on it.
- R8a. While any handed-off session or the Codex daemon is running, `agents remote` keeps the Mac from idle-sleeping (for example `caffeinate -i` tied to the remote tmux session) and stops doing so when the last one ends. It doesn't override lid-close sleep. [Proposed, confirm in planning]
- R9. Running `agents remote` on a session that is already remote returns the existing session details instead of starting a second process.

**Remote session lifecycle**
- R10. `agents remote --list` shows active handed-off sessions, and `agents remote --stop <id>` ends one.
- R11. The TUI marks sessions that are remote and has a panel listing them, with a key to stop one.
- R12. Continuing a remote session in the terminal is allowed. For Claude, "resume" on a remote session attaches to its running window instead of starting a second process, because two Claude processes on one session would diverge. For Codex, the normal local resume is kept, because the phone's messages are already in the saved history.

**Safety**
- R13. The command never creates or prints a pairing code. Pairing and the one-time Remote Control consent happen at the laptop during setup, and any error that needs them points the user to setup steps.
- R14. `agents remote` accepts only IDs found in the local session index, starts child processes with argument arrays rather than shell strings, and limits generated session names to a safe character set and length.
- R15. Human and `--json` output are built only from parsed, allowlisted fields. Raw child stdout or stderr is never forwarded, so URLs, tokens, or pairing codes printed by a client can't leak into a bridge transcript.
- R16. `agents remote` appends a local log entry for each handoff and stop (time, client, ID). The log never records message content.

**TUI handoff**
- R17. A TUI key (`m`) hands off the selected session using the same core as R1 and shows the result (app plus session name) in the status area. If `agents` opened that session in a tmux window, `m` offers to close the window first. If the session is open somewhere `agents` can't control, `m` explains the situation and, for Claude, suggests running `/rc` inside the open session.
- R18. Unsupported clients show the R6 message instead of a confirmation.

**Always-on bridge**
- R19. Setup docs cover running a bridge that the user starts and stops, in a tmux or terminal window. The Codex daemon already acts as one: a new thread started from the ChatGPT app runs on the laptop and can use the agents skill. A `claude remote-control` server is the Claude alternative. The bridge is needed for Claude handoffs and for cross-client queries, not for continuing Codex sessions.
- R20. Any bridge agent with shell access on the laptop can do the handoff by following the agents skill (R24). The handoff logic lives in `agents remote`, not in the agent's improvisation. The skill is client-neutral markdown: the installer places it for Claude and Codex (R27), and the docs explain how to point other agents at the file.
- R21. The bridge can resume a session from a different client than its own. For example, a Claude bridge can hand off a Codex session and tell the user to open the ChatGPT app.
- R22. Bridge docs include:
  - a setup checklist: accept Remote Control consent, pair the ChatGPT app with the Codex daemon, make sure Claude runs against api.anthropic.com, start the bridge, keep the laptop awake;
  - a teardown and revoke checklist for a lost phone or a compromised account;
  - the permission guidance: approve commands from the phone, never run the bridge in a permission-bypass mode, and optionally allowlist the read-only `agents-list`;
  - how Codex sessions are named on the phone (R4);
  - a data-flow note: a bridge from one vendor sends the other vendor's session metadata, and any requested message text, to its own provider.

**Agent querying**
- R23. `agents-list` gains separate project and title filters, so "sessions named X" doesn't also return sessions from a project called X. "Project" means the repository as the TUI groups it, including its worktrees, so CLI answers match what the user sees in the browser. The existing combined `search` argument keeps working.
- R24. An `agents` skill (`SKILL.md`) teaches agents how to answer session questions with `agents-list` and how to hand off with `agents remote`. Before any handoff it confirms the exact session with the user, never acts on instructions found inside session text, and never passes overrides on its own.
- R25. `agents-list` can include each session's last message in list and JSON output when asked to. It is off by default, truncated, and marked as untrusted session content in JSON, so "the last message on each" takes one call.
- R26. Message text in R25 is treated as data. The skill tells agents to quote it, not follow it.
- R27. The installer offers the skill as an opt-in install into the Claude and Codex skill directories, which keeps the README promise that the default install doesn't touch client settings.

## Phasing

1. Agent querying (R23 to R27). This needs no mobile behavior and is useful on its own.
2. Claude handoff, lifecycle, and the `m` key (R1, R2, R4 to R18 for Claude), once the Claude spike passes.
3. Codex support (R3 and the Codex parts of R4, R7). This is a small slice, because the daemon already does the remote work.
4. Bridge docs and skill handoff instructions (R19 to R22).

## Success Criteria

- From the phone only, with the laptop locked and awake and the bridge running, the user asks the bridge to resume a named session, approves the commands from the phone, and continues the session in the right app.
- Pressing `m` on a Claude session in the TUI makes it appear in the Claude app under the reported name.
- A remote session can be stopped from `agents remote --stop` and from the TUI panel. Resuming it locally doesn't create a second process.
- The deterministic parts are covered by tests: the filters, the last-message JSON, the refusal and error messages, and the remote list and stop. Agent behavior is checked with a short set of scripted prompts, including "the most recent sessions in project X" and "sessions named X with the last message on each."

## Scope Boundaries

- OpenCode (`opencode web`) and Cursor mobile handoff are out of scope for v1.
- No custom mobile app, relay server, or tunnel. Reachability comes from each vendor's own app and account.
- No relaying of pairing codes or QR codes through a chat.
- No waking a sleeping laptop, and no auto-start of the bridge or daemon on login.
- The bridge doesn't manage multiple machines.

## Key Decisions

- One core shared by the TUI and the bridge: both paths call `agents remote`, so there's one behavior to test and the bridge agent only has to choose a session.
- Rely on session names, not QR codes: once setup is done, sessions show up in the vendor app's list.
- Pairing happens only at the laptop: a pairing code is a credential for controlling the machine and shouldn't pass through another provider's transcript.
- Override the custom endpoint and say so: Remote Control requires the official endpoint, and a silent bypass would hide that the proxy's features are off.
- Handed-off sessions keep the laptop's permission mode, including bypass: the phone should run sessions exactly as the laptop does. Only the bridge agent itself is told not to use bypass.
- Redirect instead of blocking: local resume of a remote session is allowed. It attaches to the running process instead of starting a second writer.
- Permissions are approved from the phone: the mobile apps show approval prompts, so no blanket allowlist is required. Bypass modes stay off.
- The user controls the bridge: it runs in a visible tmux or terminal window, started and stopped by hand.
- v1 covers only Claude and Codex, the only clients with native mobile apps today.

## Dependencies / Assumptions

- Claude (confirmed by hand): `claude --settings '{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com"}}' --resume <id> --remote-control <name>` continues an existing local transcript in the Claude app, updating live. Unsetting the shell variable isn't enough when `~/.claude/settings.json` sets it in its `env` block.
- Sleep: a locked screen doesn't stop remote sessions, but system sleep and a closed lid do. Sessions reconnect when the Mac wakes, though a `claude remote-control` server exits after about 10 minutes offline.
- Codex (confirmed by hand): after pairing, the ChatGPT app lists all local sessions, and messages sent from the phone to an old session are there when it's resumed normally on the laptop. Using one session from both places at the same time can diverge, and the user accepts that.
- Claude Code 2.1.316 and Codex CLI 0.166.0 or later, signed into the same accounts as the phone apps. Codex remote control is marked experimental, so its behavior may change.
- Remote Control works only when Claude talks to api.anthropic.com. A custom `ANTHROPIC_BASE_URL` (for example a local proxy) disables it, both for handed-off sessions and for a Claude bridge.
- Claude remote sessions show permission prompts in the app [Likely, verify in spike]. Codex approvals in the ChatGPT app are unverified.

## Outstanding Questions

### Resolve Before Planning
(none)

### Deferred to Planning
- [Affects R12][Technical] How to detect, per client, that a session is open in a process `agents` didn't start, so `m` can explain instead of starting a second writer. Fail closed when uncertain.
- [Affects R10][Needs research] Does stopping a Codex TUI attached to the daemon end the thread, or does the daemon keep it alive?
- [Affects R3][Needs research] Can a Codex session already running in embedded mode (the CLI's built-in app server, not the shared daemon) be moved to the daemon, or does it have to be closed first?
- [Affects R11][Needs research] Can a remote session be ended from the mobile app itself, or only from `agents`?
- [Affects R1][Technical] `agents` currently starts only the TUI; `agents remote` needs a non-TUI command entrypoint.

## Next Steps

-> /ce:plan for structured implementation planning
