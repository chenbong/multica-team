"""Pair query-source / SQLc defaults for container-hosted built-in agents.

This deployment explicitly runs its runtimes in containers. IS_SANDBOX is a
Claude/ducc opt-in marker, not an isolation mechanism or a daemon-global flag.
"""
import re


def rewrite(source):
    for name in ("CreateAgentBuilder", "CreateSystemUserAgent"):
        pattern = rf"(?ms)(-- name: {name} :one\n)(.*?)(?=^-- name:|^`|\Z)"
        matches = list(re.finditer(pattern, source))
        if len(matches) != 1:
            raise SystemExit(f"built-in environment query anchor changed: {name}")
        match = matches[0]
        query = match.group()
        before = "'{}'::jsonb, '[]'::jsonb,"
        if query.count(before) != 1:
            raise SystemExit(f"built-in environment default anchor changed: {name}")
        after = """'{"IS_SANDBOX":"1"}'::jsonb, '[]'::jsonb,"""
        # This generic product-agent constructor currently creates Mika. Do
        # not change the environment of any unrelated future system-key type.
        if name == "CreateSystemUserAgent":
            key = "@system_key" if "@system_key" in query else "$12"
            after = f"CASE WHEN {key} = 'mika' THEN '{{\"IS_SANDBOX\":\"1\"}}'::jsonb ELSE '{{}}'::jsonb END, '[]'::jsonb,"
        query = query.replace(before, after, 1)
        source = source[:match.start()] + query + source[match.end():]
    return source


def extend(upstream, generated):
    # Validate the paired SQL source even though builds consume checked-in
    # SQLc constants. Compose AFTER existing agent.sql.go transfer guards.
    rewrite((upstream / "server/pkg/db/queries/agent.sql").read_text())
    relative = "server/pkg/db/generated/agent.sql.go"
    found = 0
    result = []
    for rel, text in generated:
        if rel == relative:
            text = rewrite(text)
            found += 1
        result.append((rel, text))
    if found != 1:
        raise SystemExit("expected one composed agent SQLc overlay")
    return result
