import fcntl
import os
import tempfile
import logging
from contextlib import contextmanager

logger = logging.getLogger(__name__)


class ProcessLock:
    """File-based process lock to coordinate between multiple workers"""

    def __init__(self, lock_name, lock_dir=None):
        self.lock_name = lock_name
        self.lock_dir = lock_dir or tempfile.gettempdir()
        self.lock_file_path = os.path.join(self.lock_dir, f"{lock_name}.lock")
        self.lock_file = None

    def acquire(self, timeout=0):
        """
        Acquire the lock. Returns True if successful, False otherwise.

        Args:
            timeout: Not used in this implementation (non-blocking)
        """
        try:
            # Open the lock file
            self.lock_file = open(self.lock_file_path, "w")

            # Try to acquire exclusive lock (non-blocking)
            fcntl.flock(self.lock_file.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)

            # Write process info for debugging
            self.lock_file.write(f"PID: {os.getpid()}\n")
            self.lock_file.flush()

            logger.info(f"Acquired process lock: {self.lock_name}")
            return True

        except (IOError, OSError) as e:
            # Lock is already held by another process
            if self.lock_file:
                self.lock_file.close()
                self.lock_file = None
            logger.debug(f"Could not acquire lock {self.lock_name}: {e}")
            return False

    def release(self):
        """Release the lock"""
        if self.lock_file:
            try:
                fcntl.flock(self.lock_file.fileno(), fcntl.LOCK_UN)
                self.lock_file.close()
                logger.info(f"Released process lock: {self.lock_name}")
            except (IOError, OSError) as e:
                logger.warning(f"Error releasing lock {self.lock_name}: {e}")
            finally:
                self.lock_file = None
                # Clean up lock file
                try:
                    os.unlink(self.lock_file_path)
                except (IOError, OSError):
                    pass  # File might not exist or be accessible

    def is_locked(self):
        """Check if the lock is currently held"""
        return self.lock_file is not None

    def __enter__(self):
        """Context manager entry"""
        if not self.acquire():
            raise RuntimeError(f"Could not acquire lock: {self.lock_name}")
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        """Context manager exit"""
        self.release()


@contextmanager
def process_lock(lock_name, lock_dir=None):
    """
    Context manager for process locking

    Usage:
        try:
            with process_lock("gate_counter"):
                # Only one process will execute this block
                do_work()
        except RuntimeError:
            # Lock could not be acquired
            logger.info("Another process is already running")
    """
    lock = ProcessLock(lock_name, lock_dir)
    if not lock.acquire():
        raise RuntimeError(f"Could not acquire lock: {lock_name}")

    try:
        yield lock
    finally:
        lock.release()
