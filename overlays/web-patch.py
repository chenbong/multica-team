#!/usr/bin/env python3
"""Apply checked v0.6.1 frontend edits only to an explicit staging copy.

All anchors are validated before writing. Original files are retained for a
verified restore, and a repeated application never replaces those originals.
"""
import json
import os
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = Path(os.environ.get("MULTICA_WEB_TARGET", str(HERE.parent / "multica"))).resolve()
BACKUP = Path(os.environ.get("MULTICA_WEB_BACKUP", str(REPO / ".overlay-backup"))).resolve()

DIALOG = "packages/views/runtimes/components/connect-remote-dialog.tsx"
SKILLS_PAGE = "packages/views/skills/components/skills-page.tsx"
SKILL_TYPES = "packages/core/types/agent.ts"
SKILLS_EN = "packages/views/locales/en/skills.json"
SKILLS_ZH = "packages/views/locales/zh-Hans/skills.json"
SKILL_DETAIL = "packages/views/skills/components/skill-detail-page.tsx"
AUTH_ZH = "packages/views/locales/zh-Hans/auth.json"
AUTH_EN = "packages/views/locales/en/auth.json"
LOGIN_PAGE = "packages/views/auth/login-page.tsx"
LOGIN_ROUTE = "apps/web/app/(auth)/login/page.tsx"
AGENTS_EN = "packages/views/locales/en/agents.json"
AGENTS_ZH = "packages/views/locales/zh-Hans/agents.json"
AGENTS_PAGE = "packages/views/agents/components/agents-page.tsx"
SKILL_PICKER = "packages/views/agents/components/skill-picker-list.tsx"
SIDEBAR = "packages/views/layout/app-sidebar.tsx"
PROXY = "apps/web/proxy.ts"

SIDEBAR_DRAFT_DOT = '''function DraftDot() {
  const hasDraft = useIssueDraftStore((s) => s.hasDraft());
  if (!hasDraft) return null;
  return <span className="absolute top-0 right-0 size-1.5 rounded-full bg-brand" />;
}'''

SIDEBAR_MOBILE_CONTROL = '''async function copyToClipboard(text: string): Promise<boolean> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // HTTP pages may not expose the Clipboard API; use the legacy fallback.
    }
  }

  const input = document.createElement("textarea");
  input.value = text;
  input.setAttribute("readonly", "");
  input.style.position = "fixed";
  input.style.opacity = "0";
  document.body.appendChild(input);
  input.focus();
  input.select();
  try {
    return document.execCommand("copy");
  } finally {
    input.remove();
  }
}

function MobileControlLink() {
  const [url, setUrl] = useState<string>();
  const [copied, setCopied] = useState(false);
  const copiedTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    let cancelled = false;
    void fetch("/api/public-ip", { cache: "no-store" })
      .then(async (response) => {
        if (!response.ok) throw new Error(`IP lookup failed: ${response.status}`);
        const data = (await response.json()) as { ip?: string };
        if (!data.ip) throw new Error("IP lookup returned no address");
        return data.ip;
      })
      .then((ip) => {
        if (!cancelled) setUrl(`http://${ip}:8004`);
      })
      .catch(() => {
        // Keep the sidebar unobtrusive if DNS is temporarily unavailable.
      });

    return () => {
      cancelled = true;
      if (copiedTimer.current) clearTimeout(copiedTimer.current);
    };
  }, []);

  const handleCopy = useCallback(async () => {
    if (!url || !(await copyToClipboard(url))) return;
    setCopied(true);
    if (copiedTimer.current) clearTimeout(copiedTimer.current);
    copiedTimer.current = setTimeout(() => setCopied(false), 1600);
  }, [url]);

  if (!url) return null;
  return (
    <SidebarGroup className="mt-auto pt-4 pb-0">
      <SidebarGroupContent>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              size="sm"
              type="button"
              className="text-muted-foreground hover:text-foreground"
              onClick={handleCopy}
              title={url}
              aria-live="polite"
              aria-label={copied ? "已复制" : `手机控制：${url}`}
            >
              <span className="truncate">
                {copied ? "已复制" : `手机控制：${url}`}
              </span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarGroupContent>
    </SidebarGroup>
  );
}'''

# Login-page branding is deployment configuration, never a literal in this
# repository: a public clone therefore builds a stock Multica login page, and a
# deployment points the page at its own verification robot through the
# environment (see deploy/host.env.example).
LOGIN_TITLE = os.environ.get("MULTICA_LOGIN_TITLE", "").strip()
LOGIN_DESCRIPTION = os.environ.get("MULTICA_LOGIN_DESCRIPTION", "").strip()
EMAIL_PLACEHOLDER = os.environ.get("MULTICA_EMAIL_PLACEHOLDER", "").strip()
BOT_NAME = os.environ.get("MULTICA_VERIFICATION_BOT_NAME", "").strip()
BOT_URL = os.environ.get("MULTICA_VERIFICATION_BOT_URL", "").strip()
BOT_CONFIGURED = bool(BOT_NAME and BOT_URL)
# Where the "add a computer" dialog tells people to get the CLI. Points at this
# repository's own installer by default in a deployment, and at upstream's when
# unset, so a public clone never advertises somebody else's build.
CLI_INSTALL_URL = (
    os.environ.get("MULTICA_CLI_INSTALL_URL", "").strip()
    or "https://raw.githubusercontent.com/multica-ai/multica/main/scripts/install.sh"
)

# The agents page gets one extra action: a link to this deployment's InfoFlow
# (如流) binding page, which lives on another port of the same host. Both the
# URL and its label are deployment configuration — the URL names a host that
# must never appear in the public repository — and an empty URL leaves the page
# exactly as upstream ships it.
INFOFLOW_BRIDGE_URL = os.environ.get("MULTICA_INFOFLOW_BRIDGE_URL", "").strip().rstrip("/")
INFOFLOW_BRIDGE_LABEL = (
    os.environ.get("MULTICA_INFOFLOW_BRIDGE_LABEL", "").strip() or "绑定如流机器人"
)


def agents_page_changes():
    """Add the InfoFlow binding entry point next to "new agent"."""
    if not INFOFLOW_BRIDGE_URL:
        return []
    return [
        (
            'import { useCallback, useMemo, useRef, useState } from "react";\n',
            'import { useCallback, useMemo, useRef, useState } from "react";\n'
            "\n"
            "// Local overlay: entry point to this deployment's InfoFlow binding page.\n"
            "// Injected at build time from MULTICA_INFOFLOW_BRIDGE_URL; the button is\n"
            "// not rendered at all when that variable is empty.\n"
            f"const INFOFLOW_BRIDGE_URL = {json.dumps(INFOFLOW_BRIDGE_URL)};\n"
            f"const INFOFLOW_BRIDGE_LABEL = {json.dumps(INFOFLOW_BRIDGE_LABEL)};\n",
        ),
        (
            "import {\n  AlertCircle,\n  Bot,\n  Lock,\n  Plus,\n} from \"lucide-react\";\n",
            "import {\n  AlertCircle,\n  Bot,\n  Lock,\n  MessageSquare,\n  Plus,\n} from \"lucide-react\";\n",
        ),
        (
            "      actions={\n"
            "        <CollectionPageHeaderAction\n"
            "          icon={Plus}\n"
            "          label={t(($) => $.page.new_agent)}\n"
            "          onClick={onCreate}\n"
            "        />\n"
            "      }\n",
            "      actions={\n"
            "        <>\n"
            "          {INFOFLOW_BRIDGE_URL ? (\n"
            "            <CollectionPageHeaderAction\n"
            "              icon={MessageSquare}\n"
            "              label={INFOFLOW_BRIDGE_LABEL}\n"
            "              nativeButton={false}\n"
            "              render={\n"
            '                <a href={INFOFLOW_BRIDGE_URL} target="_blank" rel="noreferrer" />\n'
            "              }\n"
            "            />\n"
            "          ) : null}\n"
            "          <CollectionPageHeaderAction\n"
            "            icon={Plus}\n"
            "            label={t(($) => $.page.new_agent)}\n"
            "            onClick={onCreate}\n"
            "          />\n"
            "        </>\n"
            "      }\n",
        ),
    ]


def daemon_runtime_defaults() -> str:
    """Runtime ids the handed-out daemon command pins, comma separated.

    MULTICA_DAEMON_RUNTIME_DEFAULTS is deployment configuration (an internal
    provider list, e.g. a company build of claude/codex), so it never lives in
    this repository: unset or empty keeps the upstream command, which registers
    every agent CLI the machine happens to have.
    """
    raw = os.environ.get("MULTICA_DAEMON_RUNTIME_DEFAULTS", "").replace(",", " ").split()
    ids: list[str] = []
    for item in raw:
        if item not in ids:
            ids.append(item)
    return ",".join(ids)


RUNTIME_DEFAULTS = daemon_runtime_defaults()
def daemon_runtime_shim_defaults() -> str:
    """Runtime command names the handed-out daemon command pins a shim for.

    MULTICA_DAEMON_RUNTIME_SHIM_DEFAULTS is deployment configuration for the
    same reason MULTICA_DAEMON_RUNTIME_DEFAULTS is: it names the machine's own
    CLIs. Empty keeps the upstream command, so nobody gets a wrapper they did
    not ask for.
    """
    raw = os.environ.get("MULTICA_DAEMON_RUNTIME_SHIM_DEFAULTS", "").replace(",", " ").split()
    names: list[str] = []
    for item in raw:
        if item not in names:
            names.append(item)
    return ",".join(names)


SHIM_DEFAULTS = daemon_runtime_shim_defaults()
def auth_zh_changes():
    entries = []
    if BOT_CONFIGURED:
        entries.append(
            (
                '  "signin": {',
                '  "signin": {\n'
                f'    "verification_bot_hint": "如果从未使用过“{BOT_NAME}”，请先打开",\n'
                '    "verification_bot_link": "对话窗口",\n'
                '    "verification_bot_tail": "，后续即可正常接收验证码。",',
            )
        )
    if LOGIN_TITLE:
        entries.append(('    "title": "登录 Multica",', f'    "title": "{LOGIN_TITLE}",'))
    if LOGIN_DESCRIPTION:
        entries.append(
            ('    "description": "输入邮箱以获取登录验证码",', f'    "description": "{LOGIN_DESCRIPTION}",')
        )
    if EMAIL_PLACEHOLDER:
        entries.append(
            ('    "email_placeholder": "you@example.com",', f'    "email_placeholder": "{EMAIL_PLACEHOLDER}",')
        )
    if BOT_CONFIGURED:
        entries.append(
            (
                '  "verify": {',
                '  "verify": {\n'
                '    "title_prefix": "查看 “",\n'
                f'    "title_link": "{BOT_NAME}",\n'
                '    "title_suffix": "” 的验证码消息",',
            )
        )
    return entries


def auth_en_changes():
    if not BOT_CONFIGURED:
        return []
    return [
        (
            '  "signin": {',
            '  "signin": {\n'
            f'    "verification_bot_hint": "If you have never used the “{BOT_NAME}” bot, open",\n'
            '    "verification_bot_link": "the conversation window",\n'
            '    "verification_bot_tail": " first; you can then receive codes normally.",',
        ),
        (
            '  "verify": {',
            '  "verify": {\n'
            '    "title_prefix": "Check the “",\n'
            f'    "title_link": "{BOT_NAME}",\n'
            '    "title_suffix": "” verification messages",',
        ),
    ]


def login_page_changes():
    if not BOT_CONFIGURED:
        return []
    return [
        (
            'import { useT } from "../i18n";',
            'import { useT } from "../i18n";\n\n'
            f'const VERIFICATION_BOT_URL = "{BOT_URL}";',
        ),
        (
            '          <CardDescription>\n            {t(($) => $.signin.description)}\n          </CardDescription>',
            '          <CardDescription>\n            {t(($) => $.signin.description)}\n          </CardDescription>\n          <div className="mt-3 space-y-1 text-left text-caption text-muted-foreground">\n            <p>\n              {t(($) => $.signin.verification_bot_hint)}{" "}\n              <a href={VERIFICATION_BOT_URL} className="text-primary underline underline-offset-2">\n                {t(($) => $.signin.verification_bot_link)}\n              </a>\n              {t(($) => $.signin.verification_bot_tail)}\n            </p>\n          </div>',
        ),
        (
            '            <CardTitle className="text-display-sm">\n              {t(($) => $.verify.title)}\n            </CardTitle>',
            '            <CardTitle className="text-display-sm">\n              {t(($) => $.verify.title_prefix)}\n              <a href={VERIFICATION_BOT_URL} className="text-primary underline underline-offset-2">\n                {t(($) => $.verify.title_link)}\n              </a>\n              {t(($) => $.verify.title_suffix)}\n            </CardTitle>',
        ),
    ]


