---
title: "feat: Hand off local sessions to mobile apps"
type: feat
status: completed
date: 2026-11-02
origin: docs/brainstorms/2026-11-02-mobile-session-handoff-requirements.md
---

# feat: Hand off local sessions to mobile apps

## Overview

Add an `agents remote` command, plus a TUI key that calls it, to continue Claude Code and Codex sessions from their mobile apps. The query flags added to `agents-list` and a new `agents` skill let a phone-side agent find the right session before handing it off. Claude sessions run under Remote Control in a detached tmux session. Codex needs no per-session process, because the paired remote-control daemon already exposes every local session to the ChatGPT app.

## Problem Frame

Laptop sessions can't currently be continued from the phone without going back to the laptop. Both vendor apps can drive local sessions, but nothing finds the right one and switches it into remote mode, either before leaving (TUI) or after leaving (a bridge agent reached from the phone). See origin: `docs/brainstorms/2026-11-02-mobile-session-handoff-requirements.md`.

Facts confirmed by hand during the brainstorm:
- Claude: `claude --settings '{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com"}}' --resume <id> --remote-control <name>` continues an existing transcript in the Claude app, updating live. Unsetting the shell variable isn't enough when `~/.claude/settings.json` sets it again.
- Codex: once paired with a running daemon, the ChatGPT app lists local sessions. Messages sent from the phone are in the history when the session is resumed normally.

## Requirements Trace

Grouped by unit. IDs match the origin document.

- Query: R23 (project and title filters), R25 and R26 (opt-in, untrusted session text).
- Skill and installer: R20, R24, R27.
- Handoff core: R1, R5, R6, R9, R10, R13, R14, R15, R16.
- Claude: R2, R4, R7, R7a, R8, R8a, R12 (Claude).
- Codex: R3, R4, R7, R8a, R12 (Codex).
- TUI: R11, R12, R17, R18.
- Bridge docs: R19, R21, R22.
- Success criteria: phone-only handoff, `m` shows the session in the Claude app, stopping and attaching don't create a second process, and tests plus scripted prompts cover the deterministic and agent parts.

## Scope Boundaries

- OpenCode and Cursor handoff are out of scope and get the R6 message.
- No relay, tunnel, or custom app. No pairing or QR relay through chats. No auto-start of the bridge or daemon, and no waking a sleeping Mac.
- `agents remote` doesn't start or pair the Codex daemon.
- Codex sessions have no process managed by `agents`, so `--list`, `--stop`, and the TUI remote marker cover Claude only.
- Keep-awake (R8a) is macOS-only; on Linux it is skipped silently.
- No cap on concurrent remote sessions and no extra confirmation token for non-interactive callers. The phone approval prompt is the control, per the accepted tradeoff.

## Context & Research

### Relevant Code and Patterns

- `main.go`: `main()` only starts the Bubble Tea program. Key handling lives in `model.update` (`case "enter"` calls `openSession`), and dialogs follow the `pendingDelete`/`deletePrompt` and `previewExpanded` patterns.
- `launch.go`: `resumeArgs`, `windowArgs`, `quote`, `openSession`. Resume currently requires `TMUX`/`TMUX_PANE` and runs through `shell -lic` followed by `exec shell -l`.
- `terminal.go`: `belongsToProcess` walks process ancestry. The open-elsewhere check reuses it to exclude its own ancestors.
- `sessions.go`: `readSessions` shells out to `agents-list --json -n 0`, `clean()` strips control characters, and `session.name()` falls back to "Untitled session".
- `projects.go`: `findRepository` resolves the main repository through `git rev-parse --show-toplevel --git-common-dir` and `git worktree list --porcelain -z`. The Python reader has to mirror this for R23.
- `bin/agents-list`: argparse in `main()`, the combined `search` filter over title/cwd/id, and `session_preview(client, id, home)` returning the last six messages.
- `install.py`: copies files with `install_file`, never edits client settings, and is tested in `install_test.py`.
- Tests: Go tests run the Python reader as a subprocess (`reader_test.go`), the TUI is tested through `fixtureModel()`/`press()` in `main_test.go`, and Python tests use `unittest` with temporary fixtures (`preview_reader_test.py`).
- Environment: tmux 3.8 locally. Codex 0.166.0 has `codex remote-control start|stop|pair` (with `--json`) and `codex app-server daemon version` (JSON).

### Institutional Learnings

- None. `docs/solutions/` doesn't exist.

### External References

