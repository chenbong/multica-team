#!/usr/bin/env python3
"""Apply checked source edits to the web app for one build, then undo them.

The Go side never touches the checkout: it builds with `go build -overlay` and
prepare.py generates the rewritten copies. Next.js has no equivalent, so the
same discipline is enforced by hand here - every edit must match exactly once,
the original is copied into generated/web-backup/ before it is written, and
restore puts it back whether the build succeeded or not. If upstream moves an
anchor the build stops instead of silently producing an unpatched site.
"""
import json
import os
import shutil
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = Path(os.environ.get("MULTICA_WEB_TARGET", str(HERE.parent / "multica"))).resolve()
BACKUP = Path(os.environ.get("MULTICA_WEB_BACKUP", str(HERE / "generated" / "web-backup"))).resolve()

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

# The "Add computer" dialog hands out the daemon command. A daemon started
# without --profile writes ~/.multica/config.json, so the second Multica account
# to run it on the same computer overwrites the first account's token; the
# daemon re-registers under the same daemon.id, the runtime owner changes, and
# the previous owner's agents queue forever against a private runtime that is no
# longer theirs. Naming the profile after the signed-in user gives every account
# its own config, daemon id, health port and workspaces root, so several people
# can host runtimes on one computer.
#
# A deployment that wants the handed-out command to pin the runtime allowlist
# (see MULTICA_DAEMON_RUNTIMES) sets MULTICA_DAEMON_RUNTIME_DEFAULTS: the line
# `config set daemon_runtimes <ids>` is then appended to every command block -
# the interactive `setup` of step 2 as well as the token login - so anyone who
# copies one gets exactly those runtimes instead of every CLI found on the
# machine. Empty keeps upstream's commands.
#
# MULTICA_DAEMON_RUNTIME_SHIM_DEFAULTS does the same for commands that need a
# generated wrapper (see runtime_shims in the CLI): the handed-out command also
# carries `config set runtime_shims <names>`, so a fresh machine installs the
# wrapper as soon as its daemon starts.
OLD_COMMANDS = '''function daemonCommands(serverUrl: string | undefined, appUrl: string | undefined) {
  const normalizedServerUrl = normalizeCommandURL(serverUrl);
  const normalizedAppUrl = normalizeCommandURL(appUrl);
  if (normalizedServerUrl && normalizedAppUrl) {
    return {
      setupCmd: `multica setup self-host --server-url ${normalizedServerUrl} --app-url ${normalizedAppUrl}`,
      tokenCmd: `multica config set server_url ${normalizedServerUrl}
multica config set app_url ${normalizedAppUrl}
multica login --token <YOUR_TOKEN>
multica daemon start`,
    };
  }

  return {
    setupCmd: "multica setup",
    tokenCmd: `multica config set server_url ${CLOUD_SERVER_URL}
multica config set app_url ${CLOUD_APP_URL}
multica login --token <YOUR_TOKEN>
multica daemon start`,
  };
}
'''

NEW_COMMANDS = '''// Local overlay: the profile name is the local part of the signed-in account
// email, sanitised because it becomes a directory under ~/.multica/profiles/.
// An account with no readable email falls back to the upstream commands.
function profileNameFromEmail(email: string | undefined) {
  const local = (email ?? "").split("@")[0] ?? "";
  return local
    .toLowerCase()
    .replace(/[^a-z0-9._-]/g, "-")
    .replace(/^[-._]+/, "")
    .replace(/[-._]+$/, "");
}

function daemonCommands(
  serverUrl: string | undefined,
  appUrl: string | undefined,
  profile: string,
  cliToken?: string,
) {
  const normalizedServerUrl = normalizeCommandURL(serverUrl);
  const normalizedAppUrl = normalizeCommandURL(appUrl);
  const p = profile ? ` --profile ${profile}` : "";
  if (normalizedServerUrl && normalizedAppUrl) {
    return {
      setupCmd: `multica${p} setup self-host --server-url ${normalizedServerUrl} --app-url ${normalizedAppUrl}__RUNTIME_LINE____SHIM_LINE__`,
      tokenCmd: `multica${p} config set server_url ${normalizedServerUrl}
multica${p} config set app_url ${normalizedAppUrl}
multica${p} login --token ${cliToken || "<YOUR_TOKEN>"}__RUNTIME_LINE____SHIM_LINE__
multica${p} daemon start`,
    };
  }

  return {
    setupCmd: `multica${p} setup__RUNTIME_LINE____SHIM_LINE__`,
    tokenCmd: `multica${p} config set server_url ${CLOUD_SERVER_URL}
multica${p} config set app_url ${CLOUD_APP_URL}
multica${p} login --token ${cliToken || "<YOUR_TOKEN>"}__RUNTIME_LINE____SHIM_LINE__
multica${p} daemon start`,
  };
}
'''

