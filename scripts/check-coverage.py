#!/usr/bin/env python3
import argparse
from pathlib import Path


CORE_PREFIXES = (
    "internal/engine/",
    "internal/planexec/",
    "internal/research/",
    "internal/budget/",
    "internal/session/",
    "internal/planvalidate/",
    "synthesis/",
    "planner/",
)


def load(path: Path):
    packages = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line or line.startswith("mode:"):
            continue
        location, statements, count = line.rsplit(" ", 2)
        filename = location.split(":", 1)[0]
        marker = "Feishu-SuperSearch/"
        relative = filename.split(marker, 1)[-1]
        package = relative.rsplit("/", 1)[0] + "/"
        total = int(statements)
        covered = total if int(count) > 0 else 0
        current = packages.setdefault(package, [0, 0])
        current[0] += covered
        current[1] += total
    return packages


def percent(covered, total):
    return 100.0 * covered / total if total else 100.0


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("profile", type=Path)
    parser.add_argument("--total", type=float, default=65.0)
    parser.add_argument("--core", type=float, default=75.0)
    args = parser.parse_args()
    packages = load(args.profile)
    total_covered = sum(value[0] for value in packages.values())
    total_statements = sum(value[1] for value in packages.values())
    core = [value for name, value in packages.items() if name.startswith(CORE_PREFIXES)]
    core_covered = sum(value[0] for value in core)
    core_statements = sum(value[1] for value in core)
    total_percent = percent(total_covered, total_statements)
    core_percent = percent(core_covered, core_statements)
    print(f"coverage total={total_percent:.1f}% ({total_covered}/{total_statements})")
    print(f"coverage core={core_percent:.1f}% ({core_covered}/{core_statements})")
    failed = []
    if total_percent + 1e-9 < args.total:
        failed.append(f"total {total_percent:.1f}% < {args.total:.1f}%")
    if core_percent + 1e-9 < args.core:
        failed.append(f"core {core_percent:.1f}% < {args.core:.1f}%")
    if failed:
        raise SystemExit("coverage gate failed: " + "; ".join(failed))


if __name__ == "__main__":
    main()
