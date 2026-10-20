"""Private machine labels and per-user realtime invalidation."""

def extend(changes):
    changes.setdefault("packages/core/types/events.ts", []).extend([
      ('  | "daemon:register"', '  | "daemon:register"\n  | "daemon:personal-name"'),
      ('  "daemon:register": unknown;', '  "daemon:register": unknown;\n  "daemon:personal-name": { action: "update" };'),
    ])
    changes.setdefault("packages/core/api/client.ts", []).extend([
      ('    return this.fetch(`/api/runtimes?${search}`, {\n      headers: workspaceHeader(workspaceSlug),\n    });',
       '    const runtimes = await this.fetch<AgentRuntime[]>(`/api/runtimes?${search}`, {\n      headers: workspaceHeader(workspaceSlug),\n    });\n    const { withPersonalName } = await import("../runtimes/personal-name-schema");\n    return runtimes.map(withPersonalName);'),
      ('    return this.fetch(`/api/runtimes/${runtimeId}`, {\n      method: "PATCH",\n      body: JSON.stringify(patch),\n    });',
       '    const runtime = await this.fetch<AgentRuntime>(`/api/runtimes/${runtimeId}`, {\n      method: "PATCH",\n      body: JSON.stringify(patch),\n    });\n    const { withPersonalName } = await import("../runtimes/personal-name-schema");\n    return withPersonalName(runtime);'),
    ])
    changes.setdefault("packages/core/types/agent.ts", []).append((
        "  custom_name?: string | null;",
        "  custom_name?: string | null;\n  personal_name?: string | null;",
    ))
    changes.setdefault("packages/core/realtime/use-realtime-sync.ts", []).append((
        '      const prefix = msg.type.split(":")[0] ?? "";',
        '      if (msg.type === "daemon:personal-name") {\n        qc.invalidateQueries({ queryKey: ["runtimes"] });\n        return;\n      }\n      const prefix = msg.type.split(":")[0] ?? "";',
    ))
    translations = {
      "zh-Hans": ("用于这台机器及其所有运行时。", "仅对你可见，在各工作区同步。"),
      "en": None, "ja": None, "ko": None, "fr": None,
    }
    import json
    originals = {
      "zh-Hans": "用于这台机器及其所有运行时。",
      "en": "Used for this computer and all its runtimes.",
      "ja": "このコンピュータと、そのすべてのランタイムに使われます。",
      "ko": "이 컴퓨터와 모든 런타임에 사용됩니다.",
      "fr": "Utilisé pour cet ordinateur et tous ses runtimes.",
    }
    descriptions = {
      "en": "Only visible to you. Synced across workspaces.",
      "ja": "自分にのみ表示され、ワークスペース間で同期されます。",
      "ko": "나에게만 표시되며 워크스페이스 간에 동기화됩니다.",
      "fr": "Visible uniquement par vous, synchronisé entre vos espaces de travail.",
    }
    for locale in translations:
        original = originals[locale]
        translated = translations[locale][1] if translations[locale] else descriptions[locale]
        changes.setdefault(f"packages/views/locales/{locale}/runtimes.json", []).append((
            '"description": ' + json.dumps(original, ensure_ascii=False),
            '"description": ' + json.dumps(translated, ensure_ascii=False),
        ))
