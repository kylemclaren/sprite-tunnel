#!/usr/bin/env python3
"""Build release archives and checksums without third-party packaging tools."""
import argparse
import hashlib
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parent.parent
TARGETS = [(system, arch) for system in ("linux", "darwin", "windows") for arch in ("amd64", "arm64")]


def git(*args):
    return subprocess.check_output(["git", *args], cwd=ROOT, text=True).strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    args = parser.parse_args()
    version = args.version
    if not re.fullmatch(r"v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?", version):
        parser.error("--version must be a semantic-version tag, such as v0.1.0")
    changelog = (ROOT / "CHANGELOG.md").read_text()
    section = re.search(r"^## " + re.escape(version) + r"\s*\n(.*?)(?=^## |\Z)", changelog, re.M | re.S)
    if not section:
        parser.error(f"CHANGELOG.md needs a '## {version}' section")
    commit = git("rev-parse", "HEAD")
    date = git("show", "-s", "--format=%cI", "HEAD")
    dist = ROOT / "dist"
    dist.mkdir(exist_ok=True)
    archives = []
    flags = f"-s -w -X main.version={version} -X main.commit={commit} -X main.date={date}"
    for system, arch in TARGETS:
        print(f"Building {system}/{arch}", flush=True)
        with tempfile.TemporaryDirectory(prefix="sprite-tunnel-release-") as tmp:
            folder = Path(tmp)
            binary = folder / ("sprite-tunnel.exe" if system == "windows" else "sprite-tunnel")
            env = dict(os.environ, GOOS=system, GOARCH=arch, CGO_ENABLED="0")
            subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", "-ldflags", flags,
                            "-o", str(binary), "."], cwd=ROOT, env=env, check=True)
            if (system, arch) != ("linux", "amd64"):
                shutil.copy2(dist / "smoke" / "sprite-tunnel", folder / "sprite-tunnel-linux-amd64")
            for name in ("README.md", "CHANGELOG.md"):
                shutil.copy2(ROOT / name, folder / name)
            suffix = "zip" if system == "windows" else "tar.gz"
            archive = dist / f"sprite-tunnel_{version}_{system}_{arch}.{suffix}"
            files = sorted(folder.iterdir())
            if suffix == "zip":
                with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED) as out:
                    for path in files:
                        out.write(path, path.name)
            else:
                with tarfile.open(archive, "w:gz") as out:
                    for path in files:
                        out.add(path, arcname=path.name)
            if (system, arch) == ("linux", "amd64"):
                (dist / "smoke").mkdir(exist_ok=True)
                shutil.copy2(binary, dist / "smoke" / "sprite-tunnel")
            archives.append(archive)
    sums = "".join(f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n" for path in archives)
    (dist / "checksums.txt").write_text(sums)
    (dist / "release-notes.md").write_text(section.group(1).strip() + "\n")
    print(f"Built {len(archives)} archives in {dist}")


if __name__ == "__main__":
    main()
