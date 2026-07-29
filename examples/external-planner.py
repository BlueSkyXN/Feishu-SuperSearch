#!/usr/bin/env python3
"""Minimal external Planner for SuperFeishuSearch.

Reads one JSON object from stdin:
  {"request": <UserRequest>, "capabilities": <CapabilitySnapshot>, ...}
Writes one retrieval-plan/v1 object to stdout.
"""

from __future__ import annotations

import json
import sys
from typing import Any


def available_search_sources(capabilities: dict[str, Any]) -> list[str]:
    preferred = ["docs", "messages", "minutes", "meetings", "tasks", "calendar", "people", "chats", "mail"]
    available: set[str] = set()
    for item in capabilities.get("providers", []):
        if item.get("status") != "ok":
            continue
        descriptor = item.get("descriptor", {})
        operations = descriptor.get("operations", {})
        if operations.get("search"):
            source = descriptor.get("source")
            if source:
                available.add(source)
    return [source for source in preferred if source in available]


def main() -> int:
    envelope = json.load(sys.stdin)
    request = envelope.get("request", {})
    query = str(request.get("query", "")).strip()
    if not query:
        raise ValueError("request.query must not be empty")

    requested_sources = request.get("sources") or []
    sources = requested_sources or available_search_sources(envelope.get("capabilities", {}))
    if not sources:
        raise ValueError("no searchable source is available")

    fetch_top = int(request.get("fetch_top_k") or 6)
    deep = bool(request.get("deep", False))
    budget = request.get("budget") or {
        "deadline_ms": 15000,
        "max_calls": 24,
        "max_pages_per_source": 2,
        "max_fetches": max(fetch_top, 8),
        "max_expanded_nodes": 8,
        "max_bytes": 16777216,
    }

    source_queries: dict[str, str] = {}
    # The Drive Search v2 query has a short limit. Keep the original question
    # outside the provider query so the kernel can still use it for ranking.
    if "docs" in sources and len(query) > 30:
        source_queries["docs"] = query[:30]

    search_request: dict[str, Any] = {
        "query": query,
        "sources": sources,
        "limit": int(request.get("limit") or 40),
        "limit_per_source": 8,
        "filters": request.get("filters") or {},
        "strategy": {
            "profile": "balanced",
            "fusion": "weighted_rrf",
            "pagination": "adaptive",
            "source_quota": True,
            "k0": 60,
        },
    }
    if source_queries:
        search_request["source_queries"] = source_queries

    nodes: list[dict[str, Any]] = [
        {
            "id": "search",
            "op": "search",
            "priority": 100,
            "retry": {"max_attempts": 2, "backoff_ms": 200, "max_backoff_ms": 1000},
            "request": search_request,
        }
    ]
    output = ["search"]

    if deep:
        nodes.append(
            {
                "id": "fetch_top",
                "op": "map_fetch",
                "depends_on": ["search"],
                "priority": 50,
                "max_items": fetch_top,
                "request": {
                    "top_k": fetch_top,
                    "max_items": fetch_top,
                    "projection_by_kind": {
                        "document": ["structure", "content"],
                        "message": ["content", "context", "relations"],
                        "minute": ["summary", "relations"],
                        "meeting": ["content", "relations"],
                        "task": ["content"],
                        "event": ["content", "relations"],
                        "mail": ["content", "context"],
                    },
                },
            }
        )
        output.append("fetch_top")

    plan = {
        "version": "retrieval-plan/v1",
        "identity": request.get("identity") or {"mode": "auto"},
        "budget": budget,
        "nodes": nodes,
        "output": output,
    }
    json.dump(plan, sys.stdout, ensure_ascii=False, separators=(",", ":"))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:  # Protocol errors belong on stderr.
        print(f"external planner error: {exc}", file=sys.stderr)
        raise SystemExit(1)
