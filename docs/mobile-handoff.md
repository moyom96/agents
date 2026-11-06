# Continue sessions on your phone

`agents remote` hands a saved session to its client's mobile app, so you can keep working from the phone while the session keeps running on this computer.

| Client | Phone app | What `agents remote` does | Name on the phone |
| --- | --- | --- | --- |
| Claude Code | Claude app → Code | Resumes the session under Remote Control in a detached `agents-remote` tmux session | `<project> - <title>` |
| Codex CLI | ChatGPT app → Codex | Checks that the remote-control daemon is running; the app already lists every local session | `<title> (<first 8 characters of the ID>)` |
| OpenCode, Cursor | None | Refuses with an explanation | |

The computer has to stay on and awake. The phone is a window into the session; nothing runs in the cloud.

## One-time setup, at the computer

1. Claude: sign in with a claude.ai account (`claude`, then `/login`), then run `claude --remote-control` once and accept the Remote Control confirmation. Remote Control does not work with `apiKeyHelper`; an `ANTHROPIC_API_KEY` is set aside for handed-off sessions so they use the claude.ai login.
2. Codex: run `codex remote-control start`, then `codex remote-control pair` and enter the code in the ChatGPT app. Do the pairing here, never through a chat: the code grants control of this computer.
3. Install tmux 3.2 or later.

If Claude is configured with a custom API endpoint, such as a local compression proxy in `ANTHROPIC_BASE_URL`, `agents remote` starts handed-off sessions against api.anthropic.com instead and says so, because Remote Control only works there. The proxy's features are off for that session. `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_CUSTOM_HEADERS`, and `ANTHROPIC_API_KEY` are always cleared for handed-off sessions, even when only your shell profile sets them, so a gateway credential is never sent to Anthropic.

## Hand off before you leave

In the browser, select a session and press `m`. The status line says which app to open and what to look for. Press `M` to list remote sessions and `x` to stop one. Pressing Enter on a remote Claude session switches to its tmux window instead of starting a second Claude process.

From a shell:

```sh
agents remote <session-id>          # hand off
agents remote --list                # remote Claude sessions
agents remote --stop <session-id>   # end one
agents remote <session-id> --json   # for scripts and agents
```

If the session is still open in a terminal, `agents remote` refuses, because two Claude processes on one session diverge. This includes asking a Claude session to hand off itself. Inside that open session, `/rc` continues it on the phone directly. If the process list can't be read, the command refuses too rather than guess. If a plain `claude` is running in the same project, the command cannot tell whether it is the same session; close it and pass `--confirm-closed`. For Codex the command warns instead, since the daemon keeps one history.

## Hand off after you leave: the bridge

To start from the phone, keep an agent you can reach from the phone running on the computer, and install the skill with `python3 install.py --skills`. Then ask it, for example, "continue my billing session on my phone".

- Codex bridge: with the daemon running, start a new Codex thread on this computer from the ChatGPT app. It runs on the computer and can call `agents-list` and `agents remote`.
- Claude bridge: in a tmux window, run `claude remote-control` in a trusted directory. With a custom endpoint configured, start it with `claude --settings '{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com"}}' remote-control`.

Start and stop the bridge yourself; nothing starts it automatically. Other agents can use the skill by reading `skills/agents/SKILL.md`.

Permissions:

- Approve the bridge's commands from the phone. Do not run the bridge itself in a permission-bypass mode: its approval prompts are what keep text inside a session title or message from turning into a command.
- Optionally allow `agents-list` without a prompt; it only reads history.
- Handed-off sessions keep this computer's permission settings exactly, including `bypassPermissions`. With bypass on, anyone signed in to your Claude app can run commands on this computer without a prompt.
- A Codex bridge runs in Codex's sandbox. If `agents remote` fails there to reach tmux or write `~/.local/state/agents`, allow those in the Codex sandbox settings or approve the command outside the sandbox.

Data flow: a bridge from one vendor sends what it reads to its own provider. A Claude bridge handing off a Codex session sends that session's title, path, and any requested message text to Anthropic, and the other way around.

## Sleep

| Situation | Phone keeps working |
| --- | --- |
| Screen locked | Yes |
| Display off | Yes |
| Idle sleep | No. `agents remote` keeps the Mac awake (`caffeinate -i`) while a handed-off Claude session or the Codex daemon is running |
| Lid closed | No, unless an external display is connected |

On Linux, keep the computer awake yourself.

## Lost phone or compromised account

1. Stop remote Claude sessions: `agents remote --list`, then `agents remote --stop <id>`, or `tmux kill-session -t agents-remote`.
2. Stop the Codex daemon: `codex remote-control stop`.
3. Stop any bridge you left running.
4. Sign out of the Claude and ChatGPT apps on the lost device from each account's security settings, and change the passwords if the account may be compromised.

## Statuses

`agents remote --json` prints `status`, `message`, `app`, `look_for`, and `notices`. Exit code 0 means you can continue on the phone (`started`, `already_remote`, `ready`, `ready_warning`). Exit 3 is `unconfirmed`: Claude started but Remote Control did not confirm in time, so check the app or stop it. Exit 2 is bad usage (`usage_error` with `--json`), a bad ID, or an unsupported client, exit 4 means the session is or may be open elsewhere, and exit 5 means a prerequisite is missing or a check failed (`index_unavailable`, `scan_failed`, `busy`); the message says what to do. `agents remote --help` prints the same summary.

Remote sessions live on the default tmux server. If the browser runs on another server (`tmux -L` or `-S`), Enter can't attach; use `tmux attach -t agents-remote` instead.

Each handoff and stop is logged without message content in `~/.local/state/agents/remote.log` (or `$XDG_STATE_HOME/agents/remote.log`).
