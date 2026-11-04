#!/usr/bin/env python3
"""Build a source-free release from an explicit set of project files."""
import argparse
import gzip
import hashlib
import io
import os
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]
TARGETS = ("linux-amd64", "linux-arm64", "darwin-arm64")
DOCS = ("agent-workflow.md", "api.md", "assets.md", "browser-access.md",
        "content-model.md", "github.md", "implementation.md", "import.md",
        "linux-service.md", "roadmap.md", "scoped-access-design.md",
        "storage.md")


def package(archive: Path, binary: Path, version: str, target: str) -> None:
    if target not in TARGETS:
        raise ValueError(f"unknown release target: {target}")
    prefix = f"veda-{version}-{target}"
    files = [Path("README.md"), Path("LICENSE"),
             Path("web/public/assets/fonts/LICENSE"),
             Path("web/public/assets/icons/LICENSE")]
    if target.startswith("linux-"):
        files.append(Path("deploy/systemd/veda.service"))
    files.extend(Path("docs") / name for name in DOCS)
    with tempfile.NamedTemporaryFile(dir=archive.parent, prefix=".veda-release-", delete=False) as tmp:
        temporary = Path(tmp.name)
        try:
            with gzip.GzipFile(filename="", mode="wb", fileobj=tmp, mtime=0) as compressed:
                with tarfile.open(fileobj=compressed, mode="w", format=tarfile.PAX_FORMAT) as tar:
                    def add(name: str, data: bytes, mode: int) -> None:
                        entry = tarfile.TarInfo(f"{prefix}/{name}")
                        entry.size, entry.mode, entry.mtime = len(data), mode, 0
                        entry.uid = entry.gid = 0
                        entry.uname = entry.gname = ""
                        tar.addfile(entry, io.BytesIO(data))
                    add("veda", binary.read_bytes(), 0o755)
                    for relative in files:
                        path = ROOT / relative
                        if any((ROOT / Path(*relative.parts[:i])).is_symlink()
                               for i in range(1, len(relative.parts) + 1)) or not path.is_file():
                            raise ValueError(f"release file missing or symlink: {relative}")
                        add(relative.as_posix(), path.read_bytes(), 0o644)
            tmp.flush()
            os.fsync(tmp.fileno())
        except BaseException:
            temporary.unlink(missing_ok=True)
            raise
    os.replace(temporary, archive)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True, help="Release label, e.g. 0.1.0 or dev")
    parser.add_argument("--target", choices=TARGETS, required=True, help="GOOS-GOARCH of the archive")
    parser.add_argument("--output", type=Path, default=ROOT / "dist")
    args = parser.parse_args()
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,63}", args.version):
        parser.error("invalid version label")
    args.output.mkdir(parents=True, exist_ok=True)
    subprocess.run(["make", "frontend"], cwd=ROOT, check=True)
    with tempfile.TemporaryDirectory(prefix="veda-build-") as tmp:
        binary = Path(tmp) / "veda"
        goos, goarch = args.target.split("-")
        env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED="0")
        subprocess.run(["go", "build", "-trimpath", "-o", str(binary), "./cmd/veda"],
                       cwd=ROOT, env=env, check=True)
        archive = args.output / f"veda-{args.version}-{args.target}.tar.gz"
        package(archive, binary, args.version, args.target)
    with archive.open("rb") as f:
        digest = hashlib.file_digest(f, "sha256").hexdigest()
    print(f"{digest}  {archive}")


if __name__ == "__main__":
    main()