OLD_CALLSITE = '''  const daemonAppUrl = useConfigStore((s) => s.daemonAppUrl);
  const { setupCmd, tokenCmd } = daemonCommands(daemonServerUrl, daemonAppUrl);
'''

NEW_CALLSITE = '''  const daemonAppUrl = useConfigStore((s) => s.daemonAppUrl);
  const [cliToken, setCliToken] = useState<string>();
  useEffect(() => {
    let cancelled = false;
    void api.getOrCreateDaemonBootstrapToken().then(({ token }) => {
      if (!cancelled && token) setCliToken(token);
    }).catch(() => {});
    return () => { cancelled = true; };
  }, []);
  const daemonProfile = profileNameFromEmail(
    useAuthStore((s) => s.user?.email),
  );
  const { setupCmd, tokenCmd } = daemonCommands(
    daemonServerUrl,
    daemonAppUrl,
    daemonProfile,
    cliToken,
  );
'''

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
# The extra command line: `config set daemon_runtimes <ids>` right after login,
# or nothing at all when the deployment does not pin a list.
RUNTIME_LINE = (
    f"\nmultica${{p}} config set daemon_runtimes {RUNTIME_DEFAULTS}"
    if RUNTIME_DEFAULTS
    else ""
)
NEW_COMMANDS = NEW_COMMANDS.replace("__RUNTIME_LINE__", RUNTIME_LINE)
assert "__RUNTIME_LINE__" not in NEW_COMMANDS


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
# `config set runtime_shims <names>` after the runtime line, or nothing at all
# when the deployment does not declare any wrapper.
SHIM_LINE = (
    f"\nmultica${{p}} config set runtime_shims {SHIM_DEFAULTS}"
    if SHIM_DEFAULTS
    else ""
)
NEW_COMMANDS = NEW_COMMANDS.replace("__SHIM_LINE__", SHIM_LINE)
assert "__SHIM_LINE__" not in NEW_COMMANDS


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
            '      "scope_project_prefix": "Project: ",\n'
            '      "scope_project_default": "current project",\n'
            '      "scope_personal": "Personal",\n',
        ),
    ],
    AGENTS_ZH: [
        (
            '    "skills_section": {\n      "label": "Skills",\n',
            '    "skills_section": {\n      "label": "Skills",\n'
            '      "scope_team": "团队",\n'
            '      "scope_project_prefix": "项目：",\n'
            '      "scope_project_default": "当前项目",\n'
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
    SKILLS_EN: [
        ('    "new_skill": "New skill",', '    "new_skill": "New skill",\n    "team_skills": "Team skills",\n    "project_skills": "Project skills",\n    "personal_skills": "Personal skills",'),
        ('    "used_by": "Used by",', '    "used_by": "Used by",\n    "usage_count": "Usage count",'),
        ('    "source": "Source",', '    "source": "Source",\n    "level": "Level",\n    "level_team": "Team",\n    "level_project": "Project",\n    "level_project_prefix": "Project: ",\n    "level_personal": "Personal",\n    "level_current_project": "Current project",'),
        ('  "detail": {', '  "detail": {\n    "category_label": "Skill category",\n    "category_team": "Team",\n    "category_project": "Project",\n    "category_personal": "Personal",'),
        ('  "actions": {\n    "row_menu":', '  "actions": {\n    "change_level": "Change level",\n    "change_level_title": "Change skill level",\n    "change_level_desc": "Only the skill creator can change its level.",\n    "change_level_save": "Save level",\n    "change_level_saved": "Skill level updated",\n    "change_level_failed": "Failed to change skill level",\n    "row_menu":'),
        ('    "section_usage": "Usage",', '    "section_usage": "Usage",\n    "section_projects": "Projects",\n    "other_projects": "Other projects",'),
        (
            '  "create": {',
            '  "create": {\n    "scope": {"title": "Scope", "desc": "Choose whether this skill belongs to the team, a project, or only to you.", "team": {"title": "Team skill", "desc": "Visible to every user across every project."}, "workspace": {"title": "Project skill", "desc": "Visible across users and projects; keeps this project as its source."}, "personal": {"title": "Personal skill", "desc": "Reusable by you across workspaces."}},',
        ),
    ],
    SKILLS_ZH: [
        ('    "new_skill": "新建 skill",', '    "new_skill": "新建 skill",\n    "team_skills": "团队 Skill",\n    "project_skills": "项目 Skill",\n    "personal_skills": "个人 Skill",'),
        ('    "used_by": "被谁使用",', '    "used_by": "被谁使用",\n    "usage_count": "使用次数",'),
        ('    "source": "来源",', '    "source": "来源",\n    "level": "Skill 级别",\n    "level_team": "团队",\n    "level_project": "项目",\n    "level_project_prefix": "项目：",\n    "level_personal": "个人",\n    "level_current_project": "当前项目",'),
        ('  "detail": {', '  "detail": {\n    "category_label": "Skill 类别",\n    "category_team": "团队",\n    "category_project": "项目",\n    "category_personal": "个人",'),
        ('  "actions": {\n    "row_menu":', '  "actions": {\n    "change_level": "调整级别",\n    "change_level_title": "调整 Skill 级别",\n    "change_level_desc": "只有 Skill 添加者可以调整级别。",\n    "change_level_save": "保存级别",\n    "change_level_saved": "Skill 级别已更新",\n    "change_level_failed": "调整 Skill 级别失败",\n    "row_menu":'),
        ('    "section_usage": "使用状态",', '    "section_usage": "使用状态",\n    "section_projects": "项目",\n    "other_projects": "其他项目",'),
        (
            '  "create": {',
            '  "create": {\n    "scope": {"title": "Skill 范围", "desc": "选择这个 Skill 属于团队、项目还是仅属于你。", "team": {"title": "团队 Skill", "desc": "所有用户、所有项目都可见。"}, "workspace": {"title": "项目 Skill", "desc": "跨用户、跨项目可见，并保留来源项目。"}, "personal": {"title": "个人 Skill", "desc": "你可以跨工作区复用。"}},',
        ),
    ],
    SKILLS_PAGE: [
        (
            '    const dir = sortDirection === "asc" ? 1 : -1;\n    filtered.sort((a, b) => {',
            '    const dir = sortDirection === "asc" ? 1 : -1;\n    filtered.sort((a, b) => {\n      const scopeRank = (scope: string | undefined) => scope === "team" ? 0 : scope === "workspace" ? 1 : 2;\n      const scopeOrder = scopeRank(a.skill.scope) - scopeRank(b.skill.scope);\n      if (scopeOrder !== 0) return scopeOrder;',
        ),
        (
            '              {virtualItems.map((vi) => {\n                const row = rows[vi.index];\n                if (!row) return null;\n                return (',
            '              {virtualItems.map((vi) => {\n                const row = rows[vi.index];\n                if (!row) return null;\n                const scope = row.skill.scope ?? "workspace";\n                const previousScope = rows[vi.index - 1]?.skill.scope ?? "workspace";\n                const startsScopeSection = vi.index === 0 || scope !== previousScope;\n                return (\n                  <>\n                    {startsScopeSection && (\n                      <div className="col-span-full border-y bg-muted/30 px-4 py-2 text-caption font-semibold text-muted-foreground">\n                        {scope === "team" ? t(($) => $.page.team_skills) : scope === "personal" ? t(($) => $.page.personal_skills) : t(($) => $.page.project_skills)}\n                      </div>\n                    )}',
        ),
        (
            '              </ListGridRow>\n                );\n              })}',
            '              </ListGridRow>\n                  </>\n                );\n              })}',
        ),
    ],

    "packages/core/api/client.ts": [
        ('  async issueCliToken(): Promise<{ token: string }> {\n    return this.fetch("/api/cli-token", { method: "POST" });\n  }', '  async issueCliToken(): Promise<{ token: string }> {\n    return this.fetch("/api/cli-token", { method: "POST" });\n  }\n\n  async getOrCreateDaemonBootstrapToken(): Promise<{ token: string }> {\n    return this.fetch("/api/daemon-bootstrap-token", { method: "POST" });\n  }')],

    DIALOG: [
        (
            'const INSTALL_CMD =\n'
            '  "curl -fsSL https://raw.githubusercontent.com/multica-ai/multica/main/scripts/install.sh | bash";',
            'const INSTALL_CMD =\n'
            f'  "curl -fsSL {CLI_INSTALL_URL} | bash";',
        ),
        (
            'import { useConfigStore } from "@multica/core/config";\n',
            'import { useConfigStore } from "@multica/core/config";\n'
            'import { useAuthStore } from "@multica/core/auth";\n'
            'import { api } from "@multica/core/api";\n',
        ),
        (
            OLD_COMMANDS,
            NEW_COMMANDS,
        ),
        (
            OLD_CALLSITE,
            NEW_CALLSITE,
        ),
        (
            '          <TroubleshootingDetails tokenCmd={tokenCmd} />',
            '          <TroubleshootingDetails tokenCmd={tokenCmd} profile={daemonProfile} />',
        ),
        (
            'function TroubleshootingDetails({ tokenCmd }: { tokenCmd: string }) {\n'
            '  const { t } = useT("runtimes");\n',
            'function TroubleshootingDetails({\n'
            '  tokenCmd,\n'
            '  profile,\n'
            '}: {\n'
            '  tokenCmd: string;\n'
            '  profile: string;\n'
            '}) {\n'
            '  const { t } = useT("runtimes");\n'
            '  // Local overlay: the status and log commands have to carry the same\n'
            '  // profile, or the person who just started the daemon is told it is not\n'
            '  // running.\n'
            '  const p = profile ? ` --profile ${profile}` : "";\n',
        ),
        (
            '              {"multica daemon status"}',
            '              {`multica${p} daemon status`}',
        ),
        (
            '              {"multica daemon logs -f"}',
            '              {`multica${p} daemon logs -f`}',
        ),
    ],
}

def apply_patches():
    for rel, replacements in CHANGES.items():
        # A file with no configured replacements stays exactly as upstream.
        if not replacements:
            continue
        source = REPO / rel
        original = source.read_text()
        text = original
        for before, after in replacements:
            # Failed/interrupted builds can leave an already-patched checkout.
            # Treat the exact replacement as idempotent instead of injecting it twice.
            if text.count(after) == 1:
                print(f"already patched {rel}")
                continue
            if text.count(before) != 1:
                raise SystemExit(
                    f"Upstream changed: {rel} has {text.count(before)} occurrences of an "
                    f"expected anchor (need exactly 1). Review overlays/web-patch.py "
                    f"before rebuilding."
                )
            text = text.replace(before, after, 1)
        backup = BACKUP / rel
        backup.parent.mkdir(parents=True, exist_ok=True)
        backup.write_text(original)
        source.write_text(text)
        print(f"patched {rel}")


def restore_patches():
    for rel in CHANGES:
        backup = BACKUP / rel
        if not backup.exists():
            continue
        shutil.copyfile(backup, REPO / rel)
        backup.unlink()
        print(f"restored {rel}")


action = sys.argv[1] if len(sys.argv) > 1 else "apply"
if action == "apply":
    apply_patches()
elif action == "restore":
    restore_patches()
else:
    raise SystemExit("usage: web-patch.py [apply|restore]")
