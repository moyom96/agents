import importlib.util
import io
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("installer", Path(__file__).with_name("install.py"))
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


class InstallerTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def fake_build(self, args, **kwargs):
        Path(args[args.index("-o") + 1]).write_bytes(b"built browser")

    def install(self):
        with patch.object(installer.shutil, "which", return_value="go"), \
             patch.object(installer.subprocess, "run", side_effect=self.fake_build), \
             patch("sys.stdout", new_callable=io.StringIO):
            installer.install(self.root / "bin", self.root / "config")

    def test_installs_both_commands_and_default_settings(self):
        self.install()
        self.assertEqual((self.root / "bin/ai-sessions-tui").read_bytes(), b"built browser")
        self.assertTrue((self.root / "bin/ai-sessions").stat().st_mode & 0o111)
        self.assertIn('target = "tmux-window"', (self.root / "config/ai-sessions/config.toml").read_text())
        self.assertFalse((self.root / ".claude").exists())
        self.assertFalse((self.root / "config/opencode").exists())

    def test_reinstall_preserves_settings(self):
        self.install()
        config = self.root / "config/ai-sessions/config.toml"
        config.write_text('[launch]\ntarget = "terminal-tab"\n')
        self.install()
        self.assertIn("terminal-tab", config.read_text())

    def test_replaces_old_command_symlink_without_modifying_its_source(self):
        directory = self.root / "bin"
        directory.mkdir()
        source = self.root / "old-reader"
        source.write_text("old reader")
        (directory / "ai-sessions").symlink_to(source)
        self.install()
        self.assertEqual(source.read_text(), "old reader")
        self.assertFalse((directory / "ai-sessions").is_symlink())

    def test_missing_go_does_not_install_partial_commands(self):
        with patch.object(installer.shutil, "which", return_value=None):
            with self.assertRaisesRegex(RuntimeError, "Go 1.26"):
                installer.install(self.root / "bin", self.root / "config")
        self.assertFalse((self.root / "bin").exists())


if __name__ == "__main__":
    unittest.main()
