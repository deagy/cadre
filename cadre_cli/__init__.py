"""Cadre CLI package."""

import sys
from pathlib import Path

_CHECKOUT_ONLY_SUBCOMMANDS = {
    "generate-plugin",
    "generate-authority-aides",
    "generate-role-metadata",  # default mode only; --check is allowed
}


def _is_checkout():
    """Check if running from checkout (has roster/ directory) vs pip/pipx install."""
    # In pip/pipx install, this package is under site-packages/cadre_cli/
    # and roster/ is vendored under cadre_cli/_vendor/roster/
    current = Path(__file__).parent
    # Installed: cadre_cli/_vendor/roster/
    # Checkout: <repo>/cadre_cli/ and <repo>/roster/
    return (current.parent / "roster").is_dir()


def _load_dispatcher():
    """Dynamically load bin/cadre.py to avoid import errors in pip environments."""
    if _is_checkout():
        repo_root = Path(__file__).parent.parent
    else:
        # Pip install: _vendor is at cadre_cli/_vendor/
        repo_root = Path(__file__).parent / "_vendor"

    bin_path = repo_root / "bin" / "cadre.py"
    if not bin_path.exists():
        raise FileNotFoundError(f"bin/cadre.py not found at {bin_path}")

    import importlib.util
    spec = importlib.util.spec_from_file_location("cadre_dispatcher", bin_path)
    dispatcher = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(dispatcher)
    return dispatcher


def main(argv=None):
    """Entry point for pip/pipx installation."""
    if argv is None:
        # pip/pipx calls main() with no arguments; get from sys.argv[1:]
        argv = sys.argv[1:]

    # Check for checkout-only commands in pip installs
    if not _is_checkout() and argv and argv[0] in _CHECKOUT_ONLY_SUBCOMMANDS:
        # Special case: generate-role-metadata --check is allowed
        if argv[0] == "generate-role-metadata" and len(argv) > 1 and argv[1] == "--check":
            pass  # Allow
        else:
            print(f"Error: cadre {argv[0]} is only available in a checkout, not in pip/pipx installs")
            return 1

    try:
        dispatcher = _load_dispatcher()
        return dispatcher.main(argv)
    except Exception as e:
        print(f"Error: {e}", file=sys.stderr)
        return 1


__all__ = ["main"]
