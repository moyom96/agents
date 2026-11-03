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

    def install(self, homes=None):
        with patch.object(installer.shutil, "which", return_value="go"), \
             patch.object(installer.subprocess, "run", side_effect=self.fake_build), \
             patch("sys.stdout", new_callable=io.StringIO) as output:
            installer.install(self.root / "bin", self.root / "config", homes)
        return output.getvalue()

    def homes(self, *names):
        homes = {name: self.root / name.lower() for name in ("Claude", "Codex")}
        for name in names:
            homes[name].mkdir()
        return homes

    def test_installs_both_commands_and_default_settings(self):
        self.install()
        self.assertEqual((self.root / "bin/agents").read_bytes(), b"built browser")
        self.assertTrue((self.root / "bin/agents-list").stat().st_mode & 0o111)
        self.assertIn('target = "tmux-window"', (self.root / "config/agents/config.toml").read_text())
        self.assertFalse((self.root / ".claude").exists())
        self.assertFalse((self.root / "config/opencode").exists())

    def test_reinstall_preserves_settings(self):
        self.install()
        config = self.root / "config/agents/config.toml"
        config.write_text('[launch]\ntarget = "terminal-tab"\n')
        self.install()
        self.assertIn("terminal-tab", config.read_text())

    def test_replaces_old_command_symlink_without_modifying_its_source(self):
        directory = self.root / "bin"
        directory.mkdir()
        source = self.root / "old-reader"
        source.write_text("old reader")
        (directory / "agents-list").symlink_to(source)
        self.install()
        self.assertEqual(source.read_text(), "old reader")
        self.assertFalse((directory / "agents-list").is_symlink())

    def test_missing_go_does_not_install_partial_commands(self):
        with patch.object(installer.shutil, "which", return_value=None):
            with self.assertRaisesRegex(RuntimeError, "Go 1.26"):
                installer.install(self.root / "bin", self.root / "config")
        self.assertFalse((self.root / "bin").exists())

    def test_skills_install_identical_copies_for_both_clients(self):
        homes = self.homes("Claude", "Codex")
        self.install(homes)
        source = (installer.ROOT / "skills/agents/SKILL.md").read_text()
        for home in homes.values():
            self.assertEqual((home / "skills/agents/SKILL.md").read_text(), source)

    def test_skills_skip_a_client_without_a_home(self):
        homes = self.homes("Claude")
        output = self.install(homes)
        self.assertTrue((homes["Claude"] / "skills/agents/SKILL.md").exists())
        self.assertFalse(homes["Codex"].exists())
        self.assertIn("Skipped Codex skill", output)

    def test_skills_replace_a_previously_installed_copy(self):
        homes = self.homes("Claude")
        target = homes["Claude"] / "skills/agents/SKILL.md"
        target.parent.mkdir(parents=True)
        target.write_text(f"old <!-- {installer.SKILL_MARKER} -->")
        self.install(homes)
        self.assertEqual(target.read_text(), (installer.ROOT / "skills/agents/SKILL.md").read_text())

    def test_skills_refuse_foreign_files_and_symlinks_before_installing(self):
        homes = self.homes("Claude")
        target = homes["Claude"] / "skills/agents/SKILL.md"
        target.parent.mkdir(parents=True)
        target.write_text("my own skill")
        with self.assertRaisesRegex(RuntimeError, "not installed by this installer"):
            self.install(homes)
        self.assertEqual(target.read_text(), "my own skill")
        self.assertFalse((self.root / "bin").exists())
        target.unlink()
        own = self.root / "own.md"
        own.write_text("linked skill")
        target.symlink_to(own)
        with self.assertRaisesRegex(RuntimeError, "symlink"):
            self.install(homes)
        self.assertEqual(own.read_text(), "linked skill")

    def test_default_install_creates_no_skill_directories(self):
        homes = self.homes("Claude", "Codex")
        self.install()
        for home in homes.values():
            self.assertFalse((home / "skills").exists())


if __name__ == "__main__":
    unittest.main()
