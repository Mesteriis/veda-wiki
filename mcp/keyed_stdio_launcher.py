"""Send a private key over a trusted child's stdin before relaying MCP stdio.

The configured child may be ssh; its command is trusted administrator input.
Neither the key nor MCP messages are logged or passed through argv/environment.
"""
import argparse
import os
import stat
import subprocess
import sys


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--key-file", required=True)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command:
        parser.error("a trusted child command is required")
    fd = os.open(args.key_file, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, "rb") as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) & 0o077:
            raise ValueError("Key must be an owner-only regular file")
        token = stream.read(65)
    if len(token) != 64 or any(c not in b"0123456789abcdef" for c in token):
        raise ValueError("Invalid key")
    child = subprocess.Popen(command, stdin=subprocess.PIPE)
    try:
        child.stdin.write(token)
        child.stdin.flush()
        while chunk := os.read(sys.stdin.fileno(), 65536):
            child.stdin.write(chunk)
            child.stdin.flush()
    except BrokenPipeError:
        pass
    finally:
        try:
            child.stdin.close()
        except BrokenPipeError:
            pass
        try:
            child.wait(timeout=5)
        except subprocess.TimeoutExpired:
            child.terminate()
            try:
                child.wait(timeout=5)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait(timeout=5)
    return child.returncode


if __name__ == "__main__":
    raise SystemExit(main())
