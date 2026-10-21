package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

func TestDeletedOpenCodeSessionsDoNotReturnFromLegacyTable(t *testing.T) {
	dir := t.TempDir()
	fixture := `import pathlib, sqlite3, sys
p = pathlib.Path(sys.argv[1]) / 'opencode'
p.mkdir()
with sqlite3.connect(p / 'opencode.db') as db:
    for table in ('session', 'session_v2'):
        db.execute(f'create table {table} (id text, directory text, title text, time_updated integer, parent_id text, time_archived integer)')
    db.execute("insert into session values ('ses_deleted', '/project', 'Deleted session', 1000, null, null)")
`
	if output, err := exec.Command("python3", "-c", fixture, dir).CombinedOutput(); err != nil {
		t.Fatalf("prepare database: %v: %s", err, output)
	}
	cmd := exec.Command("python3", "bin/agents-list", "--client", "opencode", "--json")
	cmd.Env = append(os.Environ(), "XDG_DATA_HOME="+dir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("read sessions: %v: %s", err, output)
	}
	var rows []session
	if err := json.Unmarshal(output, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("deleted migrated session reappeared: %#v", rows)
	}
}

func TestCursorReaderListsNamedRootSessionsAndResumeCommands(t *testing.T) {
	dir := t.TempDir()
	script := `import importlib.machinery, importlib.util, json, pathlib, sqlite3, sys
loader = importlib.machinery.SourceFileLoader('sessions', 'bin/agents-list')
spec = importlib.util.spec_from_loader(loader.name, loader)
reader = importlib.util.module_from_spec(spec)
loader.exec_module(reader)
home = pathlib.Path(sys.argv[1])
for session, extra in [('root', {}), ('child', {'isSubagent': True}), ('empty', {'hasConversation': False})]:
    folder = home / 'chats' / 'project-hash' / session
    folder.mkdir(parents=True)
    meta = {'cwd': '/project/worktree', 'hasConversation': True, 'updatedAtMs': 1792500000000, **extra}
    (folder / 'meta.json').write_text(json.dumps(meta))
    with sqlite3.connect(folder / 'store.db') as db:
        db.execute('create table meta (key text primary key, value text)')
        db.execute('insert into meta values (?, ?)', ('0', json.dumps({'name': 'Cursor worktree'}).encode().hex()))
rows = reader.cursor_sessions(home)
assert len(rows) == 1, rows
assert rows[0]['id'] == 'root' and rows[0]['title'] == 'Cursor worktree', rows
assert rows[0]['cwd'] == '/project/worktree' and rows[0]['activity'] == 1792500000, rows
assert reader.resume_command(rows[0]) == 'cd /project/worktree && cursor-agent --resume root'
(home / 'chats' / 'project-hash' / 'root' / 'store.db').write_bytes(b'invalid database')
rows = reader.cursor_sessions(home)
assert len(rows) == 1 and not rows[0]['title'], rows
`
	cmd := exec.Command("python3", "-c", script, dir)
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Cursor session reader: %v: %s", err, output)
	}
}

func TestCursorReaderExcludesUnmarkedAgentScratchpads(t *testing.T) {
	script := `import importlib.machinery, importlib.util, json, pathlib, sys
loader = importlib.machinery.SourceFileLoader('sessions', 'bin/agents-list')
spec = importlib.util.spec_from_loader(loader.name, loader)
reader = importlib.util.module_from_spec(spec)
loader.exec_module(reader)
home = pathlib.Path(sys.argv[1])
paths = {
    'delegated-mac': '/private/tmp/claude-1000/project/session/scratchpad/task',
    'delegated-linux': '/tmp/claude-1000/project/session/scratchpad',
    'regular-temp': '/tmp/my-project',
    'regular-scratchpad': '/projects/scratchpad',
    'worktree': '/projects/main/.worktrees/feature',
    'named-project': '/tmp/claude-tools/scratchpad',
}
for session, cwd in paths.items():
    folder = home / 'chats' / 'project-hash' / session
    folder.mkdir(parents=True)
    (folder / 'meta.json').write_text(json.dumps({'cwd': cwd, 'hasConversation': True}))
rows = reader.cursor_sessions(home)
assert {r['id'] for r in rows} == {'regular-temp', 'regular-scratchpad', 'worktree', 'named-project'}, rows
`
	cmd := exec.Command("python3", "-c", script, t.TempDir())
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Cursor scratchpad filtering: %v: %s", err, output)
	}
}