- Claude Code Remote Control docs (https://code.claude.com/docs/en/remote-control): one remote session per interactive process, a `claude remote-control` server exits after about 10 minutes offline, and Remote Control requires api.anthropic.com and a claude.ai login.

## Key Technical Decisions

**Command surface**
- **`agents remote` lives in the Go binary, not `agents-list`.** It shares `resumeArgs`, the tmux handling, and the TUI's core (R1, R17). `main()` dispatches on its first argument, and with no arguments it still starts the TUI.
- **Command forms.** `agents remote <id> [--json] [--confirm-closed]`, `agents remote --list [--json]`, and `agents remote --stop <id> [--json]`. IDs are matched exactly against the index. If the same ID exists in two clients, the error lists both instead of adding a `--client` flag.
- **One result contract with a fixed status enum and exit codes**:

  | Status | Meaning | Exit |
  | --- | --- | --- |
  | `started` | Claude remote session confirmed connected | 0 |
  | `already_remote` | Existing remote window reused (R9) | 0 |
  | `ready` | Codex daemon with remote control is running, so continue in the app | 0 |
  | `ready_warning` | Codex is ready but the session looks open in a terminal (divergence accepted, R12) | 0 |
  | `unconfirmed` | Claude started, but no connected signal was seen within the wait | 3 |
  | `unknown_session`, `unsupported_client` | Bad ID, or OpenCode/Cursor (R6) | 2 |
  | `open_in_agents_window`, `open_elsewhere`, `maybe_open` | Claude session is or may be open locally | 4 |
  | `consent_required`, `login_required`, `api_key_auth`, `startup_failed` | Claude can't start Remote Control | 5 |
  | `daemon_not_running`, `remote_control_disabled` | Codex daemon missing, or running without remote control | 5 |
  | `client_missing`, `tmux_missing`, `directory_missing` | Prerequisite missing (R7) | 5 |

  Human output and `--json` are both rendered only from this result (R15). Every session-derived string (title, project, look_for) passes through `clean()` and a length cap before output.

**Where sessions run**
- **Remote Claude sessions run in a detached tmux session named `agents-remote`**, one window per session, on the **default tmux server**. `agents` ignores the caller's `TMUX` socket for these commands. Tests isolate with `TMUX_TMPDIR`. Each window carries `@agents-remote` set to `claude:<id>`. Windows that `openSession` opens for normal resumes get a different option, `@agents-local`. List, stop, idempotency, and attach consider only `@agents-remote` windows inside `agents-remote`.
- **Atomic creation.** The first handoff creates the session together with its first window, so the session never sits empty. Later handoffs add windows. Creation captures the window ID (`-P -F '#{window_id}'`), and the tag and options are set against that ID. A lock file in the state dir covers the whole check, create, and tag sequence, so the TUI and a bridge can't both create a window for one session.
- **`remain-on-exit` is a permanent option of `agents-remote`.** A window whose client exits stays as a dead pane, and `--list` and every handoff reap dead panes before deciding anything. That removes the race between a fast exit and setting the option. tmux 3.2 or later is required and checked.

**Launching Claude**
- **The login shell runs, but only with argv-safe arguments.** The window runs `env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u TMUX ... $SHELL -lc 'exec "$@"' agents-remote <argv...>`. Arguments arrive as positional parameters and are never interpolated (R14). The profile rebuilds `PATH` and the user's environment the way a TUI resume does, so mise and nvm installs still resolve. The list of nested-agent variables to strip is one constant, defined at implementation.
- **The endpoint override is always passed for remote Claude sessions.** It's a settings-level `env.ANTHROPIC_BASE_URL=https://api.anthropic.com`, which also blanks `ANTHROPIC_AUTH_TOKEN` and `ANTHROPIC_CUSTOM_HEADERS`, so a gateway credential is never sent to Anthropic. The notice fires when a different endpoint is detected in the process env or in the user, project, or local settings `env` blocks (R7a). Only those keys are read. If `ANTHROPIC_API_KEY` or `apiKeyHelper` is configured, the command returns `api_key_auth`, because Remote Control needs a claude.ai login.
- **Permission mode is inherited unchanged.** A handed-off session runs with exactly the user's resolved settings, including `bypassPermissions`, so the phone behaves like the laptop (user decision). `agents` passes no `--permission-mode`.
- **Keep-awake by wrapping.** On macOS, `caffeinate -i` is the first argv element inside the payload, so the assertion lasts exactly as long as the session (R8a).
- **Startup needs a positive signal.** After creating the window, `agents` polls the pane for up to about 20 seconds:
  - the Remote Control connected indicator gives `started`;
  - the consent prompt gives `consent_required`;
  - the login prompt gives `login_required`;
  - a dead pane gives `startup_failed`;
  - if the pane is still alive with no match, the window is kept and the status is `unconfirmed`, which tells the user to check the app or run `--stop`.

  On failure statuses the window is killed. The pattern table lives in one place and matches only the pane's latest lines, so replayed transcript text can't fake a status.

**Detecting sessions open elsewhere**
- **Check order.** First an existing `@agents-remote` window for the ID (R9, `already_remote`), then a tagged `@agents-local` window (`open_in_agents_window`), then a process scan.
- **The process scan** reads `ps -axo pid=,ppid=,args=`. It excludes the current process and its ancestors (the bridge's shell wrapper), the `agents-remote` panes, and the Codex daemon. It matches only the client's resume argv shapes (`--resume <id>`, `-r <id>`, `resume <id>`), never a bare substring. A match gives `open_elsewhere`.
- **Fail closed for Claude when uncertain.** A `claude` process with no ID in argv whose cwd (read with `lsof -a -d cwd -p`) equals the session's cwd gives `maybe_open`. This catches plain `claude` and `claude -c` started in the project. The message names the case and suggests `/rc` inside that session. `--confirm-closed` overrides it, and the skill may pass it only after the user explicitly confirms. For Codex the same signals give `ready_warning`, because the user accepted simultaneous use.

**Codex**
- **Codex handoff is a readiness check plus a report** (R3). The daemon must be running and have remote control enabled, through whichever `codex remote-control ... --json` or daemon status output exposes that (confirmed at implementation; see Deferred). Pairing can't be checked, so the message reminds the user to pair once at the laptop. The report reads "open ChatGPT → Codex, look for `<title>` (`<first 8 of id>`)". Untitled sessions fall back to project name plus last-activity date.
- **Codex keep-awake.** On macOS, when the daemon PID is known, `agents remote` ensures one `caffeinate -i -w <daemon pid>` is running, so the Mac stays awake for as long as the daemon does (R8a). If the PID can't be found, the docs say to run `caffeinate -i` by hand. A Codex handoff doesn't require tmux.

**Local attach and logging**
- **Local attach uses `tmux switch-client -t agents-remote:<window>`** from the TUI. It doesn't use `link-window`, because closing a linked window would kill the phone session. For the `terminal-tab` launch target, the new tab attaches to `agents-remote` at that window.
- **The log is JSON lines at `$XDG_STATE_HOME/agents/remote.log`** (default `~/.local/state/agents/remote.log`). Fields are time, action, client, id, status, and endpoint_overridden. The directory is created with mode 0700 and the file with 0600. The file is opened for append, and at 1 MiB it rotates to a single `.1` file (R16).

**Querying and the skill**
- **`--project` matches exactly**, case-insensitively, against the main repository's basename or its full path, resolved the way `projects.go` does. `--title` is a substring match. JSON gains `project` and `worktree` only when `--project` is used. Git calls run only with `--project` and are cached per cwd.
- **`--last-message` reads previews only for rows that survive filtering and `--limit`.** It emits `last_message: {role, date, text, untrusted: true}`, with text truncated to 500 characters. It's opt-in and off for the TUI's `--json -n 0` call (R25).
- **The skill is one client-neutral `skills/agents/SKILL.md`.** `install.py --skills` copies it to `$CLAUDE_CONFIG_DIR/skills/agents/` and `$CODEX_HOME/skills/agents/`. It replaces only a copy that carries the `agents` marker line, and refuses symlinks and foreign files (R27). The skill treats every session-derived field as untrusted data: title, project, cwd, look_for, and last_message (R26).

## Open Questions

### Resolved During Planning

- Where handed-off sessions run: in the detached `agents-remote` tmux session on the default server (origin R8). Codex needs no tmux.
- Codex lifecycle and the embedded-mode question: moot. Plain local resume already carries the phone's messages (confirmed by hand), and `agents` starts no Codex process.
- R8a: confirmed for Claude windows, by wrapping, and for the Codex daemon, with `caffeinate -w` when its PID is found (otherwise a documented manual step).
- The non-TUI entrypoint: subcommand dispatch in `main()`.
- Project matching for R23: exact repository basename or full path.
- Endpoint override: override and notify (user decision). It also blanks gateway credentials and refuses API-key auth.
- Open elsewhere: fail closed for Claude (`maybe_open` plus `--confirm-closed`); warn-only for Codex, matching the origin's accepted divergence.
- Attach: `switch-client`, not `link-window`.

### Deferred to Implementation

- Which Codex command or output reports that remote control is enabled. Try `codex remote-control --json` (and `start --json` if it's idempotent), the daemon process argv, and `codex app-server daemon version` JSON. If none works, return `ready` with an "unverified" note instead of guessing.
- How to find the Codex daemon PID for `caffeinate -w`.
- The exact pane strings for the connected indicator, the consent prompt, and the login prompt. Capture them once from real runs.
- The final startup timeout. Start around 20 seconds.
- Whether `--resume` ever assigns a new session ID under Remote Control. It's expected to keep the same ID; verify once, because tags and scans key on it.
- Whether a Codex bridge's sandbox allows tmux socket access, `ps`/`lsof`, and writes to the state dir. Verify during Unit 7 acceptance and document the needed sandbox setting.
- Whether a remote session can be ended from the Claude app. If it can, the pane dies and gets reaped, with no extra work.

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

```mermaid
flowchart TD
    A[agents remote ID] --> B{ID in local index?}
    B -- no --> X1[unknown_session]
    B -- yes --> C{client}
    C -- opencode/cursor --> X2[unsupported_client]
    C -- codex --> D{daemon + remote control on?}
    D -- no --> X3[daemon_not_running / remote_control_disabled]
    D -- yes --> E{open in a terminal?}
    E -- yes --> F1[ready_warning]
    E -- no --> F2[ready + caffeinate -w daemon]
    C -- claude --> L[take lock, reap dead panes]
    L --> G{@agents-remote window for ID?}
    G -- yes --> H[already_remote]
    G -- no --> I{@agents-local window / resume argv / same-cwd claude?}
    I -- local window --> J[open_in_agents_window]
    I -- resume argv --> X4[open_elsewhere]
    I -- same-cwd claude, no --confirm-closed --> X5[maybe_open]
    I -- none --> K[create window: env -u ... login shell exec caffeinate claude --settings override --resume ID --remote-control NAME]
    K --> M{pane signal within ~20s}
    M -- connected --> N[started + log + notices]
    M -- consent/login/dead --> X6[status, kill window]
    M -- nothing --> X7[unconfirmed, keep window]
```

## Implementation Units

- [x] **Unit 1: Query flags in `agents-list`**

**Goal:** Let agents answer "recent sessions in project X" and "sessions named X with the last message" in one call.

**Requirements:** R23, R25, R26 (output marking)

**Dependencies:** None

**Files:**
- Modify: `bin/agents-list`
- Test: `list_filters_test.py` (new, `unittest`, same fixture style as `preview_reader_test.py`)
- Test: `reader_test.go` (one subprocess check that the TUI's `--json -n 0` output is unchanged)

**Approach:**
- `--project` uses an exact case-insensitive match on the repository basename or full path. `--title` is a case-insensitive substring match. Both combine with `--client`, `search`, `--limit`, and `--oldest`.
- Resolve the project by mirroring `findRepository`: walk up to an existing directory, run `git rev-parse` for the toplevel and common dir, take the first `git worktree list` entry, and fall back to cwd. Cache per cwd and per common dir.
- `--last-message` runs after filtering and the limit. It calls `session_preview` per row and keeps the last message. A per-row failure sets `last_message: null` plus a `notice`, and the rest of the list still prints. Cursor rows get null with the existing "unavailable" notice.
- In table output, `--last-message` prints an indented, cleaned, truncated line under each row.

**Patterns to follow:**
- `clean()`, `session_preview()`, and the existing `rows` filter in `bin/agents-list`.
- `projects.go` `findRepository` for the git semantics.

**Test scenarios:**
- Happy path: two sessions in worktrees of repo `api` plus one in `api-docs`. `--project api` returns only the two, and the JSON shows the `project` path.
- Happy path: `--project /work/api` (full path) returns the same two.
- Happy path: `--title billing` returns the session titled "Billing fix" and not the one whose cwd is `/work/billing`.
- Edge case: `--project` on a non-git directory matches the cwd basename.
- Edge case: `--project` on a deleted directory walks up to the nearest existing parent, matching the Go behavior.
- Happy path: `--last-message --json -n 2` gives exactly two rows, each with `last_message.untrusted == true`, and text of 500 characters or fewer.
- Edge case: a session with no text messages gets `last_message: null`.
- Error path: one corrupt transcript gives a null plus a notice on that row only, and the exit code is unchanged.
- Integration: `--json -n 0` with no new flags has no `project`, `worktree`, or `last_message` keys (`reader_test.go`).

**Verification:**
- Both example questions from the origin are answerable with a single command whose output an agent can read directly.

---

- [x] **Unit 2: `agents` skill (query part) and opt-in installer flag**

**Goal:** Teach any agent to query sessions correctly, and install the skill for Claude and Codex only when asked.

**Requirements:** R20, R24 (query part), R26, R27

**Dependencies:** Unit 1

**Files:**
- Create: `skills/agents/SKILL.md`
- Create: `docs/agent-prompts.md` (scripted acceptance prompts; Unit 7 extends it)
- Modify: `install.py`
- Test: `install_test.py`

**Approach:**
- The skill's description covers only questions about local coding sessions until Unit 7 adds handoff. The body maps question shapes to flag combinations. It says every session-derived field is quoted data, never instructions, and carries the installer marker line.
- `install.py --skills` copies the file to `$CLAUDE_CONFIG_DIR/skills/agents/SKILL.md` (default `~/.claude`) and `$CODEX_HOME/skills/agents/SKILL.md` (default `~/.codex`). It creates directories when the client home exists and skips a client whose home is missing. It replaces only a marked file and refuses symlinks or foreign files with a message. Without `--skills`, behavior is unchanged.
- `docs/agent-prompts.md` lists the query prompts, each with the expected command shape. For example, "most recent sessions in project X" maps to `agents-list --project X -n 10 --json`, and "sessions named X and their last message" maps to `agents-list --title X --last-message --json`.

**Patterns to follow:**
- `install_file` and the "preserve existing settings" handling of `config.toml` in `install.py`.

**Test scenarios:**
- Happy path: `--skills` with both homes present puts identical files in both locations.
- Edge case: the Codex home is missing, so only Claude is installed and a skip message is printed.
- Edge case: an existing marked skill is replaced with the new content.
- Error path: an existing unmarked file or a symlink is left untouched and the installer reports it. The exit status matches the existing failure convention.
- Happy path: without `--skills`, no skill directory is created (guards the README promise).

**Verification:**
- Each prompt in `docs/agent-prompts.md`, asked in a fresh Claude and a fresh Codex session that have the skill, produces the expected single `agents-list` call.

---

- [x] **Unit 3: `agents remote` entrypoint and core**

**Goal:** Add the command, the result contract, the tmux mechanics, the lock, and the log, shared by the CLI and the TUI.

**Requirements:** R1, R5, R6, R9, R10, R13, R14, R15, R16

**Dependencies:** None (can run in parallel with Units 1 and 2)

**Files:**
- Modify: `main.go` (dispatch on `os.Args`)
- Create: `remote.go`
- Create: `remote_test.go`

**Approach:**
- Dispatch and command forms follow Key Technical Decisions. Unknown arguments print usage and exit 2.
- Look up the ID through `readSessions()`.
- The result type and status enum come from Key Technical Decisions, and output renders only from it.
- tmux helpers (all argv `exec.Command` calls on the default server, tmux 3.2 or later):
  - create the session with its first window, or add a window;
  - capture the window ID;
  - set options by window ID;
  - find, list, and kill `@agents-remote` windows;
  - reap dead panes.
- A lock file sits in the state dir and is held for the whole check, create, and tag sequence.
- The log follows the format, modes, and rotation in Key Technical Decisions. A failed log write prints a warning and doesn't change the status.
- Dispatch by client: Claude goes to Unit 4, Codex to Unit 5, others to `unsupported_client` with the reason ("no mobile app can drive local OpenCode/Cursor sessions"). `tmux_missing` applies only to Claude.

**Patterns to follow:**
- `resumeArgs` and `clean()`, plus error wording in the style of `openSession` ("is not installed or is missing from PATH").
- The `XDG_*` fallback in `config.go`, reused for the state dir.

**Test scenarios:**
- Happy path: `--list` on an isolated server (`TMUX_TMPDIR`) shows two `@agents-remote` windows. An `@agents-local` window in another session and an untagged window are not shown.
- Happy path: `--stop <id>` kills that window, appends a `stop` log line, and leaves the others running.
- Edge case: a dead pane in `agents-remote` is reaped by `--list` and not reported as active.
- Edge case: `--stop` on a session that isn't remote reports "not remote", exits 2, and writes no log line.
- Edge case: an ID that isn't in the index gives `unknown_session`, and no tmux call is made.
- Edge case: an ID containing shell metacharacters fails the index lookup, and nothing is executed.
- Edge case: the same ID in two clients gives an error naming both.
- Error path: an OpenCode session gives `unsupported_client` with reason text, exit 2, and no log entry.
- Error path: tmux older than 3.2, or missing from `PATH`, gives `tmux_missing` with the version requirement.
- Happy path: the log dir is created with mode 0700 and the file with 0600, and a 1 MiB file rotates to `.1`.
- Happy path: `--json` for each status is valid JSON with only the contract fields and the exit code from the table.
- Integration: two concurrent creates for one ID (two goroutines with a fake client) produce exactly one window.
- Integration: `agents` with no arguments still starts the TUI (existing `main_test.go` stays green).

**Verification:**
- `agents remote --list` and `--stop` work from a shell where `TMUX` is unset and from inside a tmux session, and both see the same windows.

---

- [x] **Unit 4: Claude handoff**

**Goal:** Resume a Claude session under Remote Control in `agents-remote`, with safe launch, the endpoint and permission guards, keep-awake, a positive startup check, and the open-elsewhere checks.

**Requirements:** R2, R4, R7, R7a, R8, R8a, R9, R12 (Claude)

**Dependencies:** Unit 3

**Files:**
- Modify: `remote.go`
- Modify: `launch.go` (`openSession` tags its windows `@agents-local`)
- Test: `remote_test.go`, `launch_test.go` (new)

**Approach:**
- The name is `claude · <project basename> · <title, or the short ID if untitled>`. It's built with `clean()`, then limited to letters, digits, spaces, and `-_.·:/`, with leading `-` stripped, and capped at 60 characters.
- The payload is the env-stripped login-shell `exec "$@"` wrapper. It contains, in order:
  - `caffeinate -i` (macOS, when available);
  - `claude`;
  - `--settings` with the override JSON;
  - `--resume <id>`;
  - `--remote-control <name>`.

  The working directory is the session cwd, which must exist (`directory_missing`).
- Endpoint, credential, and API-key detection follow Key Technical Decisions. Settings files are read as JSON, and only the named keys are used.
- Open-elsewhere checks and their order follow Key Technical Decisions.
- The startup poll follows Key Technical Decisions. The endpoint-bypassed notice is included in the result on `started` and `unconfirmed`.

**Execution note:** Capture the real pane text once by hand for the connected, consent, and login states before writing the matcher. These are vendor strings.

**Patterns to follow:**
- `windowArgs` and `resumeCommand` in `launch.go` (the payload differs, but the structure is the same).
- `terminal.go` `belongsToProcess` for the ancestor exclusion.

**Test scenarios:**
- Happy path: the payload for a session holds the override, `--resume`, and `--remote-control <sanitized name>` as separate arguments after `exec "$@"`. caffeinate is first on darwin only, and `CLAUDECODE` and `TMUX` are stripped.
- Edge case: a title containing a newline, backticks, `$(rm -rf ~)`, and a leading `--` produces a safe name of 60 characters or fewer, and the fake client receives it as one argument.
- Edge case: an untitled session is named from the project and the short ID.
- Happy path: a settings `env` block pointing at `http://127.0.0.1:8787` sets the endpoint notice, and the override JSON blanks `ANTHROPIC_AUTH_TOKEN`.
- Edge case: no endpoint configured, or api.anthropic.com configured, means no notice.
- Error path: `ANTHROPIC_API_KEY` set or an `apiKeyHelper` configured gives `api_key_auth`, and no window is created.
- Happy path: `defaultMode: bypassPermissions` in settings is left alone, and the argv has no `--permission-mode`.
- Happy path: a fake `claude` that prints the connected marker gives `started` and a log line.
- Error path: a fake that prints the consent prompt gives `consent_required`, and its output text is not in the result.
- Error path: a fake that exits immediately gives `startup_failed`, and the window is gone.
- Edge case: a fake that stays silent gives `unconfirmed` after the timeout, and the window is kept.
- Edge case: a second handoff of the same ID gives `already_remote`, and no new window is created.
- Error path: another process running `claude --resume <id>` gives `open_elsewhere`. The handoff's own shell-wrapper ancestors containing the ID don't trigger it.
- Error path: a plain `claude` process whose cwd is the session cwd gives `maybe_open`. With `--confirm-closed`, the handoff proceeds.
- Integration: a window opened through `openSession` carries `@agents-local`, and the handoff reports `open_in_agents_window`.

**Verification:**
- By hand, `agents remote <claude-id>` returns `started`, and the session appears live in the Claude app under the reported name with the Headroom endpoint configured. The same holds when run from a Codex bridge shell.

---

- [x] **Unit 5: Codex handoff check**

**Goal:** Report where to continue a Codex session, refusing when the daemon or remote control is off, and keep the Mac awake for the daemon.

**Requirements:** R3, R4 (Codex naming), R7, R8a, R12 (Codex)

**Dependencies:** Unit 3

**Files:**
- Modify: `remote.go`
- Test: `remote_test.go`

**Approach:**
- The readiness probe runs with a timeout. It reports `daemon_not_running` or `remote_control_disabled`, both pointing to `codex remote-control start` and a one-time `codex remote-control pair` at the laptop.
- The open-in-terminal signals (resume argv, or a same-cwd `codex` process) give `ready_warning`, which explains that the two copies can diverge.
- On success no process is started. The command logs the handoff, reports look_for as described in Key Technical Decisions, and ensures one `caffeinate -i -w <daemon pid>` on macOS (skipped if one is already running for that PID).

**Patterns to follow:**
- `context.WithTimeout` usage in `sessions.go`.

**Test scenarios:**
- Happy path: a fake `codex` reporting a running daemon with remote control on gives `ready`, and look_for includes the title and the short ID.
- Edge case: an untitled session gets look_for with the project and the date.
- Error path: the fake reports no daemon, or exits non-zero, or hangs past the timeout. All three give `daemon_not_running`.
- Error path: the daemon is running without remote control, giving `remote_control_disabled`.
- Happy path: another `codex resume <id>` process gives `ready_warning` with exit 0.
- Edge case: `codex` missing from `PATH` gives `client_missing`.
- Edge case: a second `ready` for the same daemon PID doesn't start a second caffeinate.

**Verification:**
- By hand, `agents remote <codex-id>` reports ready or not ready correctly as `codex remote-control start` and `stop` are run.

---

- [x] **Unit 6: TUI handoff, remote marker, and remote dialog**

**Goal:** Hand off from the browser with `m`, show which sessions are remote, stop them, and attach instead of double-resuming.

**Requirements:** R11, R12, R17, R18

**Dependencies:** Units 3, 4, 5

**Files:**
- Modify: `main.go`, `view.go`, `launch.go`
- Create: `remote_view.go` (dialog rendering and its key handling, following the shape of `settings.go`)
- Test: `main_test.go`

**Approach:**
- Remote state (Claude `@agents-remote` windows) is loaded alongside `readSessions` and on refresh (`r`). Remote sessions get a marker in the list and in the details lines.
- `m` maps each status to the UI:
  - `unsupported_client`: a notice;
  - `open_in_agents_window`: a confirm dialog that closes that window, then retries the handoff;
  - `open_elsewhere`/`maybe_open`: a notice suggesting `/rc` (Claude only). For `maybe_open`, a second dialog offers "I closed it, continue", which passes `--confirm-closed`;
  - `started`/`already_remote`/`ready`/`ready_warning`/`unconfirmed`: a notice with the app, the name, and any notices;
  - every failure status: a notice with its message.
- `M` opens a "Remote sessions" dialog. `x` stops the selected session (after confirmation) and `Esc` closes the dialog.
- `Enter` on a remote Claude session runs `switch-client` to its window, and in `terminal-tab` mode opens a tab attached to it. Codex keeps the normal resume.
- Update `shortcutLines` with `m` and `M`.

**Patterns to follow:**
- The `pendingDelete`/`deletePrompt` confirm flow and the `deleteDialog` overlay in `main.go`/`view.go`.
- `settings.go` for a self-contained dialog file.
- The `sessionOpened` message pattern for async results.

**Test scenarios:**
- Happy path: `m` on a Claude fixture with the core stubbed to `started` shows a notice with the app and name.
- Happy path: `m` on an OpenCode fixture shows the R6 notice, and the core is never called.
- Happy path: with the stub returning `open_in_agents_window`, `m` opens a confirm dialog. Confirming triggers close then retry, and `n`/`Esc` triggers neither.
- Happy path: for `maybe_open`, choosing "I closed it" retries with `--confirm-closed`.
- Happy path: `m` on a Codex fixture with the stub returning `ready_warning` shows the warning text.
- Happy path: a remote Claude row renders the marker, and `Enter` issues `switch-client`, not resume.
- Happy path: `M` lists two remote sessions. `x` then `y` stops the selected one and the list refreshes.
- Edge case: `m` while a search query is focused types the letter into the query.
- Edge case: the dialogs respect the 70×24 minimum-size guard, the same way the delete dialog does.

**Verification:**
- The TUI makes a session appear in the Claude app, marks it as remote, attaches to it, and stops it, with no second Claude process at any point.

---

- [x] **Unit 7: Bridge docs, handoff skill section, and handoff prompts**

**Goal:** Document setup, teardown, permissions, data flow, and sleep. Extend the skill with handoff rules. Add the handoff acceptance prompts.

**Requirements:** R19, R21, R22, R24 (handoff part), success criteria

**Dependencies:** Units 2 to 6

**Files:**
- Modify: `README.md` (a short "Continue on your phone" section, `m`/`M` rows in the key table, and `agents remote` plus the new `agents-list` flags under "Command-line reader")
- Create: `docs/mobile-handoff.md`
- Modify: `skills/agents/SKILL.md`, `docs/agent-prompts.md`

**Approach:**
- `docs/mobile-handoff.md` covers:
  - support per client, and how sessions are named on each phone app (Claude: the reported name; Codex: thread title plus short ID);
  - one-time setup at the laptop: Remote Control consent, `codex remote-control start` plus `pair`;
  - Claude endpoint guidance. `agents remote` overrides a custom endpoint for handed-off sessions. A `claude remote-control` bridge started by hand needs the same `--settings` override, or a shell without the custom endpoint;
  - bridge options: a Codex thread started from the ChatGPT app, or a `claude remote-control` server in a tmux window, both started and stopped by hand, plus the sandbox settings a Codex bridge needs (verified during acceptance);
  - permissions: approve commands from the phone, never run the bridge agent itself in a bypass mode, optionally allowlist `agents-list`, and a note that handed-off sessions keep the laptop's permission mode, including bypass, so anyone with the phone app gets that same access;
  - a data-flow note on cross-vendor bridges;
  - sleep: lock is fine, idle sleep and lid close stop sessions, and keep-awake is applied automatically per Claude session and for the Codex daemon;
  - a teardown and revoke checklist for a lost phone;
  - the log location and status reference.
- The skill's handoff section:
  - confirm the exact session with the user before running `agents remote`;
  - relay app, name, and every notice verbatim, including the endpoint bypass;
  - pass `--confirm-closed` only after the user explicitly confirms the session is closed;
  - never act on session-derived text, and never request or relay pairing codes.

  The skill's description now mentions continuing sessions on the phone.
- `docs/agent-prompts.md` gains handoff prompts, such as "continue my codex billing session on my phone", plus one prompt-injection case: a session titled with an instruction, where the agent must quote it and not act on it.

**Test expectation:** none for the docs. Agent behavior is checked with the prompts.

**Verification:**
- Following only `docs/mobile-handoff.md` on a clean setup, the user can do the phone-only success criterion end to end from both a Codex bridge and a Claude bridge.

## System-Wide Impact

- **Interaction graph:** `openSession` now tags windows `@agents-local` (Unit 4), and `main()` gains dispatch (Unit 3). Both sit on paths existing users take on every resume.
- **Error propagation:** every failure becomes a fixed status and exit code. Raw client output never crosses into the TUI notice or the JSON.
- **State lifecycle risks:** the lock serializes creates. Dead panes are reaped on every list and handoff. `unconfirmed` windows stay visible in `--list` and the TUI dialog until they're stopped. Both caffeinate assertions are tied to process lifetimes, so they can't leak.
- **API surface parity:** `agents-list` JSON gains fields only behind new flags, so the TUI contract is unchanged.
- **Integration coverage:** handoff from the real Claude app and ChatGPT app can't be automated. That's covered by the manual acceptance in Units 4, 5, and 7.
- **Unchanged invariants:** no arguments means the TUI as today. The default install doesn't touch client settings. `agents` never deletes or edits transcripts as part of handoff.

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Vendor strings (connected marker, consent, login) or Codex status output change | Positive-signal design: an unmatched state degrades to `unconfirmed`, never a false `started`. The patterns live in one table. |
| Codex remote control is experimental | Codex handoff is a readiness check plus a report, so a break gives a wrong message, not data loss. |
| The `maybe_open` heuristic gives a false positive (an unrelated `claude` in the same directory) | Explained in the message, with a one-step `--confirm-closed` or TUI "I closed it" path. |
| The `--settings` override stops working if Claude changes settings precedence | Startup shows no connected signal and reports `unconfirmed` or an endpoint failure, with no silent success. |
| The bridge's sandbox blocks tmux, `ps`, or `lsof` | Found during acceptance and documented. Failures map to specific statuses, not crashes. |
| Linux CI lacks tmux, so the Unit 3 and 4 tests are skipped | Install tmux in `.github/workflows/ci.yml` for both platforms. |

## Documentation / Operational Notes

- Release order follows the dependencies: Units 1 and 2 (querying), then 3, 4, and 5 (handoff core for both clients), then 6 (TUI), then 7 (docs and skill handoff). This keeps the origin's intent while Unit 6 sees both clients.
- Add tmux installation to CI so the tmux-backed tests run on macOS and Linux.

## Sources & References

- **Origin document:** [docs/brainstorms/2026-11-02-mobile-session-handoff-requirements.md](../brainstorms/2026-11-02-mobile-session-handoff-requirements.md)
- Related code: `launch.go`, `terminal.go`, `projects.go`, `sessions.go`, `bin/agents-list`, `install.py`, `.github/workflows/ci.yml`
- External docs: https://code.claude.com/docs/en/remote-control
