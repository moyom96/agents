# agents

Browse, search, preview, and resume local coding-agent sessions from one terminal interface.

Supports Claude Code, Codex CLI, OpenCode, and Cursor's terminal agent.
Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea),
[Bubbles](https://github.com/charmbracelet/bubbles),
[Huh](https://github.com/charmbracelet/huh), and
[Lip Gloss](https://github.com/charmbracelet/lipgloss).

## Features

- Sessions sorted by last activity, with full local dates and sort indicators.
- Combined client, project, and session-name filters.
- Git worktrees grouped under their main repository, preserving the launch directory.
- Recent-message previews, with a scrollable expanded view that opens at the latest message.
- Resume sessions in a new tmux window, leaving the browser open.
- An optional native Alacritty tab launcher on macOS.
- Persistent launch settings and confirmation before deleting saved history.
- A separate `agents-list` command for listing, JSON output, and scripting.

## Requirements

macOS or Linux, Python 3.9 or later, and Go 1.26 or later to build.
The TUI needs a terminal at least 70 columns by 24 rows.

Install tmux to resume sessions and Git to group worktrees. Install the clients
you want to resume and make their commands available on `PATH`:
`claude`, `codex`, `opencode`, or `cursor-agent`.
You can browse saved histories without starting these clients or tmux.

The reader follows local storage formats, which may change between client versions.
Windows is not currently supported.

## Install

```sh
git clone https://github.com/moyom96/agents.git
cd agents
python3 install.py
```

The installer builds `agents` and copies it, along with the Python
reader `agents-list`, into `~/.local/bin`. It creates the default config if
missing and preserves existing settings. It does not edit client settings,
shell startup files, or authentication files. Go downloads build dependencies;
the browser itself reads local histories without making API calls.

Add the install directory to your shell's `PATH` if needed:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Add that line to your shell startup file to keep it across terminal sessions.
You can choose different destinations:

```sh
python3 install.py --bin-dir "$HOME/bin" --config-dir "$HOME/.config"
```

When using `--config-dir`, set `XDG_CONFIG_HOME` to the same directory at runtime.
With mise, select Go first, for example `mise exec go@1.26 -- python3 install.py`.

## Use

Start the browser from inside tmux:

```sh
tmux new-session -A -s main
agents
```

The browser lists all available top-level sessions. Filters combine, and
search matches session names without regard to case. Repositories with the same
directory name stay separate. A session always resumes in its recorded directory,
including when that directory is a worktree.

| Key | Action |
| --- | --- |
| `Tab` / `Shift+Tab` | Cycle Sessions → Projects → Clients, or in reverse |
| `1` / `2` / `3` | Focus Clients / Projects / Sessions directly |
| Arrows or `j` / `k` | Move through sessions or filters |
| `PageUp` / `PageDown`, `Home` / `End` | Move through longer lists |
| `/` | Search session names |
| Any unmapped letter | Start a new search with that letter |
| `Enter` / `Esc` while searching | Finish editing the query |
| `Enter` on a session | Resume using the configured launch target |
| `p` | Expand the message preview |
| `d` | Open the delete confirmation dialog |
| `s` | Toggle newest-first / oldest-first sorting |
| `r` | Refresh sessions and the selected preview |
| `,` | Open settings |
| `Esc` | Clear filters and search |
| `q` / `Ctrl+C` | Quit |

When search is focused, letters such as `d`, `p`, and `q` are query text.
Missing directories, unavailable commands, and client-reader failures appear
inside the browser. A failure in one client does not hide the others.

### Message previews

The lower pane shows recent text for the selected session. Press `p` to expand
the last six user and assistant messages with dates and full session details.
The expanded view starts at the bottom. Scroll with arrows, `j` / `k`, or
`PageUp` / `PageDown`; press `p`, `Esc`, or `q` to close it.

User messages have a contrasting background and a `>` marker. Assistant messages
are indented and start with `·`. Previews are plain text, exclude tool output and
reasoning, and strip terminal control sequences. Reads are delayed briefly while
selection moves. File previews read at most the last 4 MiB of a transcript;
individual messages are capped at 12,000 characters. Refresh to see newer messages
from an active conversation.

| Client | Session list | Text preview | Resume command |
| --- | --- | --- | --- |
| Claude Code | Yes | Yes | `claude --resume ID` |
| Codex CLI | Yes | Yes | `codex resume ID` |
| OpenCode | Yes | Yes, current and legacy stores | `opencode --session ID` |
| Cursor terminal agent | Yes | Unavailable for encrypted local messages | `cursor-agent --resume ID` |

Cursor editor conversations are outside this integration.

### Launch settings

Press `,`, choose with arrows or `j` / `k`, and press `Enter` to save or `Esc` to
cancel. The same setting can be edited in `~/.config/agents/config.toml`,
or `$XDG_CONFIG_HOME/agents/config.toml` when set:

```toml
[launch]
target = "tmux-window"
```

`tmux-window` is the default. It opens a window in the browser's tmux session.
`terminal-tab` currently supports macOS Alacritty with its default IPC socket
and Cmd+T binding, configured to attach to the same tmux server at startup.
The launcher opens a native tab and replaces only that new tab's tmux client
with the resume command. The resumed client runs outside tmux in that tab.

macOS may require Automation and Accessibility permission for Alacritty or
the process running `osascript`. Tab opening reports an error if permission is
denied, startup does not attach within 10 seconds, or the destination is
ambiguous. It does not silently open a different kind of window.

Settings are read again before opening a session. After a client exits,
a login shell remains in the project's directory.

### Deletion

The delete dialog defaults to Cancel. Choose Delete and press `Enter`, or
press `y` to confirm. `Esc` or `n` cancels. Close the session in its original
client before deleting it.

Deletion permanently removes saved history. Codex uses `codex delete --force`,
and OpenCode uses `opencode session delete`; these clients may also remove child
sessions. Claude deletion removes the matching local transcript and companion
session directory. Cursor deletion removes the selected saved terminal chat.
Project files and worktrees are kept.

### Command-line reader

```sh
agents-list                          # newest 30 sessions
agents-list -n 0                     # all sessions
agents-list --client codex           # one client
agents-list --oldest                 # oldest first
agents-list example                  # filter project, title, or ID
agents-list --commands -n 5          # include resume commands
agents-list --json -n 0              # structured session metadata
agents-list --client claude --preview SESSION_ID --json
```

The TUI uses `--json -n 0`, so it has no 30-session cap.

## Local data and privacy

No account, server, telemetry, or API key is needed by this tool. It reads local
session metadata and requested previews. Opening an agent starts that client's
normal process, which may communicate with its provider.

Standard storage locations and supported overrides:

| Client | Default location | Override |
| --- | --- | --- |
| Codex CLI | `~/.codex` | `CODEX_HOME` |
| Claude Code | `~/.claude` | `CLAUDE_CONFIG_DIR` |
| OpenCode | `~/.local/share/opencode` | `XDG_DATA_HOME` (the parent of `opencode`) |
| Cursor terminal agent | `~/.cursor/chats` | None |

Codex uses its session index and JSONL rollout files, not its SQLite database.
Claude uses transcript metadata. OpenCode databases are opened read-only.
Cursor reads chat metadata and titles, not encrypted message blobs.
Archived sessions, subagents, and delegated scratchpad conversations are excluded
where the client exposes enough metadata to identify them.

Last activity means the saved update timestamp, not the last time a terminal
was focused. File modification time is a fallback and can change after maintenance.
Dates use `DD/MM/YYYY` in the local timezone; JSON timestamps remain ISO 8601.
The browser does not display subscription quotas or remaining weekly allowances.

All examples and tests use synthetic data. Do not attach real transcripts or
unredacted screenshots when reporting issues.

## Update and uninstall

From your checkout, update and reinstall:

```sh
git pull --ff-only
python3 install.py
```

To uninstall, remove `agents-list` and `agents` from the install
directory. Optionally remove the `agents` config directory. Installation
copies files, so the checkout can be removed without breaking the installed tool.

## Development

```sh
go test ./...
go vet ./...
go build -trimpath -o agents .
python3 -m unittest discover -p '*_test.py'
```

The tests create temporary histories and databases. They do not delete real
sessions. A native Alacritty GUI test is opt-in and skipped by default.
CI runs tests, vet, and build on macOS and Linux. Changes to `main` go through
pull requests and passing checks.

## License

[MIT](LICENSE).
