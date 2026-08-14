"""Cadre CLI package entry point.

In a checkout, the main CLI dispatcher is at ../bin/cadre.py.
In an installed wheel, it's vendored at ./_vendor/bin/cadre.py (via pyproject.toml force-include).
"""
import importlib.util
import sys
from pathlib import Path

def _load_dispatcher() -> object:
    """Load the cadre CLI main dispatcher from bin/cadre.py.

    Handles both checkout layout (../bin/cadre.py) and wheel layout (_vendor/bin/cadre.py).
    """
    cadre_cli = Path(__file__).resolve().parent
    checkout_root = cadre_cli.parent

    # Try checkout layout first
    dispatcher_path = checkout_root / "bin" / "cadre.py"
    if not dispatcher_path.is_file():
        # Try wheel layout
        dispatcher_path = cadre_cli / "_vendor" / "bin" / "cadre.py"

    if not dispatcher_path.is_file():
        raise RuntimeError(
            f"Cadre CLI dispatcher not found. Tried:\n"
            f"  {checkout_root / 'bin' / 'cadre.py'}\n"
            f"  {cadre_cli / '_vendor' / 'bin' / 'cadre.py'}\n"
            "This package may be corrupted."
        )

    # Load cadre.py as a module
    spec = importlib.util.spec_from_file_location("_cadre_dispatcher", dispatcher_path)
    if not spec or not spec.loader:
        raise RuntimeError(f"Could not create module spec for {dispatcher_path}")

    dispatcher = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(dispatcher)
    return dispatcher

# Load the dispatcher module
_dispatcher = _load_dispatcher()

# Wrap the dispatcher's main function for pip's entry point
# pip calls main() with no arguments, but bin/cadre.py's main(argv) requires argv
def main() -> int:
    """Entry point for pip/pipx installations.

    The dispatcher's main function requires an argv parameter, but pip's entry point
    mechanism calls this with no arguments. We extract sys.argv[1:] (all arguments
    except the program name) and pass it through.
    """
    import sys
    return _dispatcher.main(sys.argv[1:])

__all__ = ["main"]