CHANGES = {
    AUTH_ZH: auth_zh_changes(),
    AUTH_EN: auth_en_changes(),
    LOGIN_PAGE: login_page_changes(),
    LOGIN_ROUTE: [
        # The web shell passes a "prefer the desktop app? download" slot into the
        # sign-in card. This deployment has no desktop build to point at, so the
        # slot (and the now-unused import it needed) is dropped.
        (
            "      extra={\n"
            '        <span className="text-caption text-muted-foreground">\n'
            '          {t(($) => $.web.prefer_desktop)}{" "}\n'
            "          <Link\n"
            '            href="/download"\n'
            '            className="font-medium text-foreground underline decoration-foreground/30 underline-offset-4 hover:decoration-foreground/70"\n'
            "          >\n"
            "            {t(($) => $.web.download)}\n"
            "          </Link>\n"
            "        </span>\n"
            "      }\n",
            "",
        ),
        ('import Link from "next/link";\n', ""),
    ],
    AGENTS_PAGE: agents_page_changes(),
    AGENTS_EN: [
        (
            '    "skills_section": {\n      "label": "Skills",\n',
            '    "skills_section": {\n      "label": "Skills",\n'
            '      "scope_team": "Team",\n'
            '      "scope_project_prefix": "Workspace: ",\n'
            '      "scope_project_default": "current workspace",\n'
            '      "scope_personal": "Personal",\n',
        ),
    ],
    AGENTS_ZH: [
        (
            '    "skills_section": {\n      "label": "Skills",\n',
            '    "skills_section": {\n      "label": "Skills",\n'
            '      "scope_team": "团队",\n'
            '      "scope_project_prefix": "工作区：",\n'
            '      "scope_project_default": "当前工作区",\n'
            '      "scope_personal": "个人",\n',
        ),
    ],
    SKILL_PICKER: [
        (
            '          filtered.map((skill) => {\n'
            '            const isSelected = selectedIds.has(skill.id);\n',
            '          filtered.map((skill) => {\n'
            '            const isSelected = selectedIds.has(skill.id);\n'
            '            const scope = (skill.scope ?? "workspace") as string;\n'
            '            const scopeLabel = scope === "team"\n'
            '              ? t(($) => $.create_dialog.skills_section.scope_team)\n'
            '              : scope === "personal"\n'
            '                ? t(($) => $.create_dialog.skills_section.scope_personal)\n'
            '                : t(($) => $.create_dialog.skills_section.scope_project_prefix) + (skill.workspace_name || t(($) => $.create_dialog.skills_section.scope_project_default));\n',
        ),
        (
            '                  <div className="truncate text-body font-medium">{skill.name}</div>\n',
            '                  <div className="flex min-w-0 items-center gap-2">\n'
            '                    <div className="min-w-0 truncate text-body font-medium">{skill.name}</div>\n'
            '                    <span className="shrink-0 rounded border border-border/60 px-1.5 py-0.5 text-micro font-medium text-muted-foreground">\n'
            '                      {scopeLabel}\n'
            '                    </span>\n'
            '                  </div>\n',
        ),
    ],
    SIDEBAR: [
        (
            SIDEBAR_DRAFT_DOT,
            SIDEBAR_DRAFT_DOT + "\n\n" + SIDEBAR_MOBILE_CONTROL,
        ),
        (
            "          </SidebarGroup>\n        </SidebarContent>",
            "          </SidebarGroup>\n\n"
            "          <MobileControlLink />\n"
            "        </SidebarContent>",
        ),
    ],
    PROXY: [
        (
            "  const runtimeDestination = runtimeRewriteDestination(pathname, process.env);\n",
            "  const runtimeDestination = pathname === \"/api/public-ip\"\n"
            "    ? null\n"
            "    : runtimeRewriteDestination(pathname, process.env);\n",
        ),
        (
            "  // --- Root path: redirect logged-in users to their last workspace ---\n"
            "  // The official cloud host also serves the public marketing site. Visiting\n"
            "  // https://multica.ai/ must remain a public-site navigation even when a local\n"
            "  // desktop/runtime session has fresh auth cookies; explicit app routes such\n"
            "  // as /acme/issues and legacy /issues still route to the workspace app.\n"
            "  if (\n"
            "    pathname === \"/\" &&\n"
            "    hasSession &&\n"
            "    lastSlug &&\n"
            "    !isOfficialMarketingHost(req.nextUrl.hostname)\n"
            "  ) {\n",
            "  // --- App deployment root: render login without an HTTP redirect ---\n"
            "  // Keep official marketing hosts on their public landing page, while this\n"
            "  // self-hosted deployment skips the welcome page for every visitor.\n"
            "  if (pathname === \"/\" && !isOfficialMarketingHost(req.nextUrl.hostname)) {\n",
        ),
        (
            "  if (pathname === \"/\" && !isOfficialMarketingHost(req.nextUrl.hostname)) {\n"
            "    const url = req.nextUrl.clone();\n"
            "    url.pathname = `/${lastSlug}/issues`;\n"
            "    return NextResponse.redirect(url);\n"
            "  }\n",
            "  if (pathname === \"/\" && !isOfficialMarketingHost(req.nextUrl.hostname)) {\n"
            "    const url = req.nextUrl.clone();\n"
            "    url.pathname = \"/login\";\n"
            "    const headers = new Headers(req.headers);\n"
            "    headers.set(MULTICA_LOCALE_HEADER, resolveLocale(req));\n"
            "    return NextResponse.rewrite(url, { request: { headers } });\n"
            "  }\n",
        ),
    ],
    SKILLS_EN: [
        ('    "new_skill": "New skill",', '    "new_skill": "New skill",\n    "team_skills": "Team skills",\n    "project_skills": "Workspace skills",\n    "personal_skills": "Personal skills",'),
        ('    "used_by": "Used by",', '    "used_by": "Used by",\n    "usage_count": "Usage count",'),
        ('    "source": "Source",', '    "source": "Source",\n    "level": "Level",\n    "level_team": "Team",\n    "level_project": "Workspace",\n    "level_project_prefix": "Workspace: ",\n    "level_personal": "Personal",\n    "level_current_project": "Current workspace",'),
        ('  "detail": {', '  "detail": {\n    "category_label": "Skill category",\n    "category_team": "Team",\n    "category_project": "Workspace",\n    "category_personal": "Personal",'),
        ('  "actions": {\n    "row_menu":', '  "actions": {\n    "change_level": "Change level",\n    "change_level_title": "Change skill level",\n    "change_level_desc": "Only the skill creator can change its level.",\n    "change_level_save": "Save level",\n    "change_level_saved": "Skill level updated",\n    "change_level_failed": "Failed to change skill level",\n    "row_menu":'),
        ('    "section_usage": "Usage",', '    "section_usage": "Usage",\n    "section_projects": "Workspaces",\n    "other_projects": "Other workspaces",'),
        (
            '  "create": {',
            '  "create": {\n    "scope": {"title": "Scope", "desc": "Choose whether this skill belongs to the team, a workspace, or only to you.", "team": {"title": "Team skill", "desc": "Visible to every user across every workspace."}, "workspace": {"title": "Workspace skill", "desc": "Visible across users and workspaces; keeps this workspace as its source."}, "personal": {"title": "Personal skill", "desc": "Reusable by you across workspaces."}},',
        ),
    ],
    SKILLS_ZH: [
        ('    "new_skill": "新建 skill",', '    "new_skill": "新建 skill",\n    "team_skills": "团队 Skill",\n    "project_skills": "工作区 Skill",\n    "personal_skills": "个人 Skill",'),
        ('    "used_by": "被谁使用",', '    "used_by": "被谁使用",\n    "usage_count": "使用次数",'),
        ('    "source": "来源",', '    "source": "来源",\n    "level": "Skill 级别",\n    "level_team": "团队",\n    "level_project": "工作区",\n    "level_project_prefix": "工作区：",\n    "level_personal": "个人",\n    "level_current_project": "当前工作区",'),
        ('  "detail": {', '  "detail": {\n    "category_label": "Skill 类别",\n    "category_team": "团队",\n    "category_project": "工作区",\n    "category_personal": "个人",'),
        ('  "actions": {\n    "row_menu":', '  "actions": {\n    "change_level": "调整级别",\n    "change_level_title": "调整 Skill 级别",\n    "change_level_desc": "只有 Skill 添加者可以调整级别。",\n    "change_level_save": "保存级别",\n    "change_level_saved": "Skill 级别已更新",\n    "change_level_failed": "调整 Skill 级别失败",\n    "row_menu":'),
        ('    "section_usage": "使用状态",', '    "section_usage": "使用状态",\n    "section_projects": "工作区",\n    "other_projects": "其他工作区",'),
        (
            '  "create": {',
            '  "create": {\n    "scope": {"title": "Skill 范围", "desc": "选择这个 Skill 属于团队、工作区还是仅属于你。", "team": {"title": "团队 Skill", "desc": "所有用户、所有工作区都可见。"}, "workspace": {"title": "工作区 Skill", "desc": "跨用户、跨工作区可见，并保留来源工作区。"}, "personal": {"title": "个人 Skill", "desc": "你可以跨工作区复用。"}},',
        ),
    ],

    "packages/core/api/client.ts": [
        ('  async issueCliToken(): Promise<{ token: string }> {\n    return this.fetch("/api/cli-token", { method: "POST" });\n  }', '  async issueCliToken(): Promise<{ token: string }> {\n    return this.fetch("/api/cli-token", { method: "POST" });\n  }\n\n  async getOrCreateDaemonBootstrapToken(): Promise<{ token: string }> {\n    return this.fetch("/api/daemon-bootstrap-token", { method: "POST" });\n  }')],

    "packages/views/locales/en/runtimes.json": [
        (
            '    "step2_hint": "Opens a browser to sign in, then keeps the daemon running in the background.",',
            '    "step2_hint": "Opens a browser to sign in, then keeps the daemon running in the background.",\n'
            '    "token_step_hint": "Use a token to sign in; this works in terminal-only environments.",\n'
            '    "browser_login": "Use browser sign-in",\n'
            '    "browser_login_intro": "If this computer can open a browser, you can use the browser sign-in below.",',
        ),
    ],
    "packages/views/locales/zh-Hans/runtimes.json": [
        (
            '    "step2_hint": "会打开浏览器登录，然后在后台保持守护进程运行。",',
            '    "step2_hint": "会打开浏览器登录，然后在后台保持守护进程运行。",\n'
            '    "token_step_hint": "使用 token 登录，适用于无法打开浏览器的终端环境。",\n'
            '    "browser_login": "使用浏览器登录",\n'
            '    "browser_login_intro": "如果这台电脑可以打开浏览器，也可以使用下面的方式。",',
        ),
    ],
    "packages/views/locales/ja/runtimes.json": [
        (
            '    "step2_hint": "サインインのためにブラウザを開き、その後デーモンをバックグラウンドで実行し続けます。",',
            '    "step2_hint": "サインインのためにブラウザを開き、その後デーモンをバックグラウンドで実行し続けます。",\n'
            '    "token_step_hint": "トークンでサインインします。ブラウザを開けないターミナル環境でも利用できます。",\n'
            '    "browser_login": "ブラウザでサインイン",\n'
            '    "browser_login_intro": "このコンピュータでブラウザを開ける場合は、以下のブラウザサインインも利用できます。",',
        ),
    ],
    "packages/views/locales/ko/runtimes.json": [
        (
            '    "step2_hint": "로그인을 위해 브라우저를 열고, 이후 데몬을 백그라운드에서 계속 실행합니다.",',
            '    "step2_hint": "로그인을 위해 브라우저를 열고, 이후 데몬을 백그라운드에서 계속 실행합니다.",\n'
            '    "token_step_hint": "토큰으로 로그인하며, 브라우저 없이 터미널만 있는 환경에서도 사용할 수 있습니다.",\n'
            '    "browser_login": "브라우저로 로그인",\n'
            '    "browser_login_intro": "이 컴퓨터에서 브라우저를 열 수 있다면 아래 브라우저 로그인을 사용할 수 있습니다.",',
        ),
    ],

    "packages/views/onboarding/onboarding-flow.tsx": [
        (
'  const handleWorkspaceCreated = useCallback(\n    (ws: Workspace) => {\n      setWorkspace(ws);\n      // Deliberately NOT setCurrentWorkspace: that singleton is also written by\n      // the desktop tab system, which reclaims it whenever the new workspace\n      // has no tab group yet. Racing it sent the rest of this flow — Mika, the\n      // session, the kickoff — into the previously-active workspace. Every call\n      // from here on names its target workspace instead, and the switch happens\n      // once, on the navigation in onComplete.\n      advanceFrom("workspace");\n    },\n    [advanceFrom],\n  );',
'  const handleWorkspaceCreated = useCallback(\n    async (ws: Workspace) => {\n      if (isNewWorkspace) {\n        // Creating an additional workspace should land there immediately.\n        // Runtime and Mika setup remain available from inside the workspace;\n        // they are not a prerequisite for entering it.\n        onComplete(ws);\n        return;\n      }\n      if (isWeb) {\n        // Web onboarding can enter the workspace before a runtime is ready;\n        // complete the same skip path as the visible runtime skip action.\n        try {\n          await completeOnboarding("runtime_skipped", ws.id);\n        } catch (err) {\n          toast.error(\n            err instanceof Error ? err.message : t(($) => $.errors.skip_failed),\n          );\n          // Leave the user with the manual runtime page as a recoverable\n          // fallback if the completion request is temporarily unavailable.\n          setWorkspace(ws);\n          advanceFrom("workspace");\n          return;\n        }\n        useWelcomeStore.getState().set({\n          workspaceId: ws.id,\n          choice: "skip",\n        });\n        onComplete(ws);\n        return;\n      }\n      setWorkspace(ws);\n      // Deliberately NOT setCurrentWorkspace: that singleton is also written by\n      // the desktop tab system, which reclaims it whenever the new workspace\n      // has no tab group yet. Racing it sent the rest of this flow — Mika, the\n      // session, the kickoff — into the previously-active workspace. Every call\n      // from here on names its target workspace instead, and the switch happens\n      // once, on the navigation in onComplete.\n      advanceFrom("workspace");\n    },\n    [advanceFrom, isNewWorkspace, isWeb, onComplete, t],\n  );',
        ),
        (
'  const isNewWorkspace = mode === "new_workspace";\n  const [step, setStep] = useState<OnboardingStep>(\n    isNewWorkspace ? "workspace" : "welcome",\n  );',
'  const isNewWorkspace = mode === "new_workspace";\n  // On the web route the user has already chosen web; skip the desktop/web\n  // choice hero and start with the first useful onboarding step.\n  const [step, setStep] = useState<OnboardingStep>(\n    isNewWorkspace\n      ? "workspace"\n      : runtimeInstructions\n        ? "about_you"\n        : "welcome",\n  );',
        ),
        (
'  // deliberately not saved, so every entry starts at Welcome.\n',
'  // deliberately not saved, so each entry starts at the first step for its mode.\n',
        ),
        (
'  const stepBack =\n    step === "about_you"\n      ? () => handleBack("about_you")\n      : step === "workspace"\n        ? () => handleBack("workspace")\n        : runtimeStepBack;',
'  const stepBack =\n    step === "about_you"\n      ? isWeb\n        ? undefined\n        : () => handleBack("about_you")\n      : step === "workspace"\n        ? () => handleBack("workspace")\n        : runtimeStepBack;',
        ),
    ],

}

CHANGES[SKILLS_EN].append(('\n  "actions": {', '\n  "actions": {\n    "download": "Download",\n    "download_failed": "Could not download skill",'))
CHANGES[SKILLS_ZH].append(('\n  "actions": {', '\n  "actions": {\n    "download": "下载",\n    "download_failed": "无法下载 skill",'))

CHANGES["packages/core/api/client.ts"].append((
    '  async deleteRuntime(runtimeId: string): Promise<void> {',
    '''  async deleteOfflineMachine(runtimeId: string, runtimeIds: string[], agentIds: string[]): Promise<void> {
    await this.fetch(`/api/runtimes/${runtimeId}/delete-offline-machine`, {
      method: "POST",
      body: JSON.stringify({ expected_runtime_ids: runtimeIds, expected_active_agent_ids: agentIds }),
    });
  }

  async deleteRuntime(runtimeId: string): Promise<void> {'''))
CHANGES.setdefault("packages/core/runtimes/mutations.ts", []).append((
    'export function useDeleteRuntime(wsId: string) {',
    '''export function useDeleteOfflineMachine(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({runtimeId, runtimeIds, agentIds}: {runtimeId: string; runtimeIds: string[]; agentIds: string[]}) => api.deleteOfflineMachine(runtimeId, runtimeIds, agentIds),
    onSettled: () => {
      qc.invalidateQueries({queryKey: runtimeKeys.all(wsId)});
      qc.invalidateQueries({queryKey: workspaceKeys.agents(wsId)});
      qc.invalidateQueries({queryKey: agentTaskSnapshotKeys.all(wsId)});
    },
  });
}

export function useDeleteRuntime(wsId: string) {'''))
for locale, labels in {
    "zh-Hans": {"button":"删除电脑", "title":"删除电脑：{{name}}", "description":"将从数据库删除当前工作区中这台电脑的 {{count}} 个运行时实例。保留共享运行时配置及其他电脑。", "effects":"绑定的用户智能体将解绑，保留配置、聊天和任务历史；未完成运行会取消，受影响的自动化会暂停。内部系统智能体按现有清理规则移除。", "confirm_unbind":"我确认解绑以上智能体，并取消相关未完成运行。", "reconnect":"如果该电脑的 daemon 再次连接，电脑可能重新出现在列表中。", "cancel":"取消", "success":"电脑及其运行时已删除", "failed":"删除失败，请关闭弹窗后重试", "deleting":"删除中..."},
    "en": {"button":"Delete computer", "title":"Delete computer: {{name}}", "description":"Delete this computer's {{count}} runtime instances from the current workspace database. Shared profiles and other computers are retained.", "effects":"User agents are unbound; their settings, chats and task history are retained. Unfinished runs are cancelled and affected automations paused. Internal system agents follow existing cleanup rules.", "confirm_unbind":"I confirm unbinding these agents and cancelling their unfinished runs.", "reconnect":"The computer may reappear if its daemon reconnects.", "cancel":"Cancel", "success":"Computer and runtimes deleted", "failed":"Deletion failed. Close the dialog and try again.", "deleting":"Deleting..."}
}.items():
    CHANGES.setdefault(f"packages/views/locales/{locale}/runtimes.json", []).append((
        '{\n  "page": {', '{\n  "machine_delete": ' + json.dumps(labels, ensure_ascii=False) + ',\n  "page": {'))

CHANGES["packages/core/api/client.ts"].extend([
    ('import { configStore } from "../config";', 'import { configStore } from "../config";\nimport { DuccStatusSchema, emptyDuccStatus, type DuccStatus } from "./ducc-schema";'),
    ('  async deleteRuntime(runtimeId: string): Promise<void> {', '''  async getDuccCredential(): Promise<DuccStatus> {
    return parseWithFallback<DuccStatus>(await this.fetch("/api/me/ducc"), DuccStatusSchema, emptyDuccStatus, {endpoint:"GET /api/me/ducc"});
  }
  async updateDuccCredential(input: {action:"enable"|"import"|"remove"; enabled?:boolean; daemon_id?:string}): Promise<void> {
    await this.fetch("/api/me/ducc", {method:"POST", body:JSON.stringify(input)});
  }
  async deleteRuntime(runtimeId: string): Promise<void> {''')])
CHANGES.setdefault("packages/core/runtimes/index.ts",[]).append(('export * from "./access";', 'export * from "./access";\nexport * from "./ducc";'))
for locale, labels in {
 "zh-Hans":{"title":"ducc 登录凭据","status":"托管状态","scope":"仅用于本人账号，跨工作区共用；凭据内容不会在页面展示。","loading":"加载中...","unavailable":"凭据服务暂不可用","pending":"等待所选电脑导入","imported":"已导入（来源端 CLI 认证检查通过）","not_imported":"尚未导入","source":"来源电脑","auto":"自动准备 ducc","auto_desc":"启用后，从本人电脑自动导入登录凭据；添加新电脑时自动安装 ducc，并在凭据缺失时下发。已有文件不会被覆盖。","computers":"我的电脑","computers_desc":"按电脑去重，包含所有工作区。离线电脑等待重新连接；旧版 daemon 需要升级。","upgrade":"需要升级 daemon","offline":"离线","state_unknown":"待检查","state_missing":"未找到本人凭据","state_present":"已找到凭据","state_ready":"认证检查通过","state_invalid":"凭据无法验证，请在电脑上重新登录","state_installing":"正在安装 ducc","state_failed":"安装失败，请检查网络与安装权限","import_button":"从此电脑导入","no_computers":"尚无可检测的电脑","remove":"移除托管凭据","remove_desc":"仅移除服务端副本并关闭自动同步，不会删除或注销已经下发到电脑上的凭据。","import_confirm":"从 {{name}} 导入本人账号的凭据。验证通过后将更新服务端版本，不覆盖其他电脑已有的凭据。","cancel":"取消","confirm":"确认","saved":"设置已更新","failed":"操作失败，请稍后重试"},
 "en":{"title":"ducc credentials","status":"Stored credential","scope":"Personal account only, shared across your workspaces. Credential contents are never displayed.","loading":"Loading...","unavailable":"Credential service unavailable","pending":"Waiting for the selected computer","imported":"Imported (source CLI authentication check passed)","not_imported":"Not imported","source":"Source computer","auto":"Prepare ducc automatically","auto_desc":"Import your credential from your computers. New computers install ducc and receive the credential if missing. Existing files are never overwritten.","computers":"My computers","computers_desc":"Deduplicated across workspaces. Offline computers wait until connected; older daemons need an upgrade.","upgrade":"Daemon upgrade required","offline":"Offline","state_unknown":"Waiting for check","state_missing":"Personal credential missing","state_present":"Credential found","state_ready":"Authentication check passed","state_invalid":"Credential could not be verified; log in on the computer","state_installing":"Installing ducc","state_failed":"Installation failed; check network and permissions","import_button":"Import from computer","no_computers":"No computers available","remove":"Remove stored credential","remove_desc":"Remove only the server copy and disable automatic sync. Credentials already delivered to computers are not removed or revoked.","import_confirm":"Import your credential from {{name}}. Successful validation updates the server version, without overwriting existing credentials on other computers.","cancel":"Cancel","confirm":"Confirm","saved":"Settings updated","failed":"Operation failed; please retry"}
}.items():
    labels.update({"choose_computer": "选择来源电脑", "computers": "来源电脑", "import_button": "导入凭据到平台"} if locale=="zh-Hans" else {"choose_computer": "Choose a source computer", "computers": "Source computer", "import_button": "Import credential to platform"})
    path=f"packages/views/locales/{locale}/settings.json"
    CHANGES.setdefault(path,[]).append(('  "page": {', '  "ducc": '+json.dumps(labels,ensure_ascii=False)+',\n  "page": {'))


