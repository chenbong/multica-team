"""OneTool anonymous package import and provenance, applied only to a stage."""
import json


def extend(changes):
    def add(path, before, after):
        changes.setdefault(path, []).append((before, after))

    form = "packages/views/skills/components/create-skill-dialog.tsx"
    add(form, 'import { isNameConflictError } from "../lib/utils";\n',
        'import { isNameConflictError } from "../lib/utils";\n'
        'import { ONETOOL_BROWSE_URL, normalizeOneToolInput, isOneToolInput, isOneToolDownloadUrl } from "../lib/onetool";\n')
    add(form, 'type DetectedSource = "clawhub" | "skills.sh" | "github" | null;',
        'type DetectedSource = "clawhub" | "skills.sh" | "github" | "onetool" | null;')
    add(form, 'function detectUrlSource(url: string): DetectedSource {\n',
        'function detectUrlSource(url: string): DetectedSource {\n  if (isOneToolInput(url)) return "onetool";\n')
    add(form, '    const trimmed = url.trim();\n    if (!trimmed) return;\n',
        '    const trimmed = normalizeOneToolInput(url);\n    if (!trimmed) return;\n'
        '    if (isOneToolInput(trimmed) && !isOneToolDownloadUrl(trimmed)) {\n'
        '      setError(t(($) => $.create.url.onetool_hint));\n      return;\n    }\n')
    add(form, '    if (source === "github") return t(($) => $.create.url.importing_github);\n',
        '    if (source === "github") return t(($) => $.create.url.importing_github);\n'
        '    if (source === "onetool") return t(($) => $.create.url.importing_onetool);\n')
    add(form, '<div className="grid grid-cols-3 gap-2">', '<div className="grid grid-cols-2 gap-2 sm:grid-cols-4">')
    anchor = '              active={source === "github"}\n            />\n          </div>\n'
    add(form, anchor, '              active={source === "github"}\n            />\n'
        '            <SourceCard\n              label="OneTool"\n'
        '              exampleHost="console.cloud.baidu-int.com/onetool/skills"\n'
        '              browseUrl={ONETOOL_BROWSE_URL}\n              active={source === "onetool"}\n            />\n'
        '          </div>\n          <p className="mt-2 text-caption text-muted-foreground">{t(($) => $.create.url.onetool_hint)}</p>\n')

    origin = "packages/views/skills/lib/origin.ts"
    add(origin, 'import type { Skill, SkillSummary } from "@multica/core/types";\n',
        'import type { Skill, SkillSummary } from "@multica/core/types";\nimport { isOneToolDownloadUrl, normalizeOneToolInput } from "./onetool";\n')
    add(origin, '"skills_sh" | "github" | "manual";', '"skills_sh" | "github" | "onetool" | "manual";')
    add(origin, '  if (raw?.type === "github") return raw;\n', '  if (raw?.type === "github") return raw;\n  if (raw?.type === "onetool") return raw;\n')
    add(origin, 'origin.type === "skills_sh" || origin.type === "clawhub")',
        'origin.type === "skills_sh" || origin.type === "clawhub" || origin.type === "onetool")')
    add(origin, '  if (!origin) return null;\n',
        '  if (!origin) return null;\n  if (origin.type === "onetool") return typeof origin.source_url === "string" && isOneToolDownloadUrl(origin.source_url) ? normalizeOneToolInput(origin.source_url) : null;\n')
    store = "packages/core/skills/stores/view-store.ts"
    add(store, '  | "github";\n', '  | "github"\n  | "onetool";\n')
    toolbar = "packages/views/skills/components/skill-list-toolbar.tsx"
    add(toolbar, '  "github",\n];', '  "github",\n  "onetool",\n];')
    add(toolbar, '    github: t(($) => $.table.source_github),\n',
        '    github: t(($) => $.table.source_github),\n    onetool: t(($) => $.table.source_onetool),\n')
    add("packages/views/skills/components/skills-page.tsx", '  } else if (origin.type === "github") {\n',
        '  } else if (origin.type === "onetool") {\n    icon = <Download className="h-3 w-3 shrink-0" />;\n    label = t(($) => $.table.source_onetool);\n  } else if (origin.type === "github") {\n')
    add("packages/views/skills/components/skill-detail-page.tsx", '  if (origin.type === "github") return t(($) => $.detail.subline.origin_github);\n',
        '  if (origin.type === "github") return t(($) => $.detail.subline.origin_github);\n  if (origin.type === "onetool") return t(($) => $.detail.subline.origin_onetool);\n')
    add("packages/views/skills/components/refresh-skill-dialog.tsx", '  if (origin?.type === "clawhub")',
        '  if (origin?.type === "onetool") return t(($) => $.detail.refresh.source_onetool);\n  if (origin?.type === "clawhub")')

    hints = {
        "en": "OneTool: paste a ZIP download link or the copied install text. Detail-page URLs are not supported without authorization.",
        "zh-Hans": "OneTool：粘贴 ZIP 下载链接或完整的安装文本，免鉴权模式不支持详情页地址。",
        "ja": "OneTool：ZIP のダウンロード URL またはコピーしたインストール文を貼り付けてください。詳細ページの URL には認証が必要です。",
        "ko": "OneTool: ZIP 다운로드 링크나 복사한 설치 문구를 붙여넣으세요. 상세 페이지 URL에는 인증이 필요합니다.",
        "fr": "OneTool : collez le lien ZIP ou le texte d’installation copié. Une URL de fiche nécessite une authentification.",
    }
    loading = {"en": "Importing from OneTool...", "zh-Hans": "正在从 OneTool 导入...", "ja": "OneTool からインポート中...", "ko": "OneTool에서 가져오는 중...", "fr": "Importation depuis OneTool..."}
    for locale, hint in hints.items():
        labels = f"packages/views/locales/{locale}/skills.json"
        add(labels, '      "supported_sources": ', '      "onetool_hint": ' + json.dumps(hint, ensure_ascii=False) + ',\n      "importing_onetool": ' + json.dumps(loading[locale], ensure_ascii=False) + ',\n      "supported_sources": ')
        add(labels, '\n    "source_github": ', '\n    "source_onetool": "OneTool",\n    "source_github": ')
        add(labels, '\n      "source_github": ', '\n      "source_onetool": "OneTool",\n      "source_github": ')
        add(labels, '\n      "origin_github": ', '\n      "origin_onetool": "OneTool",\n      "origin_github": ')
