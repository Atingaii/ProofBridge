#!/usr/bin/env python3
"""Publish signed, per-platform managed Bridge releases. Requires OpenSSL 3."""
import argparse
import base64
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile


def public_pin(key):
    public = subprocess.check_output(["openssl", "pkey", "-in", key, "-pubout", "-outform", "DER"])
    prefix = bytes.fromhex("302a300506032b6570032100")
    if len(public) != len(prefix) + 32 or not public.startswith(prefix):
        raise ValueError("an Ed25519 private key is required")
    return base64.b64encode(public[len(prefix):]).decode()


def keygen(key):
    fd = os.open(key, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    os.close(fd)
    try:
        subprocess.run(["openssl", "genpkey", "-algorithm", "ED25519", "-out", key], check=True)
        print(public_pin(key))
    except BaseException:
        key.unlink(missing_ok=True)
        raise


def validate_release(args):
    directory = args.directory.resolve()
    if args.key.resolve().is_relative_to(directory) or args.key.stat().st_mode & 0o077:
        raise ValueError("keep the private signing key outside downloads and readable only by its owner")
    if not (0 < args.sequence < (1 << 63)) or not re.fullmatch(r"[a-zA-Z0-9._+-]{1,128}", args.version):
        raise ValueError("invalid release sequence or version")


def publish(args):
    validate_release(args)
    public_pin(args.key)
    directory = args.directory.resolve()
    bootstraps = args.bootstrap_directory
    if bootstraps and args.key.resolve().is_relative_to(bootstraps.resolve()):
        raise ValueError("keep the signing key outside the bootstrap download directory")
    directory.mkdir(mode=0o755, parents=True, exist_ok=True)
    channel = directory / (args.platform + ".json")
    with (directory / ".publisher.lock").open("a") as lock, tempfile.TemporaryDirectory(dir=directory) as tmp:
        fcntl.flock(lock, fcntl.LOCK_EX)
        root = Path(tmp)
        artifact = root / "payload"
        digest = hashlib.sha256()
        with args.binary.open("rb") as source, artifact.open("xb") as target:
            while chunk := source.read(1 << 20):
                target.write(chunk)
                digest.update(chunk)
            target.flush()
            os.fsync(target.fileno())
        size = artifact.stat().st_size
        if not (0 < size <= 256 << 20):
            raise ValueError("invalid artifact size")
        release = dict(schema=1, sequence=args.sequence, version=args.version, platform=args.platform,
                       size=size, sha256=digest.hexdigest())
        if channel.exists():
            previous = json.loads(base64.b64decode(json.loads(channel.read_bytes())["payload"], validate=True))
            if args.sequence <= previous["sequence"] and previous != release:
                raise ValueError("new publications must increase the release sequence")
        payload = json.dumps(release, separators=(",", ":")).encode()
        message = root / "message"
        message.write_bytes(payload)
        signature = subprocess.check_output(["openssl", "pkeyutl", "-sign", "-rawin", "-inkey", args.key, "-in", message])
        manifest = root / "signed"
        with manifest.open("x") as stream:
            json.dump(dict(payload=base64.b64encode(payload).decode(), signature=base64.b64encode(signature).decode()), stream)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        # Cached endpoints may still be fetching a previous signed payload.
        os.chmod(artifact, 0o644)
        os.chmod(manifest, 0o644)
        artifact.replace(directory / (release["sha256"] + ".bin"))
        manifest.replace(channel)
        if bootstraps:
            bootstraps.mkdir(mode=0o755, parents=True, exist_ok=True)
            with tempfile.TemporaryDirectory(dir=bootstraps) as staging:
                bootstrap = Path(staging) / "bootstrap"
                shutil.copyfile(directory / (release["sha256"] + ".bin"), bootstrap)
                bootstrap.chmod(0o755)
                bootstrap.replace(bootstraps / ("codex-bridge-" + args.platform))
        fd = os.open(directory, os.O_RDONLY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
        print(f'{args.platform} {args.sequence} {args.version} {release["sha256"]}')


def build_release(args):
    validate_release(args)
    platform, arch = args.platform.split("-")
    env = dict(os.environ, CGO_ENABLED="0", GOOS=platform, GOARCH=arch)
    pin = public_pin(args.key)
    flags = f"-s -w -X main.Version={args.version} -X main.BridgeReleaseSequence={args.sequence} -X main.BridgeReleasePublicKey={pin}"
    with tempfile.TemporaryDirectory() as staging:
        args.binary = Path(staging) / "codex-bridge"
        subprocess.run([args.go, "build", "-p", "2", "-trimpath", "-buildvcs=false", "-ldflags", flags, "-o", args.binary, "."], check=True, env=env)
        publish(args)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="operation", required=True)
    gen = sub.add_parser("keygen")
    gen.add_argument("--key", type=Path, required=True)
    common = argparse.ArgumentParser(add_help=False)
    common.add_argument("--key", type=Path, required=True)
    common.add_argument("--directory", type=Path, required=True)
    common.add_argument("--bootstrap-directory", type=Path)
    common.add_argument("--platform", choices=["linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64"], required=True)
    common.add_argument("--sequence", type=int, required=True)
    common.add_argument("--version", required=True)
    pub = sub.add_parser("publish", parents=[common])
    pub.add_argument("--binary", type=Path, required=True)
    build = sub.add_parser("build", parents=[common])
    build.add_argument("--go", default="go")
    args = parser.parse_args()
    if args.operation == "keygen":
        keygen(args.key)
    elif args.operation == "build":
        build_release(args)
    else:
        publish(args)
