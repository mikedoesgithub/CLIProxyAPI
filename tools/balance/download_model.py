#!/usr/bin/env python3
"""Download an immutable MIT-licensed model snapshot and verify each SHA256."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import tempfile
import urllib.request


def sha256_file(path):
    with open(path, 'rb') as source:
        return hashlib.file_digest(source, 'sha256').hexdigest()


def download(directory):
    manifest = json.loads(Path(__file__).with_name('model-manifest.json').read_text())
    root = Path(directory)
    root.mkdir(parents=True, exist_ok=True)
    for name, expected in manifest['files'].items():
        target = root / name
        target.parent.mkdir(parents=True, exist_ok=True)
        if target.is_file() and sha256_file(target) == expected['sha256']:
            continue
        url = f"https://huggingface.co/{manifest['repository']}/resolve/{manifest['revision']}/{name}"
        temporary = None
        try:
            with urllib.request.urlopen(url, timeout=60) as response, tempfile.NamedTemporaryFile(dir=target.parent, delete=False) as output:
                temporary = output.name
                digest = hashlib.sha256()
                size = 0
                while chunk := response.read(1024 * 1024):
                    size += len(chunk)
                    if size > expected['bytes']:
                        raise ValueError(f'Unexpected size: {name}')
                    digest.update(chunk); output.write(chunk)
            if size != expected['bytes'] or digest.hexdigest() != expected['sha256']:
                raise ValueError(f'Integrity check failed: {name}')
            os.chmod(temporary, 0o600)
            os.replace(temporary, target)
        finally:
            if temporary and os.path.exists(temporary):
                os.unlink(temporary)
        print(f'Verified {name}')


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('directory')
    download(parser.parse_args().directory)
