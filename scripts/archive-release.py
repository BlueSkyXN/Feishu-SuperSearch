#!/usr/bin/env python3
import argparse
import datetime
import gzip
import os
import stat
import tarfile
import zipfile
from pathlib import Path


def paths(root):
    return sorted([root, *root.rglob("*")], key=lambda item: item.relative_to(root.parent).as_posix())


def make_tar(root, output, epoch):
    with output.open("wb") as raw:
        with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=epoch, compresslevel=9) as compressed:
            with tarfile.open(fileobj=compressed, mode="w", format=tarfile.PAX_FORMAT) as archive:
                for path in paths(root):
                    name = path.relative_to(root.parent).as_posix()
                    info = archive.gettarinfo(str(path), arcname=name)
                    info.uid = 0
                    info.gid = 0
                    info.uname = "root"
                    info.gname = "root"
                    info.mtime = epoch
                    if info.isfile():
                        with path.open("rb") as source:
                            archive.addfile(info, source)
                    else:
                        archive.addfile(info)


def make_zip(root, output, epoch):
    minimum = datetime.datetime(1980, 1, 1, tzinfo=datetime.timezone.utc)
    timestamp = max(datetime.datetime.fromtimestamp(epoch, datetime.timezone.utc), minimum)
    date_time = (timestamp.year, timestamp.month, timestamp.day, timestamp.hour, timestamp.minute, timestamp.second)
    with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
        for path in paths(root):
            name = path.relative_to(root.parent).as_posix()
            if path.is_dir():
                name += "/"
            info = zipfile.ZipInfo(name, date_time=date_time)
            info.create_system = 3
            mode = path.stat().st_mode
            info.external_attr = (stat.S_IMODE(mode) | (stat.S_IFDIR if path.is_dir() else stat.S_IFREG)) << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            archive.writestr(info, b"" if path.is_dir() else path.read_bytes())


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--epoch", type=int, required=True)
    args = parser.parse_args()
    root = args.input.resolve()
    output = args.output.resolve()
    output.parent.mkdir(parents=True, exist_ok=True)
    os.environ["TZ"] = "UTC"
    if output.name.endswith(".tar.gz"):
        make_tar(root, output, args.epoch)
    elif output.suffix == ".zip":
        make_zip(root, output, args.epoch)
    else:
        raise SystemExit("output must end in .tar.gz or .zip")


if __name__ == "__main__":
    main()