for locale,label in {"en":"Registration failed","zh-Hans":"注册失败"}.items():
    CHANGES[f"packages/views/locales/{locale}/runtimes.json"].append(('    "tagline":', '    "registration_failed": '+json.dumps(label,ensure_ascii=False)+',\n    "tagline":'))

for locale, label, old, new in [
    ("zh-Hans", "安装并启动 Multica", "在要添加的电脑上运行这两条命令。守护进程一上线，这里就会自动识别。", "在要添加的电脑上复制运行以下整组命令。守护进程一上线，这里就会自动识别。"),
    ("en", "Install and start Multica", "Run these two commands on the computer you want to add. We'll detect it the moment the daemon comes online.", "Copy and run this command block on the computer you want to add. We'll detect it the moment the daemon comes online."),
]:
    CHANGES[f"packages/views/locales/{locale}/runtimes.json"].extend([
        (json.dumps(old, ensure_ascii=False), json.dumps(new, ensure_ascii=False)),
        ('    "step1_label":', '    "combined_label": '+json.dumps(label, ensure_ascii=False)+',\n    "step1_label":'),
    ])


# The refactored v0.6.1 dialog retains its OS switch; deployment options
# affect only the command generator, never the surrounding upstream behavior.
CHANGES[DIALOG] = [
    ('const CONNECTION_DEFAULTS = {};', 'const CONNECTION_DEFAULTS = ' + json.dumps({
        "installUrl": CLI_INSTALL_URL,
        "windowsInstallUrl": os.environ.get("MULTICA_CLI_INSTALL_WINDOWS_URL", "").strip(),
        "runtimeDefaults": RUNTIME_DEFAULTS,
        "shimDefaults": SHIM_DEFAULTS,
    }) + ';'),
]

def validate_target():
    if not os.environ.get("MULTICA_WEB_TARGET"):
        raise SystemExit("Set MULTICA_WEB_TARGET to a separate frontend staging copy.")
    if REPO == HERE.parent or REPO == (HERE.parent / "multica").resolve() or (REPO / ".git").exists():
        raise SystemExit("Refusing to patch a repository checkout; use a separate staging copy.")
    if not (REPO / "apps/web/package.json").is_file():
        raise SystemExit("MULTICA_WEB_TARGET is not a frontend staging copy.")
    if BACKUP == REPO or REPO not in BACKUP.parents:
        raise SystemExit("MULTICA_WEB_BACKUP must be inside the staging copy.")


def plan_patches():
    validate_target()
    planned = []
    errors = []
    for rel, replacements in CHANGES.items():
        if not replacements:
            continue
        source = REPO / rel
        backup = BACKUP / rel
        current = source.read_text()
        original = backup.read_text() if backup.exists() else current
        text = original
        for number, (before, after) in enumerate(replacements, 1):
            if text.count(before) != 1:
                errors.append(
                    f"{rel} anchor {number}: found {text.count(before)}, expected exactly 1; "
                    f"starts with {before[:100]!r}"
                )
                break
            text = text.replace(before, after, 1)
        else:
            if current not in (original, text):
                errors.append(f"{rel}: staging file changed after patching; create a fresh copy")
            else:
                planned.append((rel, original, text, current == text))
    if errors:
        raise SystemExit("Upstream changed; no files written:\n" + "\n".join(errors))
    return planned


