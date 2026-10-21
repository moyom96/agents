import json
import os
from pathlib import Path
import runpy
import sqlite3
import subprocess
import tempfile
import unittest


READER = Path(__file__).resolve().parent / "bin/agents-list"
reader = runpy.run_path(str(READER))
SESSION = "12345678-1234-1234-1234-123456789abc"


class PreviewReaderTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.home = Path(self.temp.name)

    def transcript(self, client, events):
        folder = self.home / ("projects/project" if client == "claude" else "sessions/2026/10")
        folder.mkdir(parents=True)
        path = folder / (SESSION + ".jsonl")
        path.write_text("\n".join(json.dumps(event) for event in events) + "\n{unfinished")
        return path

    def preview(self, client, session=SESSION):
        return reader["session_preview"](client, session, self.home)

    def test_claude_excludes_tools_reasoning_and_meta_and_merges_message_blocks(self):
        events = [
            {"type": "user", "timestamp": "2026-10-20T10:00:00Z", "message": {"role": "user", "content": "Question"}},
            {"type": "assistant", "message": {"id": "same", "role": "assistant", "content": [
                {"type": "thinking", "thinking": "HIDDEN"}, {"type": "text", "text": "Answer"}]}},
            {"type": "assistant", "message": {"id": "same", "role": "assistant", "content": [
                {"type": "text", "text": "More"}, {"type": "tool_use", "input": "HIDDEN"}]}},
            {"type": "user", "message": {"role": "user", "content": [{"type": "tool_result", "content": "HIDDEN"}]}},
            {"isMeta": True, "message": {"role": "user", "content": "HIDDEN"}},
            {"isSidechain": True, "message": {"role": "assistant", "content": "HIDDEN"}},
        ]
        self.transcript("claude", events)
        messages = self.preview("claude")["messages"]
        self.assertEqual([m["text"] for m in messages], ["Question", "Answer\n\nMore"])
        self.assertEqual(messages[0]["timestamp"], "2026-10-20T10:00:00+00:00")

    def test_codex_keeps_last_six_without_duplicate_event_messages(self):
        events = [{"type": "event_msg", "payload": {"type": "agent_message", "message": "duplicate"}}]
        for i in range(10):
            events.append({"type": "response_item", "payload": {"type": "message", "role": "user" if i % 2 == 0 else "assistant",
                           "content": [{"type": "output_text", "text": str(i)}]}})
        events.extend([{"type": "response_item", "payload": {"type": "reasoning", "content": "HIDDEN"}},
                       {"type": "response_item", "payload": {"type": "function_call_output", "output": "HIDDEN"}}])
        self.transcript("codex", events)
        self.assertEqual([m["text"] for m in self.preview("codex")["messages"]], list(map(str, range(4, 10))))

    def test_codex_fallback_and_long_text(self):
        self.transcript("codex", [{"type": "event_msg", "payload": {"type": "user_message", "message": "x" * 15000}}])
        message = self.preview("codex")["messages"][0]
        self.assertTrue(message["text"].endswith("[Message truncated]"))
        self.assertLess(len(message["text"]), 12100)

    def test_large_tool_tail_does_not_hide_last_text(self):
        self.transcript("claude", [
            {"message": {"role": "user", "content": [{"type": "tool_result", "content": "x" * (5 * 1024 * 1024)}]}},
            {"message": {"role": "assistant", "content": "Latest answer"}},
        ])
        self.assertEqual(self.preview("claude")["messages"][0]["text"], "Latest answer")

    def database(self):
        folder = self.home / "opencode"
        folder.mkdir()
        db = sqlite3.connect(folder / "opencode.db")
        self.addCleanup(db.close)
        return db

    def test_opencode_v2_reads_text_and_ignores_tool_only_messages(self):
        db = self.database()
        db.execute("create table session_v2 (id text)")
        db.execute("create table session_message (session_id text, type text, seq integer, time_created integer, data text)")
        for seq, role, data in [(1, "user", {"text": "Question"}),
                                (2, "assistant", {"content": [{"type": "text", "text": "Answer"}, {"type": "reasoning", "text": "HIDDEN"}]}),
                                (3, "assistant", {"content": [{"type": "tool", "text": "HIDDEN"}]}),
                                (4, "system", {"text": "HIDDEN"})]:
            db.execute("insert into session_message values (?, ?, ?, ?, ?)", (SESSION, role, seq, 1792500000000, json.dumps(data)))
        db.execute("insert into session_message values ('other', 'user', 5, 1, ?)", (json.dumps({"text": "HIDDEN"}),))
        db.commit()
        self.assertEqual([m["text"] for m in self.preview("opencode")["messages"]], ["Question", "Answer"])

    def test_opencode_legacy_reads_ordered_text_parts(self):
        db = self.database()
        db.execute("create table message (id text, session_id text, time_created integer, data text)")
        db.execute("create table part (id text, message_id text, session_id text, time_created integer, data text)")
        db.execute("insert into message values ('m', ?, 1000, ?)", (SESSION, json.dumps({"role": "assistant"})))
        for i, part in enumerate([{"type": "text", "text": "One"}, {"type": "text", "text": "Two"},
                                  {"type": "reasoning", "text": "HIDDEN"}, {"type": "text", "text": "HIDDEN", "synthetic": True}]):
            db.execute("insert into part values (?, 'm', ?, ?, ?)", (str(i), SESSION, i, json.dumps(part)))
        db.commit()
        self.assertEqual(self.preview("opencode")["messages"][0]["text"], "One\n\nTwo")

    def test_missing_invalid_and_encrypted_histories(self):
        self.assertIn("encrypted", self.preview("cursor")["notice"])
        for value in ("../oops", "*", ""):
            with self.assertRaises(ValueError):
                self.preview("claude", value)
        with self.assertRaisesRegex(ValueError, "unavailable"):
            self.preview("claude")

    def test_preview_cli_returns_structured_error(self):
        result = subprocess.run(["python3", str(READER), "--client", "claude", "--preview", SESSION, "--json"],
                                env={**os.environ, "CLAUDE_CONFIG_DIR": str(self.home)}, capture_output=True, text=True)
        self.assertEqual(result.returncode, 1)
        self.assertEqual(json.loads(result.stdout)["messages"], [])
        self.assertEqual(result.stderr, "")


if __name__ == "__main__":
    unittest.main()
