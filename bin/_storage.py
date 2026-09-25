"""Single-filesystem storage primitives. Locks cover read-modify-write, not just publication."""

import fcntl
import os
import stat
import tempfile
from contextlib import contextmanager


def local_dir(path):
    directory = path.parent / "local"
    directory.mkdir(parents=True, exist_ok=True)
    # Also protect existing installs which have not rerun install-to for updated ignore rules.
    try:
        with (directory / ".gitignore").open("x") as out:
            out.write("*\n")
    except FileExistsError:
        pass

    return directory


@contextmanager
def locked(path):
    """Never unlink the lock: replacing its inode would let another writer bypass a waiter."""
    with (local_dir(path) / (path.name + ".lock")).open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        try:
            yield
        finally:
            fcntl.flock(lock, fcntl.LOCK_UN)


@contextmanager
def atomic_writer(path):
    """Publish a complete UTF-8 file, or leave the previous version intact before replacement.

    Temporary files and locks are ignored, including in older installs. A killed writer may leave an unused temporary file; readers never consume it. The local directory must be on the same filesystem as the destination. An fsync failure after replacement is reported, but the new complete file may already be visible.
    """
    directory = local_dir(path)
    fd, name = tempfile.mkstemp(prefix=path.name + ".tmp-", dir=directory)
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="") as out:
            if path.exists():
                os.fchmod(out.fileno(), stat.S_IMODE(path.stat().st_mode))

            yield out
            out.flush()
            os.fsync(out.fileno())

        os.replace(name, path)
        for parent in (path.parent, directory):
            sync_directory(parent)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def sync_directory(path):
    directory_fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(directory_fd)
    finally:
        os.close(directory_fd)