# Reviewed v0.6.1 deltas from the former full-file overlays.
# Keep new upstream logic; every anchor is checked before any files are written.
PORTED_CHANGES = {
    'apps/web/app/(auth)/login/page.test.tsx': [
        ('    authStateRef.state.isLoading = false;\n    mockListWorkspaces.mockResolvedValue([]);\n    mockListMyInvitations.mockResolvedValue([]);\n  });\n\n  // Shared LoginPage behavior is canonical in\n', '    authStateRef.state.isLoading = false;\n    mockListWorkspaces.mockResolvedValue([]);\n    mockListMyInvitations.mockResolvedValue([]);\n  });\n\n  it("enters an existing workspace without an onboarding detour", async () => {\n    authStateRef.state.user = { id: "user-test", email: "fixture@example.test", onboarded_at: null };\n    mockListWorkspaces.mockResolvedValue([{ id: "ws-test", slug: "fixture" }]);\n    render(<LoginPage />, { wrapper: createWrapper() });\n    await waitFor(() => expect(mockReplace).toHaveBeenCalledWith("/fixture/issues"));\n    expect(mockReplace).not.toHaveBeenCalledWith("/onboarding");\n  });\n\n  it("preserves pending invitation routing before workspace selection", async () => {\n    authStateRef.state.user = { id: "user-test", email: "fixture@example.test", onboarded_at: null };\n    mockListMyInvitations.mockResolvedValue([{ id: "invite-test" }]);\n    render(<LoginPage />, { wrapper: createWrapper() });\n    await waitFor(() => expect(mockReplace).toHaveBeenCalledWith("/invitations"));\n  });\n\n  // Shared LoginPage behavior is canonical in\n'),
    ],
    'apps/web/app/(auth)/login/page.tsx': [
        ('      // fall through\n    }\n  }\n  return resolvePostAuthDestination(workspaces, hasOnboarded);\n}\n\nfunction LoginPageContent() {\n', '      // fall through\n    }\n  }\n  // Workspace membership is enough to enter this self-hosted web app.\n  return resolvePostAuthDestination(workspaces, true);\n}\n\nfunction LoginPageContent() {\n'),
    ],
    'apps/web/app/(auth)/onboarding/page.tsx': [
        ('"use client";\n\nimport { useEffect, useRef } from "react";\nimport { useRouter } from "next/navigation";\nimport { useAuthStore } from "@multica/core/auth";\nimport {\n  paths,\n  resolvePostAuthDestination,\n  useHasOnboarded,\n} from "@multica/core/paths";\nimport { useWorkspaceList } from "@multica/core/workspace";\nimport { CliInstallInstructions, OnboardingFlow } from "@multica/views/onboarding";\n\n/**\n * Web shell for the onboarding flow. The route is the platform chrome on\n * web (matching `WindowOverlay` on desktop); content is the shared\n * `<OnboardingFlow />`. Kept minimal — guard on auth, render, exit.\n *\n * Runtime-connected onboarding opens the Mika session that the final step\n * created and started. Other exits land on the workspace issues list, or root\n * when no workspace exists.\n *\n * `CliInstallInstructions` is passed in as the `runtimeInstructions`\n * slot so the flow can render it inside the CLI dialog. The commands it\n * shows are hardcoded — nothing environmental to thread through.\n */\nexport default function OnboardingPage() {\n  const router = useRouter();\n  const user = useAuthStore((s) => s.user);\n  const isLoading = useAuthStore((s) => s.isLoading);\n  const hasOnboarded = useHasOnboarded();\n  const { workspaces, ready: workspacesReady } = useWorkspaceList({\n    enabled: !!user,\n  });\n  // The bootstrap path calls refreshMe() before returning, which flips\n  // hasOnboarded to true while the page is still mounted. Without this\n  // flag the guard below races onComplete: the guard\'s router.replace\n  // (issues list) can overtake onComplete\'s router.push (guide issue),\n  // dropping the user on the wrong destination. Marking the page as\n  // "completing" right before onComplete navigates keeps the guard\n  // silent for the in-flight transition.\n  const completingRef = useRef(false);\n\n  useEffect(() => {\n    if (isLoading || !user) {\n      if (!isLoading && !user) router.replace(paths.login());\n      return;\n    }\n    if (!workspacesReady) return;\n    if (completingRef.current) return;\n    // Bounce out only when onboarding genuinely doesn\'t apply: the user is\n    // already onboarded. We deliberately don\'t bounce on `workspaces.length`\n    // here — the flow creates a workspace mid-onboarding, and a\n    // hasWorkspaces bounce here would kick the user out before runtime and\n    // Mika setup can run. The new entry-point\n    // judgment in callback / login handles "where should this user go on\n    // login" so OnboardingPage no longer needs to second-guess it.\n    if (hasOnboarded) {\n      router.replace(resolvePostAuthDestination(workspaces, hasOnboarded));\n    }\n  }, [isLoading, user, hasOnboarded, workspacesReady, workspaces, router]);\n\n  if (isLoading || !user || hasOnboarded) return null;\n\n  // Layout: page owns its own scroll (root layout sets `body {\n  // overflow: hidden }` for the app-shell convention). OnboardingFlow\n  // owns the per-step width constraint internally — Welcome renders a\n  // wide two-column hero, all other steps wrap themselves at max-w-xl.\n  return (\n    <div className="h-full overflow-y-auto bg-background">\n      <OnboardingFlow\n        onComplete={(ws, destination) => {\n          completingRef.current = true;\n          if (ws && destination?.kind === "chat") {\n            router.push(\n              paths.workspace(ws.slug).chatSession(destination.sessionId),\n            );\n          } else if (ws && destination?.kind === "issue") {\n            router.push(\n              paths.workspace(ws.slug).issueDetail(destination.issueId),\n            );\n          } else if (ws) {\n            router.push(paths.workspace(ws.slug).issues());\n          } else {\n            router.push(paths.root());\n          }\n        }}\n        runtimeInstructions={<CliInstallInstructions />}\n      />\n    </div>\n  );\n}\n', '"use client";\n\nimport { useEffect } from "react";\nimport { useRouter } from "next/navigation";\nimport { useAuthStore } from "@multica/core/auth";\nimport { paths, resolvePostAuthDestination } from "@multica/core/paths";\nimport { useWorkspaceList } from "@multica/core/workspace";\n\n// Preserve old links and OAuth returns without showing a setup detour.\n// A user without a workspace still gets the normal workspace creation form.\nexport default function OnboardingPage() {\n  const router = useRouter();\n  const user = useAuthStore((s) => s.user);\n  const isLoading = useAuthStore((s) => s.isLoading);\n  const { workspaces, ready } = useWorkspaceList({ enabled: !!user });\n  useEffect(() => {\n    if (isLoading) return;\n    if (!user) router.replace(paths.login());\n    else if (ready) router.replace(resolvePostAuthDestination(workspaces, true));\n  }, [isLoading, user, ready, workspaces, router]);\n  return null;\n}\n'),
    ],
    'apps/web/app/[workspaceSlug]/layout.test.tsx': [
        ('});\n\ndescribe("WorkspaceLayout", () => {\n  it("keeps loading instead of showing NoAccess when the initial list request fails", async () => {\n    state.workspaceError = true;\n    const { queryClient } = renderLayout();\n', '});\n\ndescribe("WorkspaceLayout", () => {\n  it("allows workspace members without reopening onboarding", async () => {\n    state.user = { id: "user-1", onboarded_at: null };\n    state.workspace = { id: "ws-1", slug: "acme" };\n    renderLayout();\n    expect(await screen.findByTestId("workspace-content")).toBeInTheDocument();\n    expect(state.replace).not.toHaveBeenCalledWith("/onboarding");\n  });\n\n  it("keeps loading instead of showing NoAccess when the initial list request fails", async () => {\n    state.workspaceError = true;\n    const { queryClient } = renderLayout();\n'),
    ],
    'apps/web/app/[workspaceSlug]/layout.tsx': [
        ('import { setCurrentWorkspace } from "@multica/core/platform";\nimport { useAuthStore } from "@multica/core/auth";\nimport { NoAccessPage } from "@multica/views/workspace/no-access-page";\nimport { WelcomeAfterOnboarding } from "@multica/views/workspace/welcome-after-onboarding";\nimport { MulticaIcon } from "@multica/ui/components/common/multica-icon";\nimport { useWorkspaceSeen } from "@multica/views/workspace/use-workspace-seen";\nimport { workspaceSlugFromPathname } from "@/lib/workspace-slug-from-pathname";\n', 'import { setCurrentWorkspace } from "@multica/core/platform";\nimport { useAuthStore } from "@multica/core/auth";\nimport { NoAccessPage } from "@multica/views/workspace/no-access-page";\nimport { MulticaIcon } from "@multica/ui/components/common/multica-icon";\nimport { useWorkspaceSeen } from "@multica/views/workspace/use-workspace-seen";\nimport { workspaceSlugFromPathname } from "@/lib/workspace-slug-from-pathname";\n'),
        ('  useEffect(() => {\n    if (!isAuthLoading && !user) router.replace(paths.login());\n  }, [isAuthLoading, user, router]);\n\n  // Hard onboarding gate. Authenticated user but onboarded_at NULL means\n  // they bypassed /onboarding (typed the URL, deeplink, etc.). Redirect\n  // back so the questionnaire + Step 3 finish. The reverse gate lives in\n  // `apps/web/app/(auth)/onboarding/page.tsx` — onboarded users hitting\n  // /onboarding bounce out to their workspace. Together those two effects\n  // make `onboarded_at` the single source of truth for "may access /<slug>/*".\n  useEffect(() => {\n    if (user && user.onboarded_at == null) {\n      router.replace(paths.onboarding());\n    }\n  }, [user, router]);\n\n  // Resolve workspace by slug through the shared workspace-list query. A\n  // warm auth bootstrap reuses its cache; a cold route fetches it directly.\n', '  useEffect(() => {\n    if (!isAuthLoading && !user) router.replace(paths.login());\n  }, [isAuthLoading, user, router]);\n\n  // Resolve workspace by slug through the shared workspace-list query. A\n  // warm auth bootstrap reuses its cache; a cold route fetches it directly.\n'),
        ('  return (\n    <WorkspaceSlugProvider slug={workspaceSlug}>\n      {children}\n      {/* Reads the welcome-store transient signal parked by\n       *  OnboardingFlow.handleRuntimeNext. Runtime path → loading veil →\n       *  blocking Modal with Helper + starter cards. Skip path → Modal\n       *  with two seeded issues. No signal → null. */}\n      <WelcomeAfterOnboarding />\n    </WorkspaceSlugProvider>\n  );\n}\n', '  return (\n    <WorkspaceSlugProvider slug={workspaceSlug}>\n      {children}\n    </WorkspaceSlugProvider>\n  );\n}\n'),
    ],
    'apps/web/postcss.config.mjs': [
        ('/** @type {import(\'postcss-load-config\').Config} */\nconst config = {\n  plugins: {\n    "@tailwindcss/postcss": {},\n  },\n}\n\nexport default config\n', 'const config = {\n  plugins: {\n    "@tailwindcss/postcss": {},\n    "./compat/postcss.cjs": {},\n  },\n};\nexport default config;\n'),
    ],
    'apps/web/proxy.test.ts': [
        ('    expect(redirectLocation("/acme/squads", sessionCookies)).toBeNull();\n  });\n\n  it("redirects app-host root URLs to the last workspace", () => {\n    expect(redirectLocation("/", sessionCookies)).toBe(\n      "https://app.multica.test/acme/issues",\n    );\n  });\n\n  it.each(["multica.ai", "www.multica.ai"])(\n', '    expect(redirectLocation("/acme/squads", sessionCookies)).toBeNull();\n  });\n\n  it("rewrites app-host root URLs to login for session resolution", () => {\n    const response = proxy(makeRequest("/", sessionCookies));\n    expect(response.headers.get("location")).toBeNull();\n    expect(response.headers.get("x-middleware-rewrite")).toBe("https://app.multica.test/login");\n  });\n\n  it.each(["multica.ai", "www.multica.ai"])(\n'),
        ('});\n\ndescribe("proxy root and locale handling", () => {\n  it("redirects logged-in root visits to the last workspace", () => {\n    const res = proxy(\n      makeRequest("/", {\n        multica_logged_in: "1",\n', '});\n\ndescribe("proxy root and locale handling", () => {\n  it("rewrites logged-in root visits to login to resolve current membership", () => {\n    const res = proxy(\n      makeRequest("/", {\n        multica_logged_in: "1",\n'),
        ('      }),\n    );\n\n    expect(res.status).toBe(307);\n    expect(res.headers.get("location")).toBe(\n      "https://app.multica.test/acme/issues",\n    );\n  });\n\n', '      }),\n    );\n\n    expect(res.status).toBe(200);\n    expect(res.headers.get("location")).toBeNull();\n    expect(res.headers.get("x-middleware-rewrite")).toBe(\n      "https://app.multica.test/login",\n    );\n  });\n\n'),
    ],
    'packages/core/api/client.ts': [
        ('  }\n\n  async getSkill(id: string): Promise<Skill> {\n    return this.fetch(`/api/skills/${id}`);\n  }\n\n  async createSkill(data: CreateSkillRequest): Promise<Skill> {\n', '  }\n\n  async getSkill(id: string): Promise<Skill> {\n    const raw = await this.fetch<unknown>(`/api/skills/${id}`);\n    return parseWithFallback(raw, SkillSchema, EMPTY_SKILL, {\n      endpoint: "GET /api/skills/{id}",\n    });\n  }\n\n  async createSkill(data: CreateSkillRequest): Promise<Skill> {\n'),
        ('    await this.fetch(`/api/skills/${id}`, { method: "DELETE" });\n  }\n\n  async importSkill(data: { url: string }): Promise<Skill> {\n    return this.fetch("/api/skills/import", {\n      method: "POST",\n      body: JSON.stringify(data),\n', '    await this.fetch(`/api/skills/${id}`, { method: "DELETE" });\n  }\n\n  async changeSkillScope(\n    id: string,\n    data: { scope: "personal" | "workspace" | "team"; workspace_id?: string },\n  ): Promise<Skill> {\n    const raw = await this.fetch<unknown>(`/api/skills/${id}/scope`, {\n      method: "PUT",\n      body: JSON.stringify(data),\n    });\n    return parseWithFallback(raw, SkillSchema, EMPTY_SKILL, {\n      endpoint: "PUT /api/skills/{id}/scope",\n    });\n  }\n\n  async importSkill(data: { url: string; scope?: "personal" | "workspace" | "team" }): Promise<Skill> {\n    return this.fetch("/api/skills/import", {\n      method: "POST",\n      body: JSON.stringify(data),\n'),
        ('  async importSkillArchive(\n    file: File,\n    onConflict?: "fail" | "overwrite" | "rename" | "skip",\n  ): Promise<Skill> {\n    const formData = new FormData();\n    formData.append("file", file, file.name || "skill.zip");\n    if (onConflict) formData.append("on_conflict", onConflict);\n\n    let res: Response;\n    try {\n', '  async importSkillArchive(\n    file: File,\n    onConflict?: "fail" | "overwrite" | "rename" | "skip",\n    scope?: "personal" | "workspace" | "team",\n  ): Promise<Skill> {\n    const formData = new FormData();\n    formData.append("file", file, file.name || "skill.zip");\n    if (onConflict) formData.append("on_conflict", onConflict);\n    if (scope) formData.append("scope", scope);\n\n    let res: Response;\n    try {\n'),
    ],
    'packages/core/api/schemas.ts': [
        ('export const SkillSummarySchema = z.object({\n  id: z.string(),\n  workspace_id: z.string(),\n  name: z.string(),\n  description: z.string().optional().default(""),\n  config: z.record(z.string(), z.unknown()).optional().default({}),\n', 'export const SkillSummarySchema = z.object({\n  id: z.string(),\n  workspace_id: z.string(),\n  workspace_name: z.string().catch(""),\n  scope: z.enum(["personal", "workspace", "team"]).catch("workspace"),\n  owner_user_id: z.string().nullable().catch(null),\n  usage_count: z.number().int().nonnegative().catch(0),\n  name: z.string(),\n  description: z.string().optional().default(""),\n  config: z.record(z.string(), z.unknown()).optional().default({}),\n'),
    ],
    'packages/core/skills/stores/view-store.ts': [
        ('// they are session-scoped, and persisting them would greet returning users\n// with an inexplicably narrowed list.\n\nexport type SkillSortField = "name" | "usedBy" | "updated" | "created";\n\nexport type SkillSortDirection = "asc" | "desc";\n\n', '// they are session-scoped, and persisting them would greet returning users\n// with an inexplicably narrowed list.\n\nexport type SkillSortField = "name" | "usedBy" | "usageCount" | "updated" | "created";\n\nexport type SkillSortDirection = "asc" | "desc";\n\n'),
        ('> = {\n  name: "asc",\n  usedBy: "desc",\n  updated: "desc",\n  created: "desc",\n};\n', '> = {\n  name: "asc",\n  usedBy: "desc",\n  usageCount: "desc",\n  updated: "desc",\n  created: "desc",\n};\n'),
        ('  origins: SkillOriginType[];\n  agents: string[];\n  creators: string[];\n  labels: string[];\n}\n\n', '  origins: SkillOriginType[];\n  agents: string[];\n  creators: string[];\n  /** Additional project/workspace ids to reveal; current project is implicit. */\n  projects: string[];\n  labels: string[];\n}\n\n'),
        ('  origins: [],\n  agents: [],\n  creators: [],\n  labels: [],\n};\n\n// User-hideable columns. Name and the structural columns (checkbox, kebab)\n// are always visible.\nexport type SkillColumnKey =\n  | "usedBy"\n  | "source"\n  | "creator"\n  | "updated"\n', '  origins: [],\n  agents: [],\n  creators: [],\n  projects: [],\n  labels: [],\n};\n\n// User-hideable columns. Name and the structural columns (checkbox, kebab)\n// are always visible.\nexport type SkillColumnKey =\n  | "scope"\n  | "usedBy"\n  | "usageCount"\n  | "source"\n  | "creator"\n  | "updated"\n'),
    ],
    'packages/core/types/agent.ts': [
        ('  skills: AgentSkillSummary[];\n  /** Runtime-local skills this agent must not inherit. Older servers omit it. */\n  disabled_runtime_skills?: DisabledRuntimeSkill[];\n  created_at: string;\n  updated_at: string;\n  archived_at: string | null;\n', '  skills: AgentSkillSummary[];\n  /** Runtime-local skills this agent must not inherit. Older servers omit it. */\n  disabled_runtime_skills?: DisabledRuntimeSkill[];\n  /** Whether runtime-local skills are enabled when no per-skill override exists. */\n  runtime_skills_default_enabled?: boolean;\n  created_at: string;\n  updated_at: string;\n  archived_at: string | null;\n'),
        ('  key: string;\n  name?: string;\n  plugin?: string;\n}\n\nexport interface SetAgentRuntimeSkillEnabledRequest {\n', '  key: string;\n  name?: string;\n  plugin?: string;\n  /** Explicit opt-in marker used when the agent default is disabled. */\n  enabled?: boolean;\n}\n\nexport interface SetAgentRuntimeSkillEnabledRequest {\n'),
        ('export interface SkillSummary {\n  id: string;\n  workspace_id: string;\n  name: string;\n  description: string;\n  config: Record<string, unknown>;\n  created_by: string | null;\n  created_at: string;\n  updated_at: string;\n  /** Present only when returned from an agent-scoped assignment endpoint. */\n  enabled?: boolean;\n  /** Present on workspace skill lists after a backend that bulk-attaches labels. */\n', 'export interface SkillSummary {\n  id: string;\n  workspace_id: string;\n  workspace_name?: string;\n  name: string;\n  description: string;\n  config: Record<string, unknown>;\n  created_by: string | null;\n  created_at: string;\n  updated_at: string;\n  scope?: "personal" | "workspace" | "team";\n  owner_user_id?: string | null;\n  usage_count?: number;\n  /** Present only when returned from an agent-scoped assignment endpoint. */\n  enabled?: boolean;\n  /** Present on workspace skill lists after a backend that bulk-attaches labels. */\n'),
        ('}\n\nexport interface CreateSkillRequest {\n  name: string;\n  description?: string;\n  content?: string;\n', '}\n\nexport interface CreateSkillRequest {\n  scope?: "personal" | "workspace" | "team";\n  name: string;\n  description?: string;\n  content?: string;\n'),
        ('}\n\nexport interface CreateRuntimeLocalSkillImportRequest {\n  skill_key: string;\n  name?: string;\n  description?: string;\n', '}\n\nexport interface CreateRuntimeLocalSkillImportRequest {\n  scope?: "personal" | "workspace" | "team";\n  skill_key: string;\n  name?: string;\n  description?: string;\n'),
    ],
    'packages/views/agents/components/tabs/skills-tab.tsx': [
        ('            {agent.skills.map((skill) => {\n              const enabled = skill.enabled !== false;\n              const busy = busyId === skill.id;\n              return (\n                <li key={skill.id} className="flex items-center gap-3 p-3">\n                  <button\n', '            {agent.skills.map((skill) => {\n              const enabled = skill.enabled !== false;\n              const busy = busyId === skill.id;\n              const metadata = workspaceSkills.find((candidate) => candidate.id === skill.id);\n              const scope = (metadata?.scope ?? "workspace") as string;\n              const scopeLabel =\n                scope === "team"\n                  ? t(($) => $.create_dialog.skills_section.scope_team)\n                  : scope === "personal"\n                    ? t(($) => $.create_dialog.skills_section.scope_personal)\n                    : t(($) => $.create_dialog.skills_section.scope_project_prefix) +\n                      (metadata?.workspace_name ||\n                        t(($) => $.create_dialog.skills_section.scope_project_default));\n              return (\n                <li key={skill.id} className="flex items-center gap-3 p-3">\n                  <button\n'),
        ('                      <SkillIcon className="h-4 w-4" />\n                    </span>\n                    <span className="min-w-0 flex-1">\n                      <span className={cn("block text-body font-medium", !enabled && "text-muted-foreground")}>\n                        {skill.name}\n                      </span>\n                      <span className="block truncate text-caption text-muted-foreground">\n                        {skill.description || t(($) => $.tab_body.skills.no_description)}\n', '                      <SkillIcon className="h-4 w-4" />\n                    </span>\n                    <span className="min-w-0 flex-1">\n                      <span className="flex min-w-0 items-center gap-2">\n                        <span className={cn("min-w-0 truncate text-body font-medium", !enabled && "text-muted-foreground")}>\n                          {skill.name}\n                        </span>\n                        <span className="shrink-0 rounded border border-border/60 px-1.5 py-0.5 text-micro font-medium text-muted-foreground">\n                          {scopeLabel}\n                        </span>\n                      </span>\n                      <span className="block truncate text-caption text-muted-foreground">\n                        {skill.description || t(($) => $.tab_body.skills.no_description)}\n'),
        ('                agent.disabled_runtime_skills,\n                runtime?.id,\n                skill,\n              );\n              const busyKey = runtimeSkillIdentity(skill);\n              const busy = busyId === busyKey;\n', '                agent.disabled_runtime_skills,\n                runtime?.id,\n                skill,\n                agent.runtime_skills_default_enabled !== false,\n              );\n              const busyKey = runtimeSkillIdentity(skill);\n              const busy = busyId === busyKey;\n'),
        ('  disabledSkills: DisabledRuntimeSkill[] | undefined,\n  runtimeId: string | undefined,\n  skill: RuntimeLocalSkillSummary,\n): boolean {\n  if (!runtimeId || !skill.root) return false;\n  return (disabledSkills ?? []).some(\n    (disabled) =>\n      disabled.runtime_id === runtimeId &&\n      disabled.provider === skill.provider &&\n', '  disabledSkills: DisabledRuntimeSkill[] | undefined,\n  runtimeId: string | undefined,\n  skill: RuntimeLocalSkillSummary,\n  defaultEnabled: boolean,\n): boolean {\n  if (!runtimeId || !skill.root) return false;\n  const override = (disabledSkills ?? []).find(\n    (disabled) =>\n      disabled.runtime_id === runtimeId &&\n      disabled.provider === skill.provider &&\n'),
        ('      disabled.key === skill.key &&\n      (disabled.plugin ?? "") === (skill.plugin ?? ""),\n  );\n}\n\nfunction CapabilitySection({\n', '      disabled.key === skill.key &&\n      (disabled.plugin ?? "") === (skill.plugin ?? ""),\n  );\n  if (defaultEnabled) return override !== undefined;\n  return override?.enabled !== true;\n}\n\nfunction CapabilitySection({\n'),
    ],
    'packages/views/chat/floating-chat.tsx': [
        ('import { useChatStore } from "@multica/core/chat";\nimport { useWorkspacePaths } from "@multica/core/paths";\nimport { useNavigation } from "../navigation";\nimport { ChatFab } from "./components/chat-fab";\nimport { ChatWindow } from "./components/chat-window";\nimport { isFloatingChatRouteSuppressed } from "./floating-chat-visibility";\n\n/**\n * Mount point for the floating chat overlay (FAB + window). Rendered once in\n * each app shell\'s dashboard layout; owns the two gates that decide whether the\n * overlay exists at all:\n *\n *  1. The Settings → Chat preference (`floatingChatEnabled`). When a user turns\n *     the floating window off, Chat lives only in its dedicated tab.\n *  2. The Chat tab route itself. On `/:slug/chat` the full-page surface already\n *     owns the conversation, so a floating copy of the same `activeSessionId`\n *     would be pure duplication — hide it there.\n */\nexport function FloatingChat() {\n  const enabled = useChatStore((s) => s.floatingChatEnabled);\n', 'import { useChatStore } from "@multica/core/chat";\nimport { useWorkspacePaths } from "@multica/core/paths";\nimport { useNavigation } from "../navigation";\nimport { ChatWindow } from "./components/chat-window";\nimport { isFloatingChatRouteSuppressed } from "./floating-chat-visibility";\n\n/**\n * Keep the floating conversation window and its existing visibility gates,\n * but do not mount the bottom-right launcher in either browser style branch.\n * The sidebar Chat page, issue comments and existing keyboard actions remain.\n */\nexport function FloatingChat() {\n  const enabled = useChatStore((s) => s.floatingChatEnabled);\n'),
        ('  const wsPaths = useWorkspacePaths();\n\n  if (!enabled) return null;\n  // Suppress on the Chat tab — it renders the same conversation full-page.\n  if (isFloatingChatRouteSuppressed(pathname, wsPaths.chat())) return null;\n\n  return (\n    <>\n      <ChatWindow />\n      <ChatFab />\n    </>\n  );\n}\n', '  const wsPaths = useWorkspacePaths();\n\n  if (!enabled) return null;\n  if (isFloatingChatRouteSuppressed(pathname, wsPaths.chat())) return null;\n\n  return <ChatWindow />;\n}\n'),
    ],
    'packages/views/common/cli-install-command.tsx': [
        ('  labels: CliInstallCommandLabels;\n  /** Palette overrides for surfaces that do not use the product tokens. */\n  classNames?: { list?: string; trigger?: string };\n  children: (command: string) => ReactNode;\n}) {\n  return (\n    <Tabs defaultValue={PLATFORMS[0]}>\n', '  labels: CliInstallCommandLabels;\n  /** Palette overrides for surfaces that do not use the product tokens. */\n  classNames?: { list?: string; trigger?: string };\n  children: (command: string, platform: CliInstallPlatform) => ReactNode;\n}) {\n  return (\n    <Tabs defaultValue={PLATFORMS[0]}>\n'),
        ('      </TabsList>\n      {PLATFORMS.map((platform) => (\n        <TabsContent key={platform} value={platform}>\n          {children(CLI_INSTALL_COMMANDS[platform])}\n        </TabsContent>\n      ))}\n    </Tabs>\n', '      </TabsList>\n      {PLATFORMS.map((platform) => (\n        <TabsContent key={platform} value={platform}>\n          {children(CLI_INSTALL_COMMANDS[platform], platform)}\n        </TabsContent>\n      ))}\n    </Tabs>\n'),
    ],
    'packages/views/runtimes/components/connect-remote-dialog.test.tsx': [
        ('import { ConnectRemoteDialog } from "./connect-remote-dialog";\n\nconst TEST_RESOURCES = { en: { common: enCommon, runtimes: enRuntimes } };\n\n// Mocked at the module boundary rather than through navigator.clipboard: jsdom\n// exposes no clipboard, and user-event installs a getter-only stub of its own\n', 'import { ConnectRemoteDialog } from "./connect-remote-dialog";\n\nconst TEST_RESOURCES = { en: { common: enCommon, runtimes: enRuntimes } };\nconst apiMocks = vi.hoisted(() => ({ token: vi.fn(), runtimes: vi.fn() }));\nvi.mock("@multica/core/api", () => ({ api: {\n  getOrCreateDaemonBootstrapToken: apiMocks.token,\n  listRuntimes: apiMocks.runtimes,\n} }));\nvi.mock("@multica/core/auth", () => {\n  const state = { user: { id: "user-test" } };\n  return { useAuthStore: Object.assign((selector: (s: typeof state) => unknown) => selector(state), { getState: () => state }) };\n});\n\n\n// Mocked at the module boundary rather than through navigator.clipboard: jsdom\n// exposes no clipboard, and user-event installs a getter-only stub of its own\n'),
        ('describe("ConnectRemoteDialog", () => {\n  beforeEach(() => {\n    wsEventState.handler = null;\n    clipboard.copyText.mockReset().mockResolvedValue(true);\n  });\n\n  it("uses cloud setup commands by default", () => {\n    const { baseElement } = renderDialog();\n\n    expect(baseElement).toHaveTextContent("multica setup");\n    expect(baseElement).not.toHaveTextContent("multica setup self-host");\n    expect(baseElement).toHaveTextContent(\n      "multica config set server_url https://api.multica.ai",\n    );\n    expect(baseElement).toHaveTextContent(\n      "multica config set app_url https://multica.ai",\n    );\n  });\n\n  it("uses self-host daemon URLs from runtime config", () => {\n    const { baseElement } = renderDialog({\n      daemonServerUrl: "https://api.example.com/",\n      daemonAppUrl: "https://app.example.com/",\n    });\n\n    expect(baseElement).toHaveTextContent(\n      "multica setup self-host --server-url https://api.example.com --app-url https://app.example.com",\n    );\n    expect(baseElement).toHaveTextContent(\n      "multica config set server_url https://api.example.com",\n    );\n    expect(baseElement).toHaveTextContent(\n      "multica config set app_url https://app.example.com",\n    );\n  });\n\n', 'describe("ConnectRemoteDialog", () => {\n  beforeEach(() => {\n    wsEventState.handler = null;\n    apiMocks.token.mockReset().mockResolvedValue({ token: "fixture-token" });\n    apiMocks.runtimes.mockReset().mockResolvedValue([]);\n    clipboard.copyText.mockReset().mockResolvedValue(true);\n  });\n\n  it("uses cloud token commands by default", async () => {\n    const { baseElement } = renderDialog();\n    await waitFor(() => expect(baseElement).toHaveTextContent("fixture-token"));\n\n    expect(baseElement).toHaveTextContent("multica ducc setup");\n    expect(baseElement).not.toHaveTextContent("multica setup self-host");\n    expect(baseElement).toHaveTextContent(\n      "multica config set server_url \'https://api.multica.ai\'",\n    );\n    expect(baseElement).toHaveTextContent(\n      "multica config set app_url \'https://multica.ai\'",\n    );\n  });\n\n  it("uses self-host daemon URLs from runtime config", async () => {\n    const { baseElement } = renderDialog({\n      daemonServerUrl: "https://api.example.com/",\n      daemonAppUrl: "https://app.example.com/",\n    });\n    await waitFor(() => expect(baseElement).toHaveTextContent("fixture-token"));\n\n    expect(baseElement).not.toHaveTextContent("setup self-host");\n    expect(baseElement).not.toHaveTextContent("Use browser sign-in");\n    expect(baseElement).toHaveTextContent(\n      "multica config set server_url \'https://api.example.com\'",\n    );\n    expect(baseElement).toHaveTextContent(\n      "multica config set app_url \'https://app.example.com\'",\n    );\n  });\n\n'),
        ('  // is wired through to step 1\'s copy button.\n  it("copies the installer for the platform picked in step 1", async () => {\n    const user = userEvent.setup();\n    renderDialog();\n\n    await user.click(screen.getByRole("tab", { name: "Windows" }));\n    await user.click(screen.getAllByRole("button", { name: "Copy" })[0]!);\n\n    await waitFor(() => {\n      expect(clipboard.copyText).toHaveBeenCalledWith(WINDOWS_CMD);\n    });\n  });\n\n  it("transitions from setup instructions to the connected state", async () => {\n    const { baseElement } = renderDialog();\n\n    expect(baseElement).toHaveTextContent("multica setup");\n    act(() => {\n      wsEventState.handler?.({ runtime_id: "rt-test" });\n    });\n\n    await waitFor(() => {\n', '  // is wired through to step 1\'s copy button.\n  it("copies the installer for the platform picked in step 1", async () => {\n    const user = userEvent.setup();\n    const { baseElement } = renderDialog();\n    await waitFor(() => expect(baseElement).toHaveTextContent("fixture-token"));\n    await user.click(screen.getByRole("tab", { name: "Windows" }));\n    await user.click(screen.getAllByRole("button", { name: "Copy" })[0]!);\n\n    await waitFor(() => {\n      expect(clipboard.copyText).toHaveBeenCalledWith(expect.stringContaining(WINDOWS_CMD));\n      expect(clipboard.copyText).toHaveBeenCalledWith(expect.stringContaining("login --token \'fixture-token\'"));\n      expect(screen.getAllByRole("button", { name: "Copy" })).toHaveLength(1);\n    });\n  });\n\n  it("transitions from setup instructions to the connected state", async () => {\n    const { baseElement } = renderDialog();\n\n    expect(baseElement).toHaveTextContent("multica ducc setup");\n    await waitFor(() => expect(apiMocks.runtimes).toHaveBeenCalledTimes(1));\n    apiMocks.runtimes.mockResolvedValue([{ id: "rt-test", daemon_id: "new-computer", workspace_id: "ws-test", owner_id: "user-test", runtime_mode: "local", status: "online" }]);\n    act(() => {\n      wsEventState.handler?.({ runtimes: [{ id: "rt-test", runtime_mode: "local", status: "online" }] });\n    });\n\n    await waitFor(() => {\n'),
        ('        screen.getByRole("button", { name: "Create an agent" }),\n      ).toBeInTheDocument();\n    });\n    expect(baseElement).not.toHaveTextContent("multica setup");\n  });\n});\n', '        screen.getByRole("button", { name: "Create an agent" }),\n      ).toBeInTheDocument();\n    });\n    expect(baseElement).not.toHaveTextContent("multica ducc setup");\n  });\n});\n'),
    ],
    'packages/views/runtimes/components/connect-remote-dialog.tsx': [
        ('import { Check, ChevronRight, Copy, Terminal } from "lucide-react";\nimport { useQueryClient } from "@tanstack/react-query";\nimport { useWorkspaceId } from "@multica/core/hooks";\nimport { runtimeKeys } from "@multica/core/runtimes/queries";\nimport { useWSEvent } from "@multica/core/realtime";\nimport { paths, useWorkspaceSlug } from "@multica/core/paths";\nimport { useConfigStore } from "@multica/core/config";\nimport {\n  Dialog,\n  DialogContent,\n', 'import { Check, ChevronRight, Copy, Terminal } from "lucide-react";\nimport { useQueryClient } from "@tanstack/react-query";\nimport { useWorkspaceId } from "@multica/core/hooks";\nimport { runtimeListOptions } from "@multica/core/runtimes/queries";\nimport { hasOnlineRegistration, findNewConnectedRuntime } from "./connection-result";\nimport { useWSEvent } from "@multica/core/realtime";\nimport { paths, useWorkspaceSlug } from "@multica/core/paths";\nimport { useConfigStore } from "@multica/core/config";\nimport { useAuthStore } from "@multica/core/auth";\nimport { api } from "@multica/core/api";\nimport {\n  Dialog,\n  DialogContent,\n'),
        ('import { CliInstallCommand } from "../../common/cli-install-command";\nimport { useNavigation } from "../../navigation";\nimport { useT } from "../../i18n";\n\ntype Step = "instructions" | "success";\n\nconst CLOUD_SERVER_URL = "https://api.multica.ai";\nconst CLOUD_APP_URL = "https://multica.ai";\n\nfunction normalizeCommandURL(url: string | undefined) {\n  return url?.trim().replace(/\\/+$/, "") ?? "";\n}\n\nfunction daemonCommands(serverUrl: string | undefined, appUrl: string | undefined) {\n  const normalizedServerUrl = normalizeCommandURL(serverUrl);\n  const normalizedAppUrl = normalizeCommandURL(appUrl);\n  if (normalizedServerUrl && normalizedAppUrl) {\n    return {\n      setupCmd: `multica setup self-host --server-url ${normalizedServerUrl} --app-url ${normalizedAppUrl}`,\n      tokenCmd: `multica config set server_url ${normalizedServerUrl}\nmultica config set app_url ${normalizedAppUrl}\nmultica login --token <YOUR_TOKEN>\nmultica daemon start`,\n    };\n  }\n\n  return {\n    setupCmd: "multica setup",\n    tokenCmd: `multica config set server_url ${CLOUD_SERVER_URL}\nmultica config set app_url ${CLOUD_APP_URL}\nmultica login --token <YOUR_TOKEN>\nmultica daemon start`,\n  };\n}\n\nexport function ConnectRemoteDialog({ onClose }: { onClose: () => void }) {\n  const [step, setStep] = useState<Step>("instructions");\n', 'import { CliInstallCommand } from "../../common/cli-install-command";\nimport { useNavigation } from "../../navigation";\nimport { useT } from "../../i18n";\nimport { connectionCommand } from "./connection-commands";\n\nconst CONNECTION_DEFAULTS = {};\n\ntype Step = "instructions" | "success";\n\nexport function ConnectRemoteDialog({ onClose }: { onClose: () => void }) {\n  const [step, setStep] = useState<Step>("instructions");\n'),
        ('  const navigation = useNavigation();\n  const shouldReduceMotion = useReducedMotion() ?? false;\n  const newRuntimeIdRef = useRef<string | null>(null);\n\n  // `multica setup` is one blocking command that handles config + login\n  // + daemon start; the dialog passively listens for the resulting\n  // `daemon:register` WS event and auto-advances to success.\n  const handleDaemonRegister = useCallback(\n    (payload: unknown) => {\n      if (step !== "instructions") return;\n      qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });\n      const p = payload as Record<string, unknown> | null;\n      if (p?.runtime_id && typeof p.runtime_id === "string") {\n        newRuntimeIdRef.current = p.runtime_id;\n      }\n      setStep("success");\n    },\n    [step, qc, wsId],\n  );\n  useWSEvent("daemon:register", handleDaemonRegister);\n\n', '  const navigation = useNavigation();\n  const shouldReduceMotion = useReducedMotion() ?? false;\n  const newRuntimeIdRef = useRef<string | null>(null);\n  const userId = useAuthStore((state) => state.user?.id);\n  const knownComputersRef = useRef<Set<string> | null>(null);\n  useEffect(() => {\n    let cancelled = false;\n    knownComputersRef.current = null;\n    void qc.fetchQuery({ ...runtimeListOptions(wsId), staleTime: 0 }).then(rows => {\n      if (!cancelled) knownComputersRef.current = new Set(rows.map(row => row.daemon_id).filter((id): id is string => !!id));\n    }).catch(() => { /* Keep instructions visible if the baseline is unavailable. */ });\n    return () => { cancelled = true; knownComputersRef.current = null; };\n  }, [qc, wsId, userId]);\n\n  // `multica setup` is one blocking command that handles config + login\n  // + daemon start; the dialog passively listens for the resulting\n  // `daemon:register` WS event and auto-advances to success.\n  const handleDaemonRegister = useCallback(\n    (payload: unknown) => {\n      const known = knownComputersRef.current;\n      if (step !== "instructions" || !known || !hasOnlineRegistration(payload)) return;\n      void qc.fetchQuery({ ...runtimeListOptions(wsId), staleTime: 0 }).then(rows => {\n        if (knownComputersRef.current !== known || newRuntimeIdRef.current) return;\n        const connected = findNewConnectedRuntime(rows, known, wsId, userId);\n        if (!connected) return;\n        newRuntimeIdRef.current = connected.id;\n        setStep("success");\n      }).catch(() => { /* A notification alone is never proof of connection. */ });\n    },\n    [step, qc, wsId, userId],\n  );\n  useWSEvent("daemon:register", handleDaemonRegister);\n\n'),
        ('  const { t } = useT("runtimes");\n  const daemonServerUrl = useConfigStore((s) => s.daemonServerUrl);\n  const daemonAppUrl = useConfigStore((s) => s.daemonAppUrl);\n  const { setupCmd, tokenCmd } = daemonCommands(daemonServerUrl, daemonAppUrl);\n  return (\n    <>\n      <DialogHeader className="px-6 pt-6 pb-2">\n', '  const { t } = useT("runtimes");\n  const daemonServerUrl = useConfigStore((s) => s.daemonServerUrl);\n  const daemonAppUrl = useConfigStore((s) => s.daemonAppUrl);\n  const email = useAuthStore((s) => s.user?.email);\n  const userId = useAuthStore((s) => s.user?.id);\n  const [cliToken, setCliToken] = useState<string>();\n  useEffect(() => {\n    let cancelled = false;\n    setCliToken(undefined);\n    if (userId) {\n      void api.getOrCreateDaemonBootstrapToken().then(({ token }) => {\n        if (!cancelled && token) setCliToken(token);\n      }).catch(() => {});\n    }\n    return () => { cancelled = true; };\n  }, [userId]);\n  return (\n    <>\n      <DialogHeader className="px-6 pt-6 pb-2">\n'),
        ('\n      <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">\n        <div className="space-y-4">\n          {/* Step 1 owns the platform switch: the install command differs by\n              OS, so the user picks instead of us guessing. Step 2\'s\n              `multica setup` is platform-independent. */}\n          <CommandStep n={1} label={t(($) => $.connect.step1_label)}>\n            <CliInstallCommand\n              labels={{\n                group: t(($) => $.connect.platform_group),\n', '\n      <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">\n        <div className="space-y-4">\n          <CommandStep n={1} label={t(($) => $.connect.combined_label)}>\n            <CliInstallCommand\n              labels={{\n                group: t(($) => $.connect.platform_group),\n'),
        ('                windows: t(($) => $.connect.platform_windows),\n              }}\n            >\n              {(cmd) => (\n                <CommandRow\n                  cmd={cmd}\n                  copyAria={t(($) => $.connect.copy_aria)}\n                />\n              )}\n            </CliInstallCommand>\n          </CommandStep>\n\n          <div>\n            <CommandStep n={2} label={t(($) => $.connect.step2_label)}>\n              <CommandRow\n                cmd={setupCmd}\n                copyAria={t(($) => $.connect.copy_aria)}\n              />\n            </CommandStep>\n            <p className="mt-1.5 text-micro leading-[1.55] text-muted-foreground">\n              {t(($) => $.connect.step2_hint)}\n            </p>\n          </div>\n\n          <LiveListening />\n\n          <TroubleshootingDetails tokenCmd={tokenCmd} />\n        </div>\n      </div>\n\n', '                windows: t(($) => $.connect.platform_windows),\n              }}\n            >\n              {(cmd, platform) => (\n                <CommandRow\n                  cmd={connectionCommand(cmd, platform, {\n                    ...CONNECTION_DEFAULTS,\n                    serverUrl: daemonServerUrl,\n                    appUrl: daemonAppUrl,\n                    email,\n                    token: cliToken,\n                  })}\n                  copyAria={t(($) => $.connect.copy_aria)}\n                />\n              )}\n            </CliInstallCommand>\n          </CommandStep>\n          <p className="text-micro leading-[1.55] text-muted-foreground">\n            {t(($) => $.connect.token_step_hint)}\n          </p>\n          {!cliToken && (\n            <p className="text-micro leading-[1.55] text-muted-foreground">\n              {t(($) => $.connect.trouble_token_hint_prefix)}\n              <span className="font-medium text-foreground">\n                {t(($) => $.connect.trouble_token_hint_destination)}\n              </span>\n              {t(($) => $.connect.trouble_token_hint_suffix)}\n            </p>\n          )}\n\n          <LiveListening />\n\n        </div>\n      </div>\n\n'),
        ('        </Button>\n      </DialogFooter>\n    </>\n  );\n}\n\nfunction TroubleshootingDetails({ tokenCmd }: { tokenCmd: string }) {\n  const { t } = useT("runtimes");\n  return (\n    <details className="group rounded-lg border border-dashed">\n      <summary className="flex cursor-pointer list-none items-center gap-1.5 px-3 py-2 text-caption font-medium text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">\n        <ChevronRight\n          className="h-3 w-3 transition-transform group-open:rotate-90"\n          aria-hidden\n        />\n        {t(($) => $.connect.troubleshooting)}\n      </summary>\n      <div className="space-y-2 border-t px-3 pt-2.5 pb-3 text-micro leading-[1.55] text-muted-foreground">\n        <p>{t(($) => $.connect.trouble_intro)}</p>\n        <CommandStep n={2} label={t(($) => $.connect.step2_label)}>\n          <CommandRow\n            cmd={tokenCmd}\n            copyAria={t(($) => $.connect.copy_aria)}\n          />\n        </CommandStep>\n        <p>\n          {t(($) => $.connect.trouble_token_hint_prefix)}\n          <span className="font-medium text-foreground">\n            {t(($) => $.connect.trouble_token_hint_destination)}\n          </span>\n          {t(($) => $.connect.trouble_token_hint_suffix)}\n        </p>\n        <ul className="space-y-1">\n          <li className="flex items-center gap-1.5">\n            <span>{t(($) => $.connect.trouble_check_status)}</span>\n            {/* CLI command — literal shell string, not i18n content. */}\n            <code\n              className={cn(\n                "rounded-xs bg-muted px-1.5 py-0.5 font-mono text-micro text-foreground",\n                CODE_LIGATURE_CLASS,\n              )}\n            >\n              {"multica daemon status"}\n            </code>\n          </li>\n          <li className="flex items-center gap-1.5">\n            <span>{t(($) => $.connect.trouble_view_logs)}</span>\n            {/* CLI command — literal shell string, not i18n content. */}\n            <code\n              className={cn(\n                "rounded-xs bg-muted px-1.5 py-0.5 font-mono text-micro text-foreground",\n                CODE_LIGATURE_CLASS,\n              )}\n            >\n              {"multica daemon logs -f"}\n            </code>\n          </li>\n        </ul>\n      </div>\n    </details>\n  );\n}\n\n', '        </Button>\n      </DialogFooter>\n    </>\n  );\n}\n\n'),
    ],
    'packages/views/runtimes/components/runtimes-page.tsx': [
        ('import { HealthDot, HealthIcon, useHealthLabel } from "./shared";\nimport { useT, useTimeAgo } from "../../i18n";\nimport { daemonRuntimesDocsHref } from "./runtime-docs";\n\nexport interface RuntimesPageProps {\n  /** Desktop-only daemon id used to identify this device. */\n', 'import { HealthDot, HealthIcon, useHealthLabel } from "./shared";\nimport { useT, useTimeAgo } from "../../i18n";\nimport { daemonRuntimesDocsHref } from "./runtime-docs";\nimport { DeleteMachineButton } from "./delete-machine-button";\n\nexport interface RuntimesPageProps {\n  /** Desktop-only daemon id used to identify this device. */\n'),
        ('  const Icon = machine.section === "cloud" ? Cloud : Monitor;\n  const locator = machine.id;\n  const busyCount = machine.runningCount + machine.queuedCount;\n  const body = (\n    <>\n      <span className="relative flex h-10 w-10 shrink-0 items-center justify-center rounded-lg border bg-background">\n', '  const Icon = machine.section === "cloud" ? Cloud : Monitor;\n  const locator = machine.id;\n  const busyCount = machine.runningCount + machine.queuedCount;\n  const registrationErrors = machine.runtimes\n    .filter((runtime) => runtime.metadata?.runtime_profile_registration_error === true)\n    .map((runtime) => String(runtime.metadata?.runtime_profile_failure_reason ?? ""))\n    .filter(Boolean);\n  const registrationFailed = machine.onlineCount === 0 && registrationErrors.length > 0;\n  const body = (\n    <>\n      <span className="relative flex h-10 w-10 shrink-0 items-center justify-center rounded-lg border bg-background">\n'),
        ('\n      <span className="hidden w-36 shrink-0 items-center gap-1.5 text-caption md:flex">\n        <HealthIcon health={machine.health} />\n        <span>{healthLabel(machine.health)}</span>\n      </span>\n      <span className="hidden w-40 shrink-0 flex-col gap-1 lg:flex">\n        <span className="text-caption text-muted-foreground">\n', '\n      <span className="hidden w-36 shrink-0 items-center gap-1.5 text-caption md:flex">\n        <HealthIcon health={machine.health} />\n        <span title={registrationFailed ? registrationErrors.join("\\n") : undefined}>\n          {registrationFailed ? t(($) => $.page.registration_failed) : healthLabel(machine.health)}\n        </span>\n      </span>\n      <span className="hidden w-40 shrink-0 flex-col gap-1 lg:flex">\n        <span className="text-caption text-muted-foreground">\n'),
        ('  );\n\n  return (\n    <AppLink\n      href={paths.runtimeDetail(locator)}\n      className="group flex min-w-0 items-center gap-3 px-4 py-3.5 transition-colors hover:bg-accent/40 focus-visible:bg-accent/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"\n    >\n      {body}\n    </AppLink>\n  );\n}\n\n', '  );\n\n  return (\n    <div className="flex items-center">\n    <AppLink\n      href={paths.runtimeDetail(locator)}\n      className="group flex min-w-0 flex-1 items-center gap-3 px-4 py-3.5 transition-colors hover:bg-accent/40 focus-visible:bg-accent/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"\n    >\n      {body}\n    </AppLink>\n    <DeleteMachineButton machine={machine} />\n    </div>\n  );\n}\n\n'),
    ],
    'packages/views/settings/components/account-tab.tsx': [
        ('  SettingsTab,\n} from "./settings-layout";\nimport { useAutoSave } from "./use-auto-save";\n\n// Mirror server/internal/handler/auth.go:MaxProfileDescriptionLen. Counted in\n// JS String.length (UTF-16 code units) here while the server counts runes,\n', '  SettingsTab,\n} from "./settings-layout";\nimport { useAutoSave } from "./use-auto-save";\nimport { DuccCredentialCard } from "./ducc-credential-card";\n\n// Mirror server/internal/handler/auth.go:MaxProfileDescriptionLen. Counted in\n// JS String.length (UTF-16 code units) here while the server counts runes,\n'),
        ('          </SettingsRow>\n        </SettingsCard>\n      </SettingsSection>\n    </SettingsTab>\n  );\n}\n', '          </SettingsRow>\n        </SettingsCard>\n      </SettingsSection>\n      <DuccCredentialCard />\n    </SettingsTab>\n  );\n}\n'),
    ],
    'packages/views/settings/components/use-settings-search-index.ts': [
        ('    const visible = new Set(pages.map((page) => page.value));\n    const rows: SettingsSearchIndexEntry[] = [\n      // Personal\n      { tab: "profile", anchor: "avatar", title: t(($) => $.account.avatar_label) },\n      { tab: "profile", anchor: "name", title: t(($) => $.account.name_label) },\n      {\n', '    const visible = new Set(pages.map((page) => page.value));\n    const rows: SettingsSearchIndexEntry[] = [\n      // Personal\n      { tab: "profile", anchor: "ducc", title: t(($) => $.ducc.title), description: t(($) => $.ducc.scope) },\n      { tab: "profile", anchor: "avatar", title: t(($) => $.account.avatar_label) },\n      { tab: "profile", anchor: "name", title: t(($) => $.account.name_label) },\n      {\n'),
    ],
    'packages/views/skills/components/create-skill-dialog.test.tsx': [
        ('  const queryClient = new QueryClient({\n    defaultOptions: { queries: { retry: false } },\n  });\n  return {\n    onCreated,\n    onClose,\n    ...render(\n', '  const queryClient = new QueryClient({\n    defaultOptions: { queries: { retry: false } },\n  });\n  const result = {\n    onCreated,\n    onClose,\n    ...render(\n'),
        ('      </I18nProvider>,\n    ),\n  };\n}\n\ndescribe("CreateSkillDialog local import", () => {\n', '      </I18nProvider>,\n    ),\n  };\n  fireEvent.click(screen.getByRole("button", { name: /Workspace skill/i }));\n  return result;\n}\n\ndescribe("CreateSkillDialog local import", () => {\n'),
        ('    fireEvent.click(screen.getByRole("button", { name: /^Import$/i }));\n\n    await waitFor(() => {\n      expect(mockImportSkillArchive).toHaveBeenCalledWith(ARCHIVE_FILE, "fail");\n    });\n    await waitFor(() => {\n      expect(onCreated).toHaveBeenCalled();\n', '    fireEvent.click(screen.getByRole("button", { name: /^Import$/i }));\n\n    await waitFor(() => {\n      expect(mockImportSkillArchive).toHaveBeenCalledWith(ARCHIVE_FILE, "fail", "workspace");\n    });\n    await waitFor(() => {\n      expect(onCreated).toHaveBeenCalled();\n'),
    ],
    'packages/views/skills/components/create-skill-dialog.tsx': [
        ('import { useT } from "../../i18n";\nimport { isNameConflictError } from "../lib/utils";\n\ntype Method = "chooser" | "manual" | "local" | "url" | "runtime";\n\nfunction seedAfterCreate(\n  qc: ReturnType<typeof useQueryClient>,\n', 'import { useT } from "../../i18n";\nimport { isNameConflictError } from "../lib/utils";\n\ntype SkillScope = "personal" | "workspace" | "team";\ntype Method = "scope" | "chooser" | "manual" | "local" | "url" | "runtime";\n\nfunction seedAfterCreate(\n  qc: ReturnType<typeof useQueryClient>,\n'),
        ('}\n\n// ---------------------------------------------------------------------------\n// Chooser — initial method picker (4 cards)\n// ---------------------------------------------------------------------------\n\nfunction MethodChooser({ onChoose }: { onChoose: (m: Method) => void }) {\n  const { t } = useT("skills");\n  const methods: {\n    key: Exclude<Method, "chooser">;\n    icon: typeof Plus;\n  }[] = [\n    { key: "manual", icon: Plus },\n', '}\n\n// ---------------------------------------------------------------------------\n// Scope chooser — choose ownership before choosing an import method\n\nfunction ScopeChooser({ onChoose }: { onChoose: (scope: SkillScope) => void }) {\n  const { t } = useT("skills");\n  return (\n    <div className="grid gap-2 p-5">\n      {(["team", "workspace", "personal"] as const).map((scope) => (\n        <button key={scope} type="button" onClick={() => onChoose(scope)}\n          className="group flex items-start gap-3 rounded-lg border bg-card p-4 text-left transition-colors hover:border-primary/40 hover:bg-accent/40">\n          <div className="min-w-0 flex-1">\n            <div className="text-body font-medium">{t(($) => $.create.scope[scope].title)}</div>\n            <div className="mt-0.5 text-caption text-muted-foreground">{t(($) => $.create.scope[scope].desc)}</div>\n          </div>\n          <ChevronRight className="h-4 w-4 shrink-0 text-faint-foreground" />\n        </button>\n      ))}\n    </div>\n  );\n}\n\n// ---------------------------------------------------------------------------\n// Chooser — initial method picker (4 cards)\n// ---------------------------------------------------------------------------\n\nfunction MethodChooser({ onChoose }: { onChoose: (m: Method) => void }) {\n  const { t } = useT("skills");\n  const methods: {\n    key: Exclude<Method, "chooser" | "scope">;\n    icon: typeof Plus;\n  }[] = [\n    { key: "manual", icon: Plus },\n'),
        ('// ---------------------------------------------------------------------------\n\nfunction ManualForm({\n  onCreated,\n  onCancel,\n}: {\n  onCreated: (skill: Skill) => void;\n  onCancel: () => void;\n}) {\n', '// ---------------------------------------------------------------------------\n\nfunction ManualForm({\n  scope,\n  onCreated,\n  onCancel,\n}: {\n  scope: SkillScope;\n  onCreated: (skill: Skill) => void;\n  onCancel: () => void;\n}) {\n'),
        ('    setError("");\n    try {\n      const skill = await api.createSkill({\n        name: trimmed,\n        description: description.trim(),\n      });\n', '    setError("");\n    try {\n      const skill = await api.createSkill({\n        scope,\n        name: trimmed,\n        description: description.trim(),\n      });\n'),
        ('}\n\nfunction UrlForm({\n  onCreated,\n  onCancel,\n}: {\n  onCreated: (skill: Skill) => void;\n  onCancel: () => void;\n}) {\n', '}\n\nfunction UrlForm({\n  scope,\n  onCreated,\n  onCancel,\n}: {\n  scope: SkillScope;\n  onCreated: (skill: Skill) => void;\n  onCancel: () => void;\n}) {\n'),
        ('    setLoading(true);\n    setError("");\n    try {\n      const skill = await api.importSkill({ url: trimmed });\n      seedAfterCreate(qc, wsId, skill);\n      toast.success(t(($) => $.create.url.toast_imported));\n      onCreated(skill);\n', '    setLoading(true);\n    setError("");\n    try {\n      const skill = await api.importSkill({ url: trimmed, scope });\n      seedAfterCreate(qc, wsId, skill);\n      toast.success(t(($) => $.create.url.toast_imported));\n      onCreated(skill);\n'),
        ('// ---------------------------------------------------------------------------\n\nfunction LocalForm({\n  prepared,\n  preparing,\n  onCreated,\n', '// ---------------------------------------------------------------------------\n\nfunction LocalForm({\n  scope,\n  prepared,\n  preparing,\n  onCreated,\n'),
        ('  onChooseFolder,\n  onChooseArchive,\n}: {\n  prepared: PreparedSkillArchive | null;\n  preparing: boolean;\n  onCreated: (skill: Skill) => void;\n', '  onChooseFolder,\n  onChooseArchive,\n}: {\n  scope: SkillScope;\n  prepared: PreparedSkillArchive | null;\n  preparing: boolean;\n  onCreated: (skill: Skill) => void;\n'),
        ('    setLoading(true);\n    setError("");\n    try {\n      const skill = await api.importSkillArchive(prepared.file, "fail");\n      seedAfterCreate(qc, wsId, skill);\n      toast.success(t(($) => $.create.local.toast_imported));\n      onCreated(skill);\n', '    setLoading(true);\n    setError("");\n    try {\n      const skill = await api.importSkillArchive(prepared.file, "fail", scope);\n      seedAfterCreate(qc, wsId, skill);\n      toast.success(t(($) => $.create.local.toast_imported));\n      onCreated(skill);\n'),
        ('  onCreated?: (skill: Skill) => void;\n}) {\n  const { t } = useT("skills");\n  const [method, setMethod] = useState<Method>("chooser");\n  const [localPrepared, setLocalPrepared] = useState<PreparedSkillArchive | null>(\n    null,\n  );\n', '  onCreated?: (skill: Skill) => void;\n}) {\n  const { t } = useT("skills");\n  const [scope, setScope] = useState<SkillScope | null>(null);\n  const [method, setMethod] = useState<Method>("scope");\n  const [localPrepared, setLocalPrepared] = useState<PreparedSkillArchive | null>(\n    null,\n  );\n'),
        ('  const openArchivePicker = () => {\n    archiveInputRef.current?.click();\n  };\n\n  const handleChoose = (next: Method) => {\n    if (next === "local") {\n', '  const openArchivePicker = () => {\n    archiveInputRef.current?.click();\n  };\n\n  const handleScopeChoose = (next: SkillScope) => { setScope(next); setMethod("chooser"); };\n\n  const handleChoose = (next: Method) => {\n    if (next === "local") {\n'),
        ('        {/* Header */}\n        <div className="flex shrink-0 items-start justify-between gap-3 border-b px-5 pt-4 pb-3">\n          <div className="flex items-start gap-2 min-w-0">\n            {method !== "chooser" && (\n              <Tooltip>\n                <TooltipTrigger\n                  render={\n', '        {/* Header */}\n        <div className="flex shrink-0 items-start justify-between gap-3 border-b px-5 pt-4 pb-3">\n          <div className="flex items-start gap-2 min-w-0">\n            {method !== "scope" && (\n              <Tooltip>\n                <TooltipTrigger\n                  render={\n'),
        ('                      type="button"\n                      onClick={() => {\n                        resetLocal();\n                        setMethod("chooser");\n                      }}\n                      className="-ml-1 mt-px rounded-sm p-1 text-faint-foreground transition-colors hover:bg-accent/60 hover:text-muted-foreground"\n                      aria-label={t(($) => $.create.back_aria)}\n', '                      type="button"\n                      onClick={() => {\n                        resetLocal();\n                        setMethod(method === "chooser" ? "scope" : "chooser");\n                      }}\n                      className="-ml-1 mt-px rounded-sm p-1 text-faint-foreground transition-colors hover:bg-accent/60 hover:text-muted-foreground"\n                      aria-label={t(($) => $.create.back_aria)}\n'),
        ('            )}\n            <div className="min-w-0">\n              <DialogTitle className="truncate text-title-sm font-medium">\n                {t(($) => $.create.method[method].title)}\n              </DialogTitle>\n              <p className="mt-0.5 text-caption text-muted-foreground">\n                {t(($) => $.create.method[method].desc)}\n              </p>\n            </div>\n          </div>\n', '            )}\n            <div className="min-w-0">\n              <DialogTitle className="truncate text-title-sm font-medium">\n                {method === "scope" ? t(($) => $.create.scope.title) : t(($) => $.create.method[method].title)}\n              </DialogTitle>\n              <p className="mt-0.5 text-caption text-muted-foreground">\n                {method === "scope" ? t(($) => $.create.scope.desc) : t(($) => $.create.method[method].desc)}\n              </p>\n            </div>\n          </div>\n'),
        ('        />\n\n        {/* Method body — each form owns its scroll middle + footer */}\n        {method === "chooser" && <MethodChooser onChoose={handleChoose} />}\n        {method === "manual" && (\n          <ManualForm\n            onCreated={handleCreated}\n            onCancel={() => setMethod("chooser")}\n          />\n        )}\n        {method === "local" && (\n          <LocalForm\n            key={localEpoch}\n            prepared={localPrepared}\n            preparing={localPreparing}\n', '        />\n\n        {/* Method body — each form owns its scroll middle + footer */}\n        {method === "scope" && <ScopeChooser onChoose={handleScopeChoose} />}\n        {method === "chooser" && <MethodChooser onChoose={handleChoose} />}\n        {method === "manual" && (\n          <ManualForm\n            scope={scope ?? "workspace"}\n            onCreated={handleCreated}\n            onCancel={() => setMethod("chooser")}\n          />\n        )}\n        {method === "local" && (\n          <LocalForm\n            scope={scope ?? "workspace"}\n            key={localEpoch}\n            prepared={localPrepared}\n            preparing={localPreparing}\n'),
        ('        )}\n        {method === "url" && (\n          <UrlForm\n            onCreated={handleCreated}\n            onCancel={() => setMethod("chooser")}\n          />\n        )}\n        {method === "runtime" && (\n          <RuntimeLocalSkillImportPanel\n            onImported={handleCreated}\n            onBulkDone={onClose}\n          />\n', '        )}\n        {method === "url" && (\n          <UrlForm\n            scope={scope ?? "workspace"}\n            onCreated={handleCreated}\n            onCancel={() => setMethod("chooser")}\n          />\n        )}\n        {method === "runtime" && (\n          <RuntimeLocalSkillImportPanel\n            scope={scope ?? "workspace"}\n            onImported={handleCreated}\n            onBulkDone={onClose}\n          />\n'),
    ],
    'packages/views/skills/components/runtime-local-skill-import-panel.test.tsx': [
        ('        expect(mockResolveRuntimeLocalSkillImport).toHaveBeenCalledWith(\n          "runtime-1",\n          {\n            skill_key: "review-helper",\n            name: "Review Helper",\n            description: "Review pull requests",\n            supports_conflict: true,\n          },\n        );\n      },\n      { timeout: 5000 },\n    );\n  });\n\n  it("surfaces the runtime alias and provider in the picker, not the raw daemon name (MUL-5248)", async () => {\n', '        expect(mockResolveRuntimeLocalSkillImport).toHaveBeenCalledWith(\n          "runtime-1",\n          {\n            scope: "workspace",\n            skill_key: "review-helper",\n            name: "Review Helper",\n            description: "Review pull requests",\n            supports_conflict: true,\n          },\n        );\n      },\n      { timeout: 5000 },\n    );\n  });\n\n  it("surfaces the runtime alias and provider in the picker, not the raw daemon name (MUL-5248)", async () => {\n'),
        ('    });\n    await waitFor(() => expect(importButton).not.toBeDisabled(), {\n      timeout: 5000,\n    });\n    fireEvent.click(importButton);\n\n    await waitFor(\n      () => {\n        expect(mockResolveRuntimeLocalSkillImport).toHaveBeenCalledTimes(1);\n        expect(mockResolveRuntimeLocalSkillImport).toHaveBeenCalledWith(\n          "runtime-1",\n          {\n            skill_key: "code-gen",\n            name: "Code Gen",\n            description: "Generate code from specs",\n            supports_conflict: true,\n          },\n        );\n      },\n      { timeout: 5000 },\n    );\n  });\n\n  it("highlights the matched substring in search results", async () => {\n', '    });\n    await waitFor(() => expect(importButton).not.toBeDisabled(), {\n      timeout: 5000,\n    });\n    fireEvent.click(importButton);\n\n    await waitFor(\n      () => {\n        expect(mockResolveRuntimeLocalSkillImport).toHaveBeenCalledTimes(1);\n        expect(mockResolveRuntimeLocalSkillImport).toHaveBeenCalledWith(\n          "runtime-1",\n          {\n            scope: "workspace",\n            skill_key: "code-gen",\n            name: "Code Gen",\n            description: "Generate code from specs",\n            supports_conflict: true,\n          },\n        );\n      },\n      { timeout: 5000 },\n    );\n  });\n\n  it("highlights the matched substring in search results", async () => {\n'),
        ('      name: /Apply decisions/i,\n    });\n    await waitFor(() => expect(applyButton).not.toBeDisabled(), {\n      timeout: 5000,\n    });\n    fireEvent.click(applyButton);\n\n    await waitFor(\n      () => {\n        expect(mockResolveRuntimeLocalSkillImport).toHaveBeenLastCalledWith(\n          "runtime-1",\n          {\n            skill_key: "review-helper",\n            name: "Review Helper",\n            description: "Review pull requests",\n            supports_conflict: true,\n            action: "overwrite",\n            target_skill_id: "existing-skill-1",\n          },\n        );\n      },\n      { timeout: 5000 },\n    );\n\n', '      name: /Apply decisions/i,\n    });\n    await waitFor(() => expect(applyButton).not.toBeDisabled(), {\n      timeout: 5000,\n    });\n    fireEvent.click(applyButton);\n\n    await waitFor(\n      () => {\n        expect(mockResolveRuntimeLocalSkillImport).toHaveBeenLastCalledWith(\n          "runtime-1",\n          {\n            scope: "workspace",\n            skill_key: "review-helper",\n            name: "Review Helper",\n            description: "Review pull requests",\n            supports_conflict: true,\n            action: "overwrite",\n            target_skill_id: "existing-skill-1",\n          },\n        );\n      },\n      { timeout: 5000 },\n    );\n\n'),
        ('\n    expect(\n      await screen.findByText(/A skill with this name already exists/i),\n    ).toBeInTheDocument();\n\n    fireEvent.click(screen.getByRole("button", { name: /^Overwrite$/i }));\n\n    await waitFor(\n      () => {\n        expect(mockResolveRuntimeLocalSkillImport).toHaveBeenLastCalledWith(\n          "runtime-1",\n          {\n            skill_key: "review-helper",\n            name: "Review Helper",\n            description: "Review pull requests",\n', '\n    expect(\n      await screen.findByText(/A skill with this name already exists/i),\n    ).toBeInTheDocument();\n\n    fireEvent.click(screen.getByRole("button", { name: /^Overwrite$/i }));\n\n    await waitFor(\n      () => {\n        expect(mockResolveRuntimeLocalSkillImport).toHaveBeenLastCalledWith(\n          "runtime-1",\n          {\n            scope: "workspace",\n            skill_key: "review-helper",\n            name: "Review Helper",\n            description: "Review pull requests",\n'),
    ],
    'packages/views/skills/components/runtime-local-skill-import-panel.tsx': [
        ('// ---------------------------------------------------------------------------\n\nexport function RuntimeLocalSkillImportPanel({\n  onImported,\n  onBulkDone,\n}: {\n  onImported?: (skill: Skill) => void;\n  onBulkDone?: () => void;\n}) {\n', '// ---------------------------------------------------------------------------\n\nexport function RuntimeLocalSkillImportPanel({\n  scope,\n  onImported,\n  onBulkDone,\n}: {\n  scope?: "personal" | "workspace" | "team";\n  onImported?: (skill: Skill) => void;\n  onBulkDone?: () => void;\n}) {\n'),
        ('          : skill.description || undefined;\n      try {\n        const result = await resolveRuntimeLocalSkillImport(selectedRuntimeId, {\n          skill_key: skill.key,\n          name: importName,\n          description: importDescription,\n', '          : skill.description || undefined;\n      try {\n        const result = await resolveRuntimeLocalSkillImport(selectedRuntimeId, {\n          scope: scope ?? "workspace",\n          skill_key: skill.key,\n          name: importName,\n          description: importDescription,\n'),
        ('\n      try {\n        const result = await resolveRuntimeLocalSkillImport(selectedRuntimeId, {\n          skill_key: r.key,\n          name:\n            resolution.action === "rename"\n', '\n      try {\n        const result = await resolveRuntimeLocalSkillImport(selectedRuntimeId, {\n          scope: scope ?? "workspace",\n          skill_key: r.key,\n          name:\n            resolution.action === "rename"\n'),
    ],
    'packages/views/skills/components/skill-detail-page.tsx': [
        ('  AddToAgentDialog,\n  type SkillActionsContext,\n} from "./skill-list-actions";\nimport { RefreshSkillDialog } from "./refresh-skill-dialog";\nimport { useT } from "../../i18n";\nimport { ResourceLabelPicker } from "../../labels/resource-label-picker";\n\nconst SKILL_MD = "SKILL.md";\n\n', '  AddToAgentDialog,\n  type SkillActionsContext,\n} from "./skill-list-actions";\nimport { SkillScopeDialog } from "./skill-scope-dialog";\nimport { RefreshSkillDialog } from "./refresh-skill-dialog";\nimport { useT } from "../../i18n";\nimport { ResourceLabelPicker } from "../../labels/resource-label-picker";\nimport { SkillDownload } from "./skill-download";\n\nconst SKILL_MD = "SKILL.md";\n\n'),
        ('              )}\n            </span>\n          )}\n          <span className="inline-flex items-center gap-1.5">\n            <FileText className="h-3.5 w-3.5" aria-hidden="true" />\n            {t(($) => $.detail.header.files, { count: totalFileCount(skill) })}\n', '              )}\n            </span>\n          )}\n          <span className="inline-flex items-center gap-1.5">\n            <span className="font-medium">{t(($) => $.detail.category_label)}:</span>\n            {skill.scope === "team"\n              ? t(($) => $.detail.category_team)\n              : skill.scope === "personal"\n                ? t(($) => $.detail.category_personal)\n                : t(($) => $.detail.category_project)}\n          </span>\n          <span className="inline-flex items-center gap-1.5">\n            <FileText className="h-3.5 w-3.5" aria-hidden="true" />\n            {t(($) => $.detail.header.files, { count: totalFileCount(skill) })}\n'),
        ('  const [confirmDelete, setConfirmDelete] = useState(false);\n  const [confirmRefresh, setConfirmRefresh] = useState(false);\n  const [showAddToAgents, setShowAddToAgents] = useState(false);\n  const [addingFile, setAddingFile] = useState(false);\n  const [conflictPending, setConflictPending] = useState(false);\n\n', '  const [confirmDelete, setConfirmDelete] = useState(false);\n  const [confirmRefresh, setConfirmRefresh] = useState(false);\n  const [showAddToAgents, setShowAddToAgents] = useState(false);\n  const [changeScopeOpen, setChangeScopeOpen] = useState(false);\n  const [addingFile, setAddingFile] = useState(false);\n  const [conflictPending, setConflictPending] = useState(false);\n\n'),
        ('                {t(($) => $.detail.read_only)}\n              </span>\n            )}\n            {canEdit && origin && isRefreshableOrigin(origin) && (\n              <Tooltip>\n                <TooltipTrigger\n', '                {t(($) => $.detail.read_only)}\n              </span>\n            )}\n            {skill.created_by === currentUserId && (\n              <Button\n                variant="outline"\n                size="xs"\n                className="gap-1"\n                onClick={() => setChangeScopeOpen(true)}\n              >\n                {t(($) => $.actions.change_level)}\n              </Button>\n            )}\n            {canEdit && origin && isRefreshableOrigin(origin) && (\n              <Tooltip>\n                <TooltipTrigger\n'),
        ('              <UserPlus className="h-3 w-3" />\n              {t(($) => $.actions.add_to_agent)}\n            </Button>\n            {canEdit && (\n              <Tooltip>\n                <TooltipTrigger\n', '              <UserPlus className="h-3 w-3" />\n              {t(($) => $.actions.add_to_agent)}\n            </Button>\n            <SkillDownload skillId={skill.id} wsId={wsId} />\n            {canEdit && (\n              <Tooltip>\n                <TooltipTrigger\n'),
        ('        </DialogContent>\n      </Dialog>\n\n      <AddToAgentDialog\n        skills={[skill]}\n        ctx={actionsCtx}\n', '        </DialogContent>\n      </Dialog>\n\n      {skill.created_by === currentUserId && changeScopeOpen && (\n        <SkillScopeDialog\n          skill={skill}\n          wsId={wsId}\n          open={changeScopeOpen}\n          onOpenChange={setChangeScopeOpen}\n        />\n      )}\n\n      <AddToAgentDialog\n        skills={[skill]}\n        ctx={actionsCtx}\n'),
    ],
    'packages/views/skills/components/skill-list-actions.tsx': [
        ('  Check,\n  ChevronRight,\n  ExternalLink,\n  Loader2,\n  MoreHorizontal,\n  Plus,\n', '  Check,\n  ChevronRight,\n  ExternalLink,\n  Layers3,\n  Loader2,\n  MoreHorizontal,\n  Plus,\n'),
        ('import { useIntentNavigate } from "../../navigation";\nimport { isRefreshableOrigin, readOrigin } from "../lib/origin";\nimport { RefreshSkillDialog } from "./refresh-skill-dialog";\nimport type { SkillRow } from "./skill-list-filter";\n\n// Shared context the row kebab and the batch toolbar both need. Assembled\n', 'import { useIntentNavigate } from "../../navigation";\nimport { isRefreshableOrigin, readOrigin } from "../lib/origin";\nimport { RefreshSkillDialog } from "./refresh-skill-dialog";\nimport { SkillScopeDialog } from "./skill-scope-dialog";\nimport { SkillDownload } from "./skill-download";\nimport type { SkillRow } from "./skill-list-filter";\n\n// Shared context the row kebab and the batch toolbar both need. Assembled\n'),
        ('  const intentNavigate = useIntentNavigate();\n  const [addOpen, setAddOpen] = useState(false);\n  const [refreshOpen, setRefreshOpen] = useState(false);\n  const [deleteOpen, setDeleteOpen] = useState(false);\n\n  const origin = readOrigin(row.skill);\n', '  const intentNavigate = useIntentNavigate();\n  const [addOpen, setAddOpen] = useState(false);\n  const [refreshOpen, setRefreshOpen] = useState(false);\n  const [scopeOpen, setScopeOpen] = useState(false);\n  const [deleteOpen, setDeleteOpen] = useState(false);\n\n  const origin = readOrigin(row.skill);\n'),
        ('          }\n        />\n        <DropdownMenuContent align="end" className="w-52">\n          <DropdownMenuItem\n            onClick={() =>\n              intentNavigate(\n', '          }\n        />\n        <DropdownMenuContent align="end" className="w-52">\n          <SkillDownload skillId={row.skill.id} wsId={ctx.wsId} menu />\n          <DropdownMenuItem\n            onClick={() =>\n              intentNavigate(\n'),
        ('            <DropdownMenuItem onClick={() => setRefreshOpen(true)}>\n              <RotateCw className="size-3.5" />\n              {t(($) => $.actions.refresh)}\n            </DropdownMenuItem>\n          )}\n          {row.canEdit && (\n', '            <DropdownMenuItem onClick={() => setRefreshOpen(true)}>\n              <RotateCw className="size-3.5" />\n              {t(($) => $.actions.refresh)}\n            </DropdownMenuItem>\n          )}\n          {row.canChangeScope && (\n            <DropdownMenuItem onClick={() => setScopeOpen(true)}>\n              <Layers3 className="size-3.5" />\n              {t(($) => $.actions.change_level)}\n            </DropdownMenuItem>\n          )}\n          {row.canEdit && (\n'),
        ('          wsId={ctx.wsId}\n          open={refreshOpen}\n          onOpenChange={setRefreshOpen}\n        />\n      )}\n      <DeleteSkillsDialog\n', '          wsId={ctx.wsId}\n          open={refreshOpen}\n          onOpenChange={setRefreshOpen}\n        />\n      )}\n      {row.canChangeScope && scopeOpen && (\n        <SkillScopeDialog\n          skill={row.skill}\n          wsId={ctx.wsId}\n          open={scopeOpen}\n          onOpenChange={setScopeOpen}\n        />\n      )}\n      <DeleteSkillsDialog\n'),
    ],
    'packages/views/skills/components/skill-list-filter.ts': [
        ('  runtime: AgentRuntime | null;\n  originType: OriginInfo["type"];\n  canEdit: boolean;\n}\n\n/**\n', '  runtime: AgentRuntime | null;\n  originType: OriginInfo["type"];\n  canEdit: boolean;\n  canChangeScope?: boolean;\n}\n\n/**\n'),
        ('  row: SkillRow,\n  filters: SkillListFilters,\n  query: string,\n): boolean {\n  const q = query.trim().toLowerCase();\n  if (q && !row.skill.name.toLowerCase().includes(q)) return false;\n  if (filters.usage.length > 0) {\n', '  row: SkillRow,\n  filters: SkillListFilters,\n  query: string,\n  currentWorkspaceId?: string,\n): boolean {\n  if (\n    currentWorkspaceId &&\n    (row.skill.scope ?? "workspace") === "workspace" &&\n    row.skill.workspace_id !== currentWorkspaceId &&\n    !filters.projects.includes(row.skill.workspace_id)\n  ) return false;\n  const q = query.trim().toLowerCase();\n  if (q && !row.skill.name.toLowerCase().includes(q)) return false;\n  if (filters.usage.length > 0) {\n'),
    ],
    'packages/views/skills/components/skill-list-toolbar.tsx': [
        ('export type OriginType = SkillOriginType;\n\nconst COLUMN_KEYS: SkillColumnKey[] = [\n  "usedBy",\n  "source",\n  "creator",\n  "updated",\n  "created",\n];\n\nconst SORT_FIELDS: SkillSortField[] = ["name", "usedBy", "updated", "created"];\n\nexport function countActiveFilterDimensions(\n  filters: SkillListFilters,\n', 'export type OriginType = SkillOriginType;\n\nconst COLUMN_KEYS: SkillColumnKey[] = [\n  "scope",\n  "usedBy",\n  "usageCount",\n  "source",\n  "creator",\n  "updated",\n  "created",\n];\n\nconst SORT_FIELDS: SkillSortField[] = ["name", "usedBy", "usageCount", "updated", "created"];\n\nexport function countActiveFilterDimensions(\n  filters: SkillListFilters,\n'),
        ('  if (filters.origins.length > 0) count++;\n  if (filters.agents.length > 0) count++;\n  if (filters.creators.length > 0) count++;\n  if (filters.labels.length > 0) count++;\n  return count;\n}\n', '  if (filters.origins.length > 0) count++;\n  if (filters.agents.length > 0) count++;\n  if (filters.creators.length > 0) count++;\n  if (filters.projects.length > 0) count++;\n  if (filters.labels.length > 0) count++;\n  return count;\n}\n'),
        ('  onToggleColumn,\n  allRows,\n  visibleCount,\n}: {\n  search: string;\n  onSearchChange: (v: string) => void;\n', '  onToggleColumn,\n  allRows,\n  visibleCount,\n  currentWorkspaceId,\n}: {\n  search: string;\n  onSearchChange: (v: string) => void;\n'),
        ('  allRows: SkillRow[];\n  /** Rows surviving search + filters — shown as "n / total" when narrowed. */\n  visibleCount: number;\n}) {\n  const { t } = useT("skills");\n  const [labelSearch, setLabelSearch] = useState("");\n', '  allRows: SkillRow[];\n  /** Rows surviving search + filters — shown as "n / total" when narrowed. */\n  visibleCount: number;\n  currentWorkspaceId: string;\n}) {\n  const { t } = useT("skills");\n  const [labelSearch, setLabelSearch] = useState("");\n'),
        ('    string,\n    { member: MemberWithUser; count: number }\n  >();\n  const labelCounts = new Map<string, number>();\n  for (const row of allRows) {\n    originCounts.set(row.originType, (originCounts.get(row.originType) ?? 0) + 1);\n', '    string,\n    { member: MemberWithUser; count: number }\n  >();\n  const projectOptions = new Map<string, { name: string; count: number }>();\n  const labelCounts = new Map<string, number>();\n  for (const row of allRows) {\n    originCounts.set(row.originType, (originCounts.get(row.originType) ?? 0) + 1);\n'),
        ('      const entry = creatorOptions.get(row.creator.user_id);\n      if (entry) entry.count += 1;\n      else creatorOptions.set(row.creator.user_id, { member: row.creator, count: 1 });\n    }\n    for (const label of row.skill.labels ?? []) {\n      labelCounts.set(label.id, (labelCounts.get(label.id) ?? 0) + 1);\n', '      const entry = creatorOptions.get(row.creator.user_id);\n      if (entry) entry.count += 1;\n      else creatorOptions.set(row.creator.user_id, { member: row.creator, count: 1 });\n    }\n    if (\n      row.skill.scope === "workspace" &&\n      row.skill.workspace_id &&\n      row.skill.workspace_id !== currentWorkspaceId\n    ) {\n      const id = row.skill.workspace_id;\n      const entry = projectOptions.get(id);\n      if (entry) entry.count += 1;\n      else {\n        projectOptions.set(id, {\n          name: row.skill.workspace_name || id,\n          count: 1,\n        });\n      }\n    }\n    for (const label of row.skill.labels ?? []) {\n      labelCounts.set(label.id, (labelCounts.get(label.id) ?? 0) + 1);\n'),
        ('  };\n\n  const COLUMN_LABELS: Record<SkillColumnKey, string> = {\n    usedBy: t(($) => $.table.used_by),\n    source: t(($) => $.table.source),\n    creator: t(($) => $.table.created_by),\n    updated: t(($) => $.table.updated),\n', '  };\n\n  const COLUMN_LABELS: Record<SkillColumnKey, string> = {\n    scope: t(($) => $.table.level),\n    usedBy: t(($) => $.table.used_by),\n    usageCount: t(($) => $.table.usage_count),\n    source: t(($) => $.table.source),\n    creator: t(($) => $.table.created_by),\n    updated: t(($) => $.table.updated),\n'),
        ('  const SORT_LABELS: Record<SkillSortField, string> = {\n    name: t(($) => $.table.name),\n    usedBy: t(($) => $.table.used_by),\n    updated: t(($) => $.table.updated),\n    created: t(($) => $.table.created),\n  };\n', '  const SORT_LABELS: Record<SkillSortField, string> = {\n    name: t(($) => $.table.name),\n    usedBy: t(($) => $.table.used_by),\n    usageCount: t(($) => $.table.usage_count),\n    updated: t(($) => $.table.updated),\n    created: t(($) => $.table.created),\n  };\n'),
        ('                ))}\n              </DropdownMenuSubContent>\n            </DropdownMenuSub>\n\n            {/* Source */}\n            <DropdownMenuSub>\n', '                ))}\n              </DropdownMenuSubContent>\n            </DropdownMenuSub>\n\n            {/* Source */}\n            {projectOptions.size > 0 && (\n              <DropdownMenuSub>\n                <DropdownMenuSubTrigger>\n                  <span className="flex-1">{t(($) => $.toolbar.section_projects)}</span>\n                  {filters.projects.length > 0 && (\n                    <span className="text-caption font-medium text-primary">\n                      {filters.projects.length}\n                    </span>\n                  )}\n                </DropdownMenuSubTrigger>\n                <DropdownMenuSubContent className="max-h-72 w-auto min-w-52 overflow-y-auto">\n                  {[...projectOptions.entries()].map(([id, project]) => (\n                    <DropdownMenuCheckboxItem\n                      key={id}\n                      checked={filters.projects.includes(id)}\n                      onCheckedChange={() => onToggleFilter("projects", id)}\n                      className={FILTER_ITEM_CLASS}\n                    >\n                      <HoverCheck checked={filters.projects.includes(id)} />\n                      <span className="min-w-0 truncate">{project.name}</span>\n                      {countBadge(project.count)}\n                    </DropdownMenuCheckboxItem>\n                  ))}\n                </DropdownMenuSubContent>\n              </DropdownMenuSub>\n            )}\n\n            {/* Source */}\n            <DropdownMenuSub>\n'),
    ],
    'packages/views/skills/components/skills-page-filter-predicate.test.ts': [
        ('  agents: [],\n  creators: [],\n  labels: [],\n};\n\nfunction withLabels(filters: SkillListFilters, ...ids: string[]): SkillListFilters {\n', '  agents: [],\n  creators: [],\n  labels: [],\n  projects: [],\n};\n\nfunction withLabels(filters: SkillListFilters, ...ids: string[]): SkillListFilters {\n'),
    ],
    'packages/views/skills/components/skills-page.tsx': [
        ('  TooltipTrigger,\n} from "@multica/ui/components/ui/tooltip";\nimport { ActorAvatar } from "@multica/ui/components/common/actor-avatar";\nimport {\n  rowLinkInteractiveProps,\n  useNavigation,\n', '  TooltipTrigger,\n} from "@multica/ui/components/ui/tooltip";\nimport { ActorAvatar } from "@multica/ui/components/common/actor-avatar";\nimport { cn } from "@multica/ui/lib/utils";\nimport {\n  rowLinkInteractiveProps,\n  useNavigation,\n'),
        ('import { canEditSkill } from "../hooks/use-can-edit-skill";\nimport { originSourceUrl, readOrigin } from "../lib/origin";\nimport { rowMatchesFilters, type SkillRow } from "./skill-list-filter";\nimport { CreateSkillDialog } from "./create-skill-dialog";\nimport {\n  useSkillsViewStore,\n', 'import { canEditSkill } from "../hooks/use-can-edit-skill";\nimport { originSourceUrl, readOrigin } from "../lib/origin";\nimport { rowMatchesFilters, type SkillRow } from "./skill-list-filter";\nimport { compareSkillScopes, skillListItems } from "./skill-list-scope";\nimport { CreateSkillDialog } from "./create-skill-dialog";\nimport {\n  useSkillsViewStore,\n'),
        ('// - Container < @2xl (phones, slim split panes): static core set\n//   (name + usedBy), no horizontal scroll, column toggles don\'t apply.\nconst GRID_COLS =\n  "grid-cols-[0.75rem_1rem_minmax(120px,1fr)_var(--lgc-usedby)_1.75rem_0.75rem] " +\n  "@2xl:grid-cols-[0.75rem_1rem_minmax(200px,1fr)_var(--lgc-usedby)_var(--lgc-source)_var(--lgc-creator)_var(--lgc-updated)_var(--lgc-created)_1.75rem_0.75rem]";\n\n// h-12 rows. The virtualizer\'s fixed-size contract: every row renders at\n// exactly this height, which is what lets it skip per-row measurement.\n', '// - Container < @2xl (phones, slim split panes): static core set\n//   (name + usedBy), no horizontal scroll, column toggles don\'t apply.\nconst GRID_COLS =\n  "grid-cols-[0.75rem_1rem_minmax(120px,1fr)_var(--lgc-scope)_var(--lgc-usedby)_1.75rem_0.75rem] " +\n  "@2xl:grid-cols-[0.75rem_1rem_minmax(200px,1fr)_var(--lgc-scope)_var(--lgc-usedby)_var(--lgc-usage)_var(--lgc-source)_var(--lgc-creator)_var(--lgc-updated)_var(--lgc-created)_1.75rem_0.75rem]";\n\n// h-12 rows. The virtualizer\'s fixed-size contract: every row renders at\n// exactly this height, which is what lets it skip per-row measurement.\n'),
        ("// Single source for hideable column widths: track vars and the grid's\n// min-width derive from the same numbers.\nconst COLUMN_WIDTHS: Record<SkillColumnKey, number> = {\n  usedBy: 144,\n  source: 152,\n  creator: 144,\n  updated: 104,\n", "// Single source for hideable column widths: track vars and the grid's\n// min-width derive from the same numbers.\nconst COLUMN_WIDTHS: Record<SkillColumnKey, number> = {\n  scope: 144,\n  usedBy: 144,\n  usageCount: 104,\n  source: 152,\n  creator: 144,\n  updated: 104,\n"),
        ('      0,\n    );\n  return {\n    "--lgc-usedby": width("usedBy"),\n    "--lgc-source": width("source"),\n    "--lgc-creator": width("creator"),\n    "--lgc-updated": width("updated"),\n', '      0,\n    );\n  return {\n    "--lgc-scope": width("scope"),\n    "--lgc-usedby": width("usedBy"),\n    "--lgc-usage": width("usageCount"),\n    "--lgc-source": width("source"),\n    "--lgc-creator": width("creator"),\n    "--lgc-updated": width("updated"),\n'),
        ('  );\n}\n\nfunction CreatorCell({ creator }: { creator: MemberWithUser | null }) {\n  return (\n    <ListGridCell className="hidden gap-1.5 @2xl:flex">\n', '  );\n}\n\nfunction ScopeCell({\n  skill,\n  currentWorkspaceId,\n}: {\n  skill: SkillSummary;\n  currentWorkspaceId: string;\n}) {\n  const { t } = useT("skills");\n  const scope = skill.scope ?? "workspace";\n  const projectName =\n    skill.workspace_name ||\n    (skill.workspace_id === currentWorkspaceId\n      ? t(($) => $.table.level_current_project)\n      : t(($) => $.table.level_project));\n  const label =\n    scope === "team"\n      ? t(($) => $.table.level_team)\n      : scope === "personal"\n        ? t(($) => $.table.level_personal)\n        : `${t(($) => $.table.level_project_prefix)}${projectName}`;\n  const className =\n    scope === "team"\n      ? "bg-violet-500/10 text-violet-700 dark:text-violet-300"\n      : scope === "personal"\n        ? "bg-amber-500/10 text-amber-700 dark:text-amber-300"\n        : "bg-blue-500/10 text-blue-700 dark:text-blue-300";\n  return (\n    <ListGridCell className="min-w-0 text-caption @2xl:flex">\n      <span className={cn("max-w-full truncate rounded px-1.5 py-0.5", className)}>\n        {label}\n      </span>\n    </ListGridCell>\n  );\n}\n\nfunction CreatorCell({ creator }: { creator: MemberWithUser | null }) {\n  return (\n    <ListGridCell className="hidden gap-1.5 @2xl:flex">\n'),
        ('      <ListGridHeaderCell sorted={sorted("name")} onSort={() => onSort("name")}>\n        {t(($) => $.table.name)}\n      </ListGridHeaderCell>\n      {isColVisible("usedBy") ? (\n        <ListGridHeaderCell\n          sorted={sorted("usedBy")}\n', '      <ListGridHeaderCell sorted={sorted("name")} onSort={() => onSort("name")}>\n        {t(($) => $.table.name)}\n      </ListGridHeaderCell>\n      {isColVisible("scope") ? (\n        <ListGridHeaderCell>\n          {t(($) => $.table.level)}\n        </ListGridHeaderCell>\n      ) : (\n        <ListGridHeaderCell className="px-0" />\n      )}\n      {isColVisible("usedBy") ? (\n        <ListGridHeaderCell\n          sorted={sorted("usedBy")}\n'),
        ('        </ListGridHeaderCell>\n      ) : (\n        <ListGridHeaderCell className="px-0" />\n      )}\n      {isColVisible("source") ? (\n        <ListGridHeaderCell className="hidden @2xl:flex">\n', '        </ListGridHeaderCell>\n      ) : (\n        <ListGridHeaderCell className="px-0" />\n      )}\n      {isColVisible("usageCount") ? (\n        <ListGridHeaderCell\n          className="hidden @2xl:flex"\n          sorted={sorted("usageCount")}\n          onSort={() => onSort("usageCount")}\n        >\n          {t(($) => $.table.usage_count)}\n        </ListGridHeaderCell>\n      ) : (\n        <ListGridHeaderCell className="hidden px-0 @2xl:flex" />\n      )}\n      {isColVisible("source") ? (\n        <ListGridHeaderCell className="hidden @2xl:flex">\n'),
        ('          <Skeleton className="h-3 w-12" />\n        </ListGridHeaderCell>\n        <ListGridHeaderCell>\n          <Skeleton className="h-3 w-14" />\n        </ListGridHeaderCell>\n        {/* Source and created are hidden by default — keep their tracks\n            mapped with empty placeholders so the skeleton matches the\n', '          <Skeleton className="h-3 w-12" />\n        </ListGridHeaderCell>\n        <ListGridHeaderCell>\n          <Skeleton className="h-3 w-16" />\n        </ListGridHeaderCell>\n        <ListGridHeaderCell>\n          <Skeleton className="h-3 w-14" />\n        </ListGridHeaderCell>\n        <ListGridHeaderCell className="hidden @2xl:flex">\n          <Skeleton className="h-3 w-8" />\n        </ListGridHeaderCell>\n        {/* Source and created are hidden by default — keep their tracks\n            mapped with empty placeholders so the skeleton matches the\n'),
        ('          <ListGridCell>\n            <Skeleton className="h-5 w-14" />\n          </ListGridCell>\n          <ListGridCell className="hidden px-0 @2xl:flex" />\n          <ListGridCell className="hidden gap-1.5 @2xl:flex">\n            <Skeleton className="size-5 rounded-full" />\n            <Skeleton className="h-3 w-12" />\n', '          <ListGridCell>\n            <Skeleton className="h-5 w-14" />\n          </ListGridCell>\n          <ListGridCell>\n            <Skeleton className="h-3 w-16" />\n          </ListGridCell>\n          <ListGridCell className="hidden px-0 @2xl:flex" />\n          <ListGridCell className="hidden @2xl:flex" />\n          <ListGridCell className="hidden gap-1.5 @2xl:flex">\n            <Skeleton className="size-5 rounded-full" />\n            <Skeleton className="h-3 w-12" />\n'),
        ('        runtime,\n        originType: origin.type,\n        canEdit: canEditSkill(skill, { userId: currentUserId, role: myRole }),\n      };\n    });\n  }, [skills, assignments, membersById, runtimesById, currentUserId, myRole]);\n', '        runtime,\n        originType: origin.type,\n        canEdit: canEditSkill(skill, { userId: currentUserId, role: myRole }),\n        canChangeScope: Boolean(skill.created_by && skill.created_by === currentUserId),\n      };\n    });\n  }, [skills, assignments, membersById, runtimesById, currentUserId, myRole]);\n'),
        ('  // Visible rows: name search + filters, then sort.\n  const rows = useMemo<SkillRow[]>(() => {\n    const filtered = allRows.filter((row) =>\n      rowMatchesFilters(row, filters, search),\n    );\n\n    const dir = sortDirection === "asc" ? 1 : -1;\n    filtered.sort((a, b) => {\n      if (sortField === "name") {\n        return a.skill.name.localeCompare(b.skill.name) * dir;\n      }\n      if (sortField === "usedBy") {\n        return (\n          (a.agents.length - b.agents.length) * dir ||\n          a.skill.name.localeCompare(b.skill.name)\n        );\n      }\n', '  // Visible rows: name search + filters, then sort.\n  const rows = useMemo<SkillRow[]>(() => {\n    const filtered = allRows.filter((row) =>\n      rowMatchesFilters(row, filters, search, wsId),\n    );\n\n    const dir = sortDirection === "asc" ? 1 : -1;\n    filtered.sort((a, b) => {\n      const scopeOrder = compareSkillScopes(a.skill, b.skill);\n      if (scopeOrder !== 0) return scopeOrder;\n      if (sortField === "name") {\n        return a.skill.name.localeCompare(b.skill.name) * dir;\n      }\n      if (sortField === "usedBy") {\n        return (\n          (a.agents.length - b.agents.length) * dir ||\n          a.skill.name.localeCompare(b.skill.name)\n        );\n      }\n      if (sortField === "usageCount") {\n        return (\n          ((a.skill.usage_count ?? 0) - (b.skill.usage_count ?? 0)) * dir ||\n          a.skill.name.localeCompare(b.skill.name)\n        );\n      }\n'),
        ('      );\n    });\n    return filtered;\n  }, [allRows, search, filters, sortField, sortDirection]);\n\n  // Row virtualization — Linear-style: the virtualizer only does the math\n  // (visible index range + offsets); the DOM stays ours. Offsets become\n', '      );\n    });\n    return filtered;\n  }, [allRows, search, filters, sortField, sortDirection, wsId]);\n\n  // Row virtualization — Linear-style: the virtualizer only does the math\n  // (visible index range + offsets); the DOM stays ours. Offsets become\n'),
        ("  // scroller (both axes) — see ListGridBody's comment for why the split\n  // scroll structure was retired.\n  const listScrollRef = useRef<HTMLDivElement | null>(null);\n  const rowVirtualizer = useVirtualizer({\n    count: rows.length,\n    getScrollElement: () => listScrollRef.current,\n    estimateSize: () => ROW_HEIGHT,\n    overscan: 10,\n  });\n\n", '  // scroller (both axes) — see ListGridBody\'s comment for why the split\n  // scroll structure was retired.\n  const listScrollRef = useRef<HTMLDivElement | null>(null);\n  const listItems = useMemo(() => skillListItems(rows), [rows]);\n  const rowVirtualizer = useVirtualizer({\n    count: listItems.length,\n    getScrollElement: () => listScrollRef.current,\n    getItemKey: (index) => listItems[index]!.key,\n    estimateSize: (index) => listItems[index]?.kind === "scope" ? 32 : ROW_HEIGHT,\n    overscan: 10,\n  });\n\n'),
        ('            onToggleColumn={toggleColumn}\n            allRows={allRows}\n            visibleCount={rows.length}\n          />\n          <div\n            ref={listScrollRef}\n', '            onToggleColumn={toggleColumn}\n            allRows={allRows}\n            visibleCount={rows.length}\n            currentWorkspaceId={wsId}\n          />\n          <div\n            ref={listScrollRef}\n'),
        ('                </div>\n              )}\n              {virtualItems.map((vi) => {\n                const row = rows[vi.index];\n                if (!row) return null;\n                return (\n              <ListGridRow\n                key={row.skill.id}\n', '                </div>\n              )}\n              {virtualItems.map((vi) => {\n                const item = listItems[vi.index];\n                if (!item) return null;\n                if (item.kind === "scope") {\n                  return (\n                    <div key={item.key} className="col-span-full flex h-8 items-center border-y bg-muted/30 px-4 text-caption font-semibold text-muted-foreground">\n                      {item.scope === "team" ? t(($) => $.page.team_skills) : item.scope === "personal" ? t(($) => $.page.personal_skills) : t(($) => $.page.project_skills)}\n                    </div>\n                  );\n                }\n                const row = item.row;\n                return (\n              <ListGridRow\n                key={row.skill.id}\n'),
        ('                  onToggle={() => toggleSelected(row.skill.id)}\n                />\n                <NameCell row={row} />\n                {isColVisible("usedBy") ? (\n                  <UsedByCell agents={row.agents} />\n                ) : (\n                  <ListGridCell className="px-0" />\n                )}\n                {isColVisible("source") ? (\n                  <SourceCell skill={row.skill} runtime={row.runtime} />\n', '                  onToggle={() => toggleSelected(row.skill.id)}\n                />\n                <NameCell row={row} />\n                {isColVisible("scope") ? (\n                  <ScopeCell skill={row.skill} currentWorkspaceId={wsId} />\n                ) : (\n                  <ListGridCell className="px-0" />\n                )}\n                {isColVisible("usedBy") ? (\n                  <UsedByCell agents={row.agents} />\n                ) : (\n                  <ListGridCell className="px-0" />\n                )}\n                {isColVisible("usageCount") ? (\n                  <ListGridCell className="hidden whitespace-nowrap text-caption tabular-nums text-muted-foreground @2xl:flex">\n                    {row.skill.usage_count ?? 0}\n                  </ListGridCell>\n                ) : (\n                  <ListGridCell className="hidden px-0 @2xl:flex" />\n                )}\n                {isColVisible("source") ? (\n                  <SourceCell skill={row.skill} runtime={row.runtime} />\n'),
    ],
}

for _rel, _replacements in PORTED_CHANGES.items():
    CHANGES[_rel] = _replacements + CHANGES.get(_rel, [])

def apply_patches():
    for rel, original, text, unchanged in plan_patches():
        if unchanged:
            print(f"already patched {rel}")
            continue
        backup = BACKUP / rel
        backup.parent.mkdir(parents=True, exist_ok=True)
        if not backup.exists():
            backup.write_text(original)
        (REPO / rel).write_text(text)
        print(f"patched {rel}")


def restore_patches():
    planned = plan_patches()
    for rel, original, _text, _unchanged in planned:
        backup = BACKUP / rel
        if not backup.exists():
            continue
        (REPO / rel).write_text(original)
        backup.unlink()
        print(f"restored {rel}")


if __name__ == "__main__":
    action = sys.argv[1] if len(sys.argv) > 1 else "check"
    if action == "apply":
        apply_patches()
    elif action == "restore":
        restore_patches()
    elif action == "check":
        print(f"Validated {len(plan_patches())} frontend patch files; no files written.")
    else:
        raise SystemExit("usage: web-patch.py [check|apply|restore]")
