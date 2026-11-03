import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


READER = Path(__file__).resolve().parent / "bin/agents-list"
GIT = ["git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"]


class ListFiltersTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.work = self.root / "work"
        self.work.mkdir()
        self.env = {"PATH": os.environ["PATH"], "HOME": str(self.root / "home"),
                    "CLAUDE_CONFIG_DIR": str(self.root / "claude"), "CODEX_HOME": str(self.root / "codex"),
                    "XDG_DATA_HOME": str(self.root / "data"), "GIT_CONFIG_GLOBAL": os.devnull,
                    "GIT_CONFIG_NOSYSTEM": "1", "PYTHONDONTWRITEBYTECODE": "1"}

    def git(self, cwd, *args):
        subprocess.run(GIT + list(args), cwd=cwd, env=self.env, check=True, capture_output=True)

    def repository(self, name):
        path = self.work / name
        path.mkdir()
        self.git(path, "init", "-q")
        self.git(path, "commit", "-q", "--allow-empty", "-m", "initial")
        return path

    def worktree(self, repository, name):
        path = self.work / name
        self.git(repository, "worktree", "add", "-q", "-b", name, str(path))
        return path

    def claude(self, session, cwd, title="", events=None, activity="2026-10-20T10:00:00Z"):
        folder = self.root / "claude/projects/project"
        folder.mkdir(parents=True, exist_ok=True)
        first = {"type": "user", "cwd": str(cwd), "customTitle": title, "timestamp": activity,
                 "message": {"role": "user", "content": "First message"}}
        lines = [first] + (events or [])
        (folder / f"{session}.jsonl").write_text("\n".join(json.dumps(line) for line in lines) + "\n")

    def codex(self, session, cwd):
        folder = self.root / "codex/sessions/2026/10"
        folder.mkdir(parents=True, exist_ok=True)
        meta = {"type": "session_meta", "payload": {"id": session, "cwd": str(cwd)}}
        (folder / f"rollout-{session}.jsonl").write_text(json.dumps(meta) + "\n")

    def run_reader(self, *args, env=None):
        return subprocess.run([sys.executable, str(READER), *args], env=env or self.env,
                              capture_output=True, text=True)

    def rows(self, *args):
        result = self.run_reader("--json", *args)
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def project_fixture(self):
        api = self.repository("api")
        feature = self.worktree(api, "api-feature")
        docs = self.repository("api-docs")
        self.claude("in-api", api, activity="2026-10-20T10:00:03Z")
        self.claude("in-worktree", feature, activity="2026-10-20T10:00:02Z")
        self.claude("in-docs", docs, activity="2026-10-20T10:00:01Z")
        return api, feature

    def test_project_matches_repository_name_including_worktrees(self):
        api, feature = self.project_fixture()
        rows = self.rows("--project", "API")
        self.assertEqual([r["id"] for r in rows], ["in-api", "in-worktree"])
        self.assertEqual({r["project"] for r in rows}, {str(api)})
        self.assertEqual({r["worktree"] for r in rows}, {str(api), str(feature)})

    def test_project_matches_full_path(self):
        api, _ = self.project_fixture()
        self.assertEqual([r["id"] for r in self.rows("--project", str(api).upper())], ["in-api", "in-worktree"])

    def test_title_does_not_match_project_directory(self):
        billing = self.work / "billing"
        billing.mkdir()
        self.claude("titled", self.work / "other", title="Billing fix")
        self.claude("in-billing", billing, title="Unrelated")
        self.assertEqual([r["id"] for r in self.rows("--title", "billing")], ["titled"])

    def test_project_on_non_git_directory_matches_cwd_basename(self):
        plain = self.work / "notes"
        plain.mkdir()
        self.claude("plain", plain)
        rows = self.rows("--project", "notes")
        self.assertEqual([(r["id"], r["project"], r["worktree"]) for r in rows], [("plain", str(plain), "")])

    def test_project_on_deleted_directory_uses_nearest_existing_parent(self):
        api = self.repository("api")
        self.claude("gone", api / "removed/deeper")
        rows = self.rows("--project", "api")
        self.assertEqual([(r["id"], r["project"], r["worktree"]) for r in rows], [("gone", str(api), str(api))])

    def test_project_combines_with_client_and_limit(self):
        api, _ = self.project_fixture()
        self.codex("codex-api", api)
        self.assertEqual([r["id"] for r in self.rows("--project", "api", "--client", "claude", "-n", "1")], ["in-api"])

    def test_no_new_flags_leaves_rows_unchanged_and_does_not_need_git(self):
        self.project_fixture()
        empty = self.root / "empty-bin"
        empty.mkdir()
        result = self.run_reader("--json", env={**self.env, "PATH": str(empty)})
        self.assertEqual(result.returncode, 0, result.stderr)
        for row in json.loads(result.stdout):
            self.assertEqual(set(row), {"client", "id", "cwd", "title", "activity", "last_activity", "resume"})

    def test_last_message_applies_after_limit_and_marks_text_untrusted(self):
        for index in range(3):
            self.claude(f"s{index}", self.work, activity=f"2026-10-20T10:00:0{index}Z", events=[
                {"type": "assistant", "message": {"id": f"m{index}", "role": "assistant", "content": "x" * 900}}])
        rows = self.rows("--last-message", "-n", "2")
        self.assertEqual([r["id"] for r in rows], ["s2", "s1"])
        for row in rows:
            self.assertTrue(row["last_message"]["untrusted"])
            self.assertEqual(row["last_message"]["role"], "assistant")
            self.assertEqual(len(row["last_message"]["text"]), 500)
            self.assertNotIn("notice", row)

    def test_session_without_text_messages_has_null_last_message(self):
        folder = self.root / "claude/projects/project"
        folder.mkdir(parents=True)
        event = {"type": "user", "cwd": str(self.work), "message": {"role": "user", "content": [{"type": "tool_result", "content": "x"}]}}
        (folder / "silent.jsonl").write_text(json.dumps(event) + "\n")
        row = self.rows("--last-message")[0]
        self.assertIsNone(row["last_message"])
        self.assertIn("No readable", row["notice"])

    def test_failing_row_gets_notice_without_changing_exit_code_or_other_rows(self):
        self.claude("good", self.work, activity="2026-10-20T10:00:02Z")
        self.claude("corrupt", self.work, activity="2026-10-20T10:00:01Z")
        (self.root / "claude/projects/project/corrupt.jsonl").write_bytes(b"\xff\xfe not json\n{")
        self.codex("bad id!", self.work)
        rows = {r["id"]: r for r in self.rows("--last-message")}
        self.assertEqual(rows["good"]["last_message"]["text"], "First message")
        self.assertIsNone(rows["corrupt"]["last_message"])
        self.assertIsNone(rows["bad id!"]["last_message"])
        self.assertEqual(rows["bad id!"]["notice"], "Invalid session ID.")

    def test_cursor_row_keeps_unavailable_notice(self):
        folder = self.root / "home/.cursor/chats/hash/cursor-1"
        folder.mkdir(parents=True)
        (folder / "meta.json").write_text(json.dumps({"cwd": str(self.work), "hasConversation": True, "updatedAtMs": 1792500000000}))
        row = self.rows("--last-message", "--client", "cursor")[0]
        self.assertIsNone(row["last_message"])
        self.assertIn("encrypted", row["notice"])

    def test_table_output_prints_cleaned_last_message_under_row(self):
        self.claude("tabular", self.work, events=[
            {"type": "assistant", "message": {"id": "m", "role": "assistant", "content": "line one\n\nline\ttwo"}}])
        result = self.run_reader("--last-message")
        self.assertEqual(result.returncode, 0, result.stderr)
        lines = result.stdout.splitlines()
        self.assertEqual(lines[-1], "  assistant: line one line two")
        self.assertIn("[tabular]", lines[-2])


if __name__ == "__main__":
    unittest.main()
