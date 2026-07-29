#!/usr/bin/env python3
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path


GLOBAL_SOURCES = "docs,messages,chats,people,minutes,meetings,calendar,tasks,mail"


def required(name):
    value = os.environ.get(name, "").strip()
    if not value:
        raise RuntimeError(f"missing required environment variable {name}")
    return value


def run_json(command, timeout=90):
    result = subprocess.run(command, capture_output=True, text=True, timeout=timeout, check=False)
    if result.returncode != 0:
        raise RuntimeError(f"command failed with exit code {result.returncode}")
    try:
        return json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError("command returned invalid JSON") from error


def candidate_id(snapshot, kind):
    for candidate in snapshot.get("candidates", []):
        if candidate.get("kind") == kind:
            ref = candidate.get("ref", {})
            return ref.get("canonical_id") or ref.get("native_id")
    return ""


def main():
    binary = Path(sys.argv[1] if len(sys.argv) > 1 else "bin/sfs").resolve()
    profile = required("SFS_LIVE_PROFILE")
    query = required("SFS_LIVE_QUERY")
    person = required("SFS_LIVE_PERSON_QUERY")
    backend = os.environ.get("SFS_LIVE_BACKEND", "larkcli").strip()
    if backend in {"larkcli", "hybrid"}:
        executable = shutil.which(os.environ.get("SFS_LARKCLI", "lark-cli"))
        if not executable:
            raise RuntimeError("lark-cli is not available")
        env = dict(os.environ)
        env["LARKSUITE_CLI_NO_UPDATE_NOTIFIER"] = "1"
        env["LARKSUITE_CLI_NO_SKILLS_NOTIFIER"] = "1"
        auth = subprocess.run([executable, "--profile", profile, "auth", "status", "--json", "--verify"], capture_output=True, text=True, timeout=30, env=env, check=False)
        if auth.returncode != 0:
            raise RuntimeError(f"lark-cli auth verification failed with exit code {auth.returncode}")

    blockers = []
    with tempfile.TemporaryDirectory(prefix="sfs-live-") as directory:
        database = str(Path(directory) / "sfs.db")
        common = [str(binary), "--backend", backend, "--database", database, "--profile", profile, "--as", "user", "--output", "json"]
        capabilities = run_json(common + ["doctor"])
        providers = capabilities.get("providers", [])
        if not providers:
            raise RuntimeError("doctor returned no providers")

        search = run_json(common + ["search", query, "--sources", GLOBAL_SOURCES, "--strategy", "balanced", "--pages", "1"])
        runs = {item.get("source"): item for item in search.get("sources", [])}
        for source in GLOBAL_SOURCES.split(","):
            run = runs.get(source)
            if not run:
                blockers.append(f"{source}:missing_source_run")
                continue
            status = run.get("status")
            error_type = (run.get("error") or {}).get("type")
            if status in {"ok", "empty"}:
                pass
            elif status == "missing_scope":
                blockers.append(f"{source}:missing_scope")
            elif status == "unavailable" and error_type:
                blockers.append(f"{source}:{error_type}")
            else:
                blockers.append(f"{source}:{status or 'missing_status'}")
            if error_type == "parse_error":
                blockers.append(f"{source}:parse_error")

        continuations = [item for item in search.get("continuations", []) if item.get("has_more")]
        if continuations:
            sources = ",".join(item["source"] for item in continuations if item.get("source"))
            run_json(common + ["continue", "--session", search["session_id"], "--sources", sources, "--pages", "1"])

        fetch_cases = {
            "document": ("docs", "structure,content", "SFS_LIVE_DOC_ID"),
            "message": ("messages", "content,context,relations", "SFS_LIVE_MESSAGE_ID"),
            "minute": ("minutes", "summary,structure,content", "SFS_LIVE_MINUTE_ID"),
            "task": ("tasks", "content,relations", "SFS_LIVE_TASK_ID"),
        }
        for kind, (source, projection, env_name) in fetch_cases.items():
            object_id = os.environ.get(env_name, "").strip() or candidate_id(search, kind)
            if not object_id:
                blockers.append(f"{source}:missing_test_object")
                continue
            command = common + ["fetch", "--session", search["session_id"], "--id", object_id, "--source", source, "--kind", kind, "--projection", projection]
            result = run_json(command)
            items = result.get("items", [])
            if not items:
                blockers.append(f"{source}:empty_fetch_result")
                continue
            item = items[0]
            if item.get("error"):
                blockers.append(f"{source}:fetch_{item['error'].get('type', 'unknown_error')}")
            elif not item.get("artifact"):
                blockers.append(f"{source}:missing_artifact")

        resolved = run_json(common + ["resolve", person, "--source", "people", "--limit", "3"])
        if not resolved.get("refs"):
            blockers.append("people:empty_resolve")

        meeting_id = os.environ.get("SFS_LIVE_MEETING_ID", "").strip() or candidate_id(search, "meeting")
        if meeting_id:
            run_json(common + ["expand", "--session", search["session_id"], "--id", meeting_id, "--source", "meetings", "--kind", "meeting", "--depth", "2"])
        else:
            blockers.append("meetings:missing_test_object")

        base_container = os.environ.get("SFS_LIVE_BASE_CONTAINER", "").strip()
        if base_container:
            run_json(common + ["query", "--source", "base", "--container-id", base_container, "--container-kind", "base_table", "--filter", json.dumps({"keyword": query}, ensure_ascii=False)])
        else:
            blockers.append("base:missing_test_object")

        sheet_container = os.environ.get("SFS_LIVE_SHEET_CONTAINER", "").strip()
        sheet_id = os.environ.get("SFS_LIVE_SHEET_ID", "").strip()
        if sheet_container and sheet_id:
            run_json(common + ["query", "--source", "sheets", "--container-id", sheet_container, "--container-kind", "sheet", "--filter", json.dumps({"find": query, "sheet_id": sheet_id}, ensure_ascii=False)])
        else:
            blockers.append("sheets:missing_test_object")

    status_counts = {}
    for run in runs.values():
        status = run.get("status", "unknown")
        status_counts[status] = status_counts.get(status, 0) + 1
    print("live source status:", json.dumps(status_counts, sort_keys=True))
    if blockers:
        print("live acceptance blockers:", ", ".join(sorted(set(blockers))))
        raise SystemExit(3)
    print("read-only live acceptance passed")


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, subprocess.TimeoutExpired) as error:
        print(f"live acceptance failed: {error}", file=sys.stderr)
        raise SystemExit(2)
