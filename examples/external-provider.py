#!/usr/bin/env python3
"""One-shot JSON-RPC Provider example for SuperFeishuSearch."""

from __future__ import annotations

import datetime as dt
import json
import sys
from typing import Any

PROVIDER_ID = "example.docs"
SOURCE = "docs"
NATIVE_ID = "external-example"


def response(request_id: int, result: Any) -> dict[str, Any]:
    return {"jsonrpc": "2.0", "id": request_id, "result": result}


def candidate(identity: dict[str, Any]) -> dict[str, Any]:
    scope = identity.get("scope_key") or "external"
    canonical = f"feishu:{scope}:document:{NATIVE_ID}"
    now = dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z")
    return {
        "ref": {
            "platform": "feishu",
            "scope_key": scope,
            "kind": "document",
            "native_id": NATIVE_ID,
            "canonical_id": canonical,
            "provider_id": PROVIDER_ID,
            "source": SOURCE,
            "url": "https://example.invalid/docs/external-example",
        },
        "source": SOURCE,
        "kind": "document",
        "title": "External Provider 示例文档",
        "snippet": "由独立 JSON-RPC 进程返回的候选。",
        "url": "https://example.invalid/docs/external-example",
        "native_rank": 1,
        "fused_score": 0,
        "projection": ["head", "snippet"],
        "available_projection": ["content"],
        "discovered_by": [{"provider_id": PROVIDER_ID, "source": SOURCE, "rank": 1}],
        "provenance": {
            "provider_id": PROVIDER_ID,
            "backend": "exec-jsonrpc",
            "operation": "search",
            "retrieved_at": now,
            "source_rank": 1,
        },
    }


def artifact(ref: dict[str, Any]) -> dict[str, Any]:
    now = dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z")
    return {
        "ref": ref,
        "projection": ["head", "content"],
        "metadata": {"example": True},
        "chunks": [{"id": "external-1", "kind": "text", "text": "这是外部 Provider 返回的正文。"}],
        "provenance": {
            "provider_id": PROVIDER_ID,
            "backend": "exec-jsonrpc",
            "operation": "fetch",
            "retrieved_at": now,
        },
    }


def main() -> int:
    req = json.load(sys.stdin)
    request_id = int(req.get("id", 1))
    method = req.get("method")
    params = req.get("params") or {}

    if method == "health":
        result = {"version": "example/1"}
    elif method == "search":
        query = str(params.get("query", "")).lower()
        item = candidate(params.get("identity") or {})
        items = [item] if not query or any(term in (item["title"] + item["snippet"]).lower() for term in query.split()) else []
        result = {"candidates": items, "has_more": False, "raw_count": len(items)}
    elif method == "fetch":
        result = [artifact(item["ref"]) for item in params]
    elif method == "query":
        result = {"candidates": [], "has_more": False, "raw_count": 0}
    elif method == "expand":
        result = []
    elif method == "resolve":
        result = []
    else:
        print(json.dumps({"jsonrpc": "2.0", "id": request_id, "error": {"code": -32601, "message": f"unknown method: {method}"}}))
        return 0

    print(json.dumps(response(request_id, result), ensure_ascii=False, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"external provider error: {exc}", file=sys.stderr)
        raise SystemExit(1)
