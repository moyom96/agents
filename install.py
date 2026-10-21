#!/usr/bin/env python3
"""Build and install the session browser and its local history reader."""

import argparse
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


ROOT = Path(__file__).resolve().parent


def install_file(source, target, mode):
    target.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(prefix=".ai-sessions-", dir=target.parent)
    os.close(descriptor)
    try:
        shutil.copyfile(source, temporary)
        os.chmod(temporary, mode)
        os.replace(temporary, target)
    finally:
        Path(temporary).unlink(missing_ok=True)


def install(bin_dir, config_dir):
    go = shutil.which("go")
    if not go:
        raise RuntimeError("Go 1.26 or later is required to build. Install Go and add it to PATH.")
    with tempfile.TemporaryDirectory(prefix="ai-sessions-build-") as directory:
        binary = Path(directory) / "ai-sessions-tui"
        subprocess.run([go, "build", "-trimpath", "-o", str(binary), "."], cwd=ROOT, check=True)
        install_file(binary, bin_dir / binary.name, 0o755)
        install_file(ROOT / "bin/ai-sessions", bin_dir / "ai-sessions", 0o755)
    config = config_dir / "ai-sessions/config.toml"
    if not config.exists() and not config.is_symlink():
        install_file(ROOT / "config/config.toml", config, 0o644)
    print(f"Installed ai-sessions and ai-sessions-tui in {bin_dir}")
    print(f"Settings: {config} (existing settings preserved)")
    if not shutil.which("tmux"):
        print("Install tmux to resume sessions. Browsing does not require tmux.")
    print("Add the install directory to PATH, then run ai-sessions-tui inside tmux.")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bin-dir", type=Path, default=Path.home() / ".local/bin")
    parser.add_argument("--config-dir", type=Path, default=Path(os.environ.get("XDG_CONFIG_HOME") or Path.home() / ".config"))
    args = parser.parse_args()
    try:
        install(args.bin_dir.expanduser().resolve(), args.config_dir.expanduser().resolve())
    except (OSError, RuntimeError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"Installation failed: {error}\n")
