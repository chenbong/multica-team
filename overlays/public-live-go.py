"""Generate Go query constants and the narrow Handler field without editing upstream."""
from pathlib import Path


def extend(upstream, files, entries):
    root = Path(__file__).resolve().parent.parent
    relative = "server/internal/handler/handler.go"
    result = [(name, source) for name, source in entries if name != relative]
    base = files / relative
    text = next((source for name, source in entries if name == relative), (base if base.is_file() else upstream / relative).read_text())
    anchor = "\tTaskService            *service.TaskService\n"
    if text.count(anchor) != 1:
        raise SystemExit("Public live Handler anchor changed")
    result.append((relative, text.replace(anchor, anchor + "\tPublicLive             *service.PublicLiveService\n")))
    declarations = ["package service\n"]
    for section in ("realtime", "recent", "history"):
        sql = (root / "stats/live" / (section + ".sql")).read_text()
        lines = [line for line in sql.splitlines() if not line.startswith(("--", "BEGIN TRANSACTION", "SET LOCAL", "COMMIT"))]
        query = "\n".join(lines).strip().rstrip(";")
        if "`" in query:
            raise SystemExit("Unexpected raw-string delimiter in public query")
        declarations.append("const publicLive" + section.title() + "SQL = `" + query + "`\n")
    result.append(("server/internal/service/public_live_queries.go", "\n".join(declarations)))
    result.append(("server/internal/handler/reserved_slugs.json", (files / "server/internal/handler/reserved_slugs.json").read_text()))
    return result
