"""Source identity shared by deployment checks and artifact preparation."""
import hashlib
import os
from pathlib import Path
import subprocess


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def inside_root(path, root):
    try:
        path.resolve().relative_to(root)
        return True
    except ValueError:
        return False


def source_files(root):
    names = subprocess.check_output(
        ["git", "ls-files", "-z", "--cached", "--others", "--exclude-standard"], cwd=root)
    return [Path(os.fsdecode(name)) for name in sorted(set(names.split(b"\0")) - {b""})]


def index_source_copy(root):
    # Shared checks enumerate repository inputs. Index this isolated copy before
    # adding dependencies/build outputs; do not copy the user's Git state.
    subprocess.run(["git", "init", "-q"], cwd=root, check=True)
    subprocess.run(["git", "add", "--force", "."], cwd=root, check=True)


def source_digest(root, files):
    root = root.resolve()
    digest = hashlib.sha256()
    for relative in files:
        path = root / relative
        digest.update(os.fsencode(str(relative)) + b"\0")
        if not path.exists() and not path.is_symlink():
            digest.update(b"deleted\0")
            continue
        require(not path.is_symlink() or inside_root(path, root),
                "Source symlink leaves the repository: " + str(relative))
        digest.update(str(path.lstat().st_mode).encode() + b"\0")
        digest.update(os.fsencode(os.readlink(path)) if path.is_symlink() else path.read_bytes())
        digest.update(b"\0")
    return digest.hexdigest()


if __name__ == "__main__":
    root = Path(__file__).resolve().parents[1]
    print(source_digest(root, source_files(root)))
