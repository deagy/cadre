#!/usr/bin/env python3
"""Check for and apply Cadre CLI updates.

Usage:
    cadre upgrade            # Check for updates and prompt if available
    cadre upgrade --check    # Only check, don't prompt or update
    cadre upgrade --force    # Update without confirmation
    cadre upgrade --help     # Show this help

This tool checks for newer versions available on GitHub and offers to update
the current installation using the package manager that installed Cadre
(detected automatically: pip, pipx, or source checkout).
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
from pathlib import Path
from typing import Any

try:
    import urllib.request
    import urllib.error
except ImportError as e:
    print(f"cadre upgrade: requires Python's urllib (standard library): {e}", file=sys.stderr)
    sys.exit(1)

def _find_repo_root() -> Path:
    """Find repository root by searching for cadre_cli/_version.py."""
    current = Path(__file__).resolve().parent
    for _ in range(10):  # Search up to 10 levels
        if (current / "cadre_cli" / "_version.py").is_file():
            return current
        current = current.parent
    # Fallback: assume standard location (3 levels up from src/)
    return Path(__file__).resolve().parents[3]

REPO_ROOT = _find_repo_root()


def get_installed_version() -> str:
    """Get the currently installed Cadre version."""
    import ast

    checkout_marker = REPO_ROOT / "cadre_cli" / "_version.py"
    version_marker = checkout_marker if checkout_marker.is_file() else REPO_ROOT.parent / "_version.py"

    try:
        module = ast.parse(version_marker.read_text(encoding="utf-8"), filename=str(version_marker))
    except (OSError, SyntaxError) as error:
        raise RuntimeError(f"could not read version marker: {error}") from error

    for statement in module.body:
        if not isinstance(statement, ast.Assign):
            continue
        if not any(isinstance(target, ast.Name) and target.id == "VERSION" for target in statement.targets):
            continue
        if isinstance(statement.value, ast.Constant) and isinstance(statement.value.value, str):
            return statement.value.value

    raise RuntimeError(f"could not find VERSION in {version_marker}")


def fetch_latest_release() -> tuple[str, str] | None:
    """Fetch the latest Cadre CLI version from PyPI.

    Returns (version, release_url) or None if fetch fails.
    Checks PyPI for the canonical pip/pipx distribution version.
    """
    url = "https://pypi.org/pypi/cadre/json"

    try:
        with urllib.request.urlopen(url, timeout=5) as response:
            if response.status != 200:
                return None
            data = json.loads(response.read().decode("utf-8"))
            version = data.get("info", {}).get("version", "")
            release_url = f"https://pypi.org/project/cadre/{version}/"
            return (version, release_url) if version else None
    except (urllib.error.URLError, urllib.error.HTTPError, json.JSONDecodeError, OSError):
        return None


def detect_install_method() -> str:
    """Detect how Cadre was installed: 'pipx', 'pip', or 'source'.

    Returns the detection result, which influences how we recommend updating.
    """
    # Check if running from a source checkout
    # Look for pyproject.toml and bin/cadre at repo root
    if (REPO_ROOT / "pyproject.toml").is_file() and (REPO_ROOT / "bin" / "cadre").is_file():
        return "source"
    # Also check parent directory in case we're in a plugin bundle
    if (REPO_ROOT.parent / "pyproject.toml").is_file() and (REPO_ROOT.parent / "bin" / "cadre").is_file():
        return "source"

    # Try to detect pipx installation by checking if 'cadre' command exists
    # and is managed by pipx
    try:
        result = subprocess.run(
            ["pipx", "list", "--json"],
            capture_output=True,
            text=True,
            timeout=5,
        )
        if result.returncode == 0:
            try:
                data = json.loads(result.stdout)
                if "venvs" in data and "cadre" in data["venvs"]:
                    return "pipx"
            except json.JSONDecodeError:
                pass
    except (FileNotFoundError, subprocess.TimeoutExpired):
        pass

    # Default to pip
    return "pip"


def compare_versions(current: str, latest: str) -> int:
    """Compare version strings, handling pre-release versions.

    Returns: -1 if current < latest, 0 if equal, 1 if current > latest
    """
    def parse_version(v: str) -> tuple:
        # Strip pre-release markers (rc, a, b, dev, post, etc.)
        # Examples: 1.2.3rc1 -> 1.2.3, 1.2.3a1 -> 1.2.3
        base = v.split("+")[0]  # Remove local version
        for marker in ("rc", "a", "b", "dev", "post"):
            if marker in base.lower():
                base = base.lower().split(marker)[0]
                break

        try:
            parts = tuple(int(x) for x in base.split("."))
            return parts if parts else (0,)
        except (ValueError, AttributeError):
            return (0,)

    current_parts = parse_version(current)
    latest_parts = parse_version(latest)

    if current_parts < latest_parts:
        return -1
    elif current_parts > latest_parts:
        return 1
    return 0


def prompt_update(current: str, latest: str, release_url: str) -> bool:
    """Prompt the user to confirm an update."""
    print(f"\nCadre update available!")
    print(f"  Current version: {current}")
    print(f"  Latest version:  {latest}")
    print(f"  Release notes:   {release_url}")
    print()

    try:
        response = input("Update Cadre now? (y/n) [n]: ").strip().lower()
        return response == "y"
    except (EOFError, KeyboardInterrupt):
        return False


def update_cadre(install_method: str) -> int:
    """Attempt to update Cadre using the detected installation method.

    Returns 0 on success, 1 on failure.
    """
    if install_method == "source":
        print("cadre: Running from a source checkout. To update, use:")
        print("  git pull origin main")
        print("  ./bin/cadre generate-role-metadata")
        print("  ./bin/cadre generate-plugin --output plugin")
        return 0

    if install_method == "pipx":
        print("Updating via pipx...")
        result = subprocess.run(["pipx", "upgrade", "cadre"])
        if result.returncode == 0:
            print("✓ Cadre updated successfully via pipx")
            print("  Run 'cadre --version' to verify the new version")
        return result.returncode

    # pip
    print("Updating via pip...")
    result = subprocess.run([sys.executable, "-m", "pip", "install", "--upgrade", "cadre"])
    if result.returncode == 0:
        print("✓ Cadre updated successfully via pip")
        print("  Run 'cadre --version' to verify the new version")
    return result.returncode


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(
        prog="cadre upgrade",
        description="Check for and install Cadre CLI updates",
        add_help=False,  # We'll handle help ourselves to match cadre style
    )
    parser.add_argument(
        "--check",
        action="store_true",
        help="Only check for updates, don't install",
    )
    parser.add_argument(
        "--force",
        action="store_true",
        help="Update without confirmation",
    )
    parser.add_argument(
        "-h",
        "--help",
        action="store_true",
        help="Show this help message",
    )

    try:
        args = parser.parse_args(argv)
    except SystemExit:
        return 2

    if args.help:
        parser.print_help()
        return 0

    try:
        current_version = get_installed_version()
    except RuntimeError as error:
        print(f"cadre upgrade: {error}", file=sys.stderr)
        return 1

    print(f"Current version: {current_version}")
    print("Checking for updates...")

    result = fetch_latest_release()
    if result is None:
        print("cadre upgrade: Could not reach GitHub to check for updates", file=sys.stderr)
        print("  Check your internet connection or try again later")
        return 1

    latest_version, release_url = result

    comparison = compare_versions(current_version, latest_version)

    if comparison == 0:
        print(f"✓ Cadre {current_version} is up to date")
        return 0

    if comparison > 0:
        print(f"✓ Cadre {current_version} is newer than the latest release ({latest_version})")
        return 0

    # comparison < 0: update available
    if args.check:
        print(f"Update available: {current_version} → {latest_version}")
        print(f"Release notes: {release_url}")
        return 0

    install_method = detect_install_method()

    if args.force:
        return update_cadre(install_method)

    if not prompt_update(current_version, latest_version, release_url):
        print("Update cancelled")
        return 0

    return update_cadre(install_method)


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
