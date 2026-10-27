#!/usr/bin/env python3
"""Offline integration checks for staging-only patching and branding."""
import contextlib
import io
import json
import os
import re
import runpy
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
BRAND_FILES = [
    "apps/web/platform/document-title.ts",
    "apps/web/platform/document-title.test.tsx",
    "apps/web/app/layout.tsx",
    "apps/web/app/manifest.ts",
]


class FrontendPatches(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="multica-v061-web-test.")
        self.addCleanup(self.temp.cleanup)
        self.target = Path(self.temp.name)
        self.env = patch.dict(os.environ, {
            "MULTICA_WEB_TARGET": str(self.target),
            "MULTICA_WEB_BACKUP": str(self.target / ".overlay-backup"),
            "MULTICA_INFOFLOW_BRIDGE_URL": "http://bridge.example.test:8002",
            "MULTICA_BRAND_NAME": "",
            "MULTICA_BRAND_BRIDGE_URL": "",
            "MULTICA_VERIFICATION_BOT_NAME": "Fixture bot",
            "MULTICA_VERIFICATION_BOT_URL": "infoflow://APICenter/fixture",
            "MULTICA_LOGIN_TITLE": "Fixture title",
            "MULTICA_LOGIN_DESCRIPTION": "Fixture description",
            "MULTICA_EMAIL_PLACEHOLDER": "fixture@example.test",
            "MULTICA_CLI_INSTALL_URL": "https://example.test/install.sh",
            "MULTICA_DAEMON_RUNTIME_DEFAULTS": "ducc,codex",
            "MULTICA_DAEMON_RUNTIME_SHIM_DEFAULTS": "ducc",
            "MULTICA_ONEAPI_TRACE_URL": "https://probe.example.test/",
            "MULTICA_QUICKSTART_URL": "https://docs.example.test/quickstart",
            "MULTICA_INFOFLOW_USER_GROUP_URL": "https://community.example.test/join",
        })
        self.env.start()
        self.addCleanup(self.env.stop)
        self.module = runpy.run_path(str(ROOT / "overlays/web-patch.py"))
        self.originals = {}
        for rel in set(self.module["CHANGES"]) | set(BRAND_FILES) | {"apps/web/package.json"}:
            source = (ROOT / "multica" / rel).read_text()
            self.originals[rel] = source
            target = self.target / rel
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(source)

    def apply(self):
        with contextlib.redirect_stdout(io.StringIO()):
            self.module["apply_patches"]()

    def snapshot(self):
        return {rel: (self.target / rel).read_text() for rel in self.originals}

    def test_check_apply_idempotence_and_restore(self):
        self.assertGreater(len(self.module["plan_patches"]()), 35)
        self.assertEqual(self.originals, self.snapshot())
        self.apply()
        applied = self.snapshot()
        self.apply()
        self.assertEqual(applied, self.snapshot())
        for rel, _before, _after, _done in self.module["plan_patches"]():
            self.assertEqual((self.target / ".overlay-backup" / rel).read_text(), self.originals[rel])
        with contextlib.redirect_stdout(io.StringIO()):
            self.module["restore_patches"]()
        self.assertEqual(self.originals, self.snapshot())

    def test_late_anchor_drift_writes_nothing(self):
        rel = "packages/views/skills/components/skills-page.tsx"
        target = self.target / rel
        target.write_text(target.read_text().replace('const GRID_COLS =', 'const CHANGED_GRID_COLS ='))
        before = self.snapshot()
        with self.assertRaisesRegex(SystemExit, "no files written"):
            self.apply()
        self.assertEqual(before, self.snapshot())
        self.assertFalse((self.target / ".overlay-backup").exists())

    def test_form_controls_avoid_foreign_editor_class_without_changing_other_styles(self):
        self.apply()
        for component in self.module["FORM_BORDER_COMPONENTS"]:
            rel = f"packages/ui/components/ui/{component}.tsx"
            with self.subTest(component=component):
                original = self.originals[rel]
                self.assertEqual(original.count(" border-input "), 1)
                patched = (self.target / rel).read_text()
                self.assertEqual(patched, original.replace(" border-input ", " border-[color:var(--input)] "))
                self.assertNotIn(" border-input ", patched)
                self.assertIn(" border-[color:var(--input)] ", patched)
        for component in ("bind-infoflow-conversation", "issue-infoflow-group-binding"):
            source = (ROOT / f"overlays/web-direct/packages/views/issues/components/{component}.tsx").read_text()
            self.assertNotIn(" border-input ", source)
            self.assertIn(" border-[color:var(--input)] ", source)

    def test_explicit_build_configuration_fails_closed_and_retains_login_copy(self):
        with patch.dict(os.environ, {"MULTICA_VERIFICATION_BOT_URL": ""}):
            with self.assertRaisesRegex(SystemExit, "configuration is incomplete"):
                runpy.run_path(str(ROOT / "overlays/web-patch.py"))
        prepare = runpy.run_path(str(ROOT / "deploy/prepare-web.py"))
        config = {key: os.environ.get(key, "Fixture") or "Fixture" for key in prepare["REQUIRED"]}
        config["MULTICA_BRAND_NAME"] = "Fixture title"
        path = self.target / "build-config.json"
        path.write_text(json.dumps(config))
        self.assertEqual(prepare["load_config"](path), config)
        for key in prepare["REQUIRED"]:
            missing = {**config, key: ""}
            path.write_text(json.dumps(missing))
            with self.assertRaisesRegex(ValueError, key):
                prepare["load_config"](path)
        self.apply()
        prepare["verify_output"](self.target, config)
        login = self.target / "packages/views/auth/login-page.tsx"
        login.write_text(login.read_text().replace("$.verify.title_link", "$.verify.title"))
        with self.assertRaisesRegex(ValueError, "lost verification"):
            prepare["verify_output"](self.target, config)

    def test_runtime_ownership_overlay_keeps_existing_page_features(self):
        rel = "packages/views/runtimes/components/runtimes-page.tsx"
        (self.target / rel).write_text((ROOT / "overlays/web-direct" / rel).read_text())
        self.apply()
        page = (self.target / rel).read_text()
        self.assertIn("<DeleteMachineButton machine={machine}", page)
        self.assertIn('role === "owner" || role === "admin"', page)
        self.assertIn("checked={!showAllRuntimes}", page)
        for locale in ("en", "zh-Hans", "ja", "ko", "fr"):
            labels = json.loads((self.target / f"packages/views/locales/{locale}/runtimes.json").read_text())
            self.assertTrue(labels["page"]["only_mine"])

    def test_login_support_links_are_centered_and_preserve_the_form(self):
        self.apply()
        login = (self.target / "packages/views/auth/login-page.tsx").read_text()
        self.assertEqual(login.count('data-testid="login-support-links"'), 1)
        self.assertIn('flex flex-nowrap items-center justify-center gap-6', login)
        self.assertIn('href={"https://docs.example.test/quickstart"} target="_blank" rel="noopener noreferrer"', login)
        self.assertIn('href={"https://community.example.test/join"} target="_blank" rel="noopener noreferrer"', login)
        self.assertLess(login.index('$.signin.title'), login.index('data-testid="login-support-links"'))
        self.assertLess(login.index('data-testid="login-support-links"'), login.index('$.signin.description'))
        self.assertLess(login.index('data-testid="login-support-links"'), login.index('<form id="login-form"'))
        self.assertIn('onSubmit={handleSendCode}', login)
        self.assertIn('className="text-display-sm break-words text-balance"', login)
        self.assertIn('$.verify.title_link', login)
        for locale in ("en", "zh-Hans", "ja", "ko", "fr"):
            labels = json.loads((self.target / f"packages/views/locales/{locale}/auth.json").read_text())
            self.assertEqual(set(labels["login_links"]), {"quickstart", "community"})

    def test_task_math_uses_the_canonical_renderer_and_keeps_currency_policy(self):
        self.apply()
        renderer = (self.target / "packages/views/rich-content/rich-content.tsx").read_text()
        self.assertIn('import { normalizeLatexDelimiters } from "./latex-delimiters";', renderer)
        self.assertIn('import "katex/contrib/mhchem";', renderer)
        self.assertIn("preprocessMarkdown(normalizeLatexDelimiters(content),", renderer)
        self.assertIn("singleDollarTextMath: false", renderer)
        self.assertIn("rehypeKatex", renderer)
        self.assertTrue((ROOT / "overlays/web-direct/packages/views/rich-content/latex-delimiters.ts").exists())

    def test_reapplication_does_not_clobber_changed_staging_files(self):
        self.apply()
        target = self.target / "apps/web/proxy.ts"
        target.write_text(target.read_text() + "\n// Manual staging change\n")
        before = self.snapshot()
        with self.assertRaisesRegex(SystemExit, "changed after patching"):
            self.apply()
        self.assertEqual(before, self.snapshot())

    def test_deployed_ui_simplifications_survive_rebuild(self):
        self.apply()
        sidebar = (self.target / "packages/views/layout/app-sidebar.tsx").read_text()
        self.assertNotIn("JoinDiscordCard", sidebar)
        footer = sidebar[sidebar.index("<SidebarFooter"):]
        self.assertNotIn("HelpLauncher", sidebar)
        self.assertNotIn("MobileControlLink", sidebar)
        self.assertNotIn("infoflowGroupUrl", sidebar)
        labels = json.loads((self.target / "packages/views/locales/zh-Hans/layout.json").read_text())
        self.assertEqual(labels["sidebar"]["quick_links"]["title"], "常用链接")
        self.assertEqual(labels["sidebar"]["quick_links"]["official_docs"], "官方文档")
        dashboard = (self.target / "packages/views/layout/dashboard-layout.tsx").read_text()
        self.assertNotIn("SourceBackfillModal", dashboard)
        self.assertIn("<ModalRegistry />", dashboard)
        dialog = (self.target / "packages/views/runtimes/components/connect-remote-dialog.tsx").read_text()
        self.assertIn('platforms={["macosLinux"]}', dialog)
        installer = (self.target / "packages/views/common/cli-install-command.tsx").read_text()
        self.assertIn("platforms = PLATFORMS", installer)

    def test_code_fonts_use_a_monospace_fallback_and_common_links_precede_utilities(self):
        self.apply()
        layout = (self.target / "apps/web/app/layout.tsx").read_text()
        mono = layout[layout.index("const geistMono"):layout.index("export const metadata")]
        self.assertIn("adjustFontFallback: false", mono)
        self.assertIn('"Menlo"', mono)
        self.assertIn('"monospace"', mono)
        sidebar = (self.target / "packages/views/layout/app-sidebar.tsx").read_text()
        footer = sidebar[sidebar.index("<SidebarFooter"):]
        self.assertEqual(footer.count("<SidebarShortcutLinks "), 1)
        self.assertLess(footer.index("<SidebarShortcutLinks "), footer.index("utilityNav.map"))
        self.assertLess(footer.index("<SidebarShortcutLinks "), footer.index("<SidebarCommunityLink "))
        self.assertLess(footer.index("<SidebarCommunityLink "), footer.index("utilityNav.map"))
        self.assertIn('url={"https://community.example.test/join"}', footer)
        self.assertIn('modelTraceUrl={"https://probe.example.test/"}', footer)
        self.assertIn('quickstartUrl={"https://docs.example.test/quickstart"}', footer)
        for locale in ("en", "zh-Hans", "ja", "ko", "fr"):
            labels = json.loads((self.target / f"packages/views/locales/{locale}/layout.json").read_text())
            self.assertEqual(set(labels["sidebar"]["quick_links"]), {"title", "mobile_control", "loading", "unavailable", "copied", "copy_failed", "model_trace", "quickstart_wishlist", "official_docs"})

    def test_custom_apps_mount_after_navigation_and_stay_off_the_api(self):
        self.apply()
        sidebar = (self.target / "packages/views/layout/app-sidebar.tsx").read_text()
        content = sidebar[sidebar.index("<SidebarContent"):sidebar.index("</SidebarContent>")]
        self.assertEqual(sidebar.count("<CustomAppsNav "), 1)
        self.assertLess(content.index("aiTeamNav.map"), content.index("<CustomAppsNav "))
        # "My dashboard" is the last item of the Work group, before the AI-team group.
        self.assertEqual(sidebar.count("<CustomDashboardNavItem "), 1)
        self.assertLess(content.index("workNav.map"), content.index("<CustomDashboardNavItem "))
        self.assertLess(content.index("<CustomDashboardNavItem "), content.index("$.sidebar.ai_team_group"))
        self.assertIn('import { CustomAppsNav, CustomDashboardNavItem } from "./custom-apps-nav";', sidebar)
        index = (self.target / "packages/views/layout/index.ts").read_text()
        # POPOs lists only the viewer's own pages: no shared app list, no
        # framed-app route, no server-side list fetch.
        self.assertNotIn("CustomAppPage", index)
        proxy = (self.target / "apps/web/proxy.ts").read_text()
        self.assertNotIn("/api/custom-apps", proxy)
        for gone in ("packages/views/layout/custom-apps.ts", "packages/views/layout/custom-app-page.tsx",
                     "apps/web/app/api/custom-apps/route.ts",
                     "apps/web/app/[workspaceSlug]/(dashboard)/apps/[key]/page.tsx"):
            self.assertFalse((ROOT / "overlays/web-direct" / gone).exists(), gone)
        for locale in ("en", "zh-Hans", "ja", "ko", "fr"):
            labels = json.loads((self.target / f"packages/views/locales/{locale}/layout.json").read_text())
            if locale == "en":
                expected = set(labels["sidebar"]["custom_apps"])
                self.assertTrue({"title", "my_dashboard", "add_page", "section_title", "linked_issues", "unbind",
                                 "resize", "remove_from_dashboard", "reload_loop", "url_placeholder",
                                 "remove_from_sidebar"} <= expected)
                self.assertFalse({"recommended", "restore_hidden", "not_found"} & expected)
            self.assertEqual(labels["sidebar"]["custom_apps"]["title"], "POPOs", locale)
            self.assertEqual(set(labels["sidebar"]["custom_apps"]), expected, locale)
        self.assertIn('export { CustomDashboardPage } from "./custom-dashboard-page";', index)
        self.assertIn('export { CustomPageView } from "./custom-page-view";', index)
        client = (self.target / "packages/core/api/client.ts").read_text()
        self.assertIn("async setIssueMetadataKey(", client)
        self.assertIn("async deleteIssueMetadataKey(", client)
        detail = (self.target / "packages/views/issues/components/issue-detail.tsx").read_text()
        self.assertEqual(detail.count("<IssuePagesSection issue={issue} />"), 1)
        self.assertLess(detail.index("<PluginPanelSection issueId={issue.id} />"), detail.index("<IssuePagesSection "))
        for rel in ("packages/views/layout/custom-apps-nav.tsx",
                    "packages/views/layout/custom-pages.ts", "packages/views/layout/custom-dashboard-page.tsx",
                    "packages/views/layout/custom-page-view.tsx", "packages/views/layout/add-page-dialog.tsx",
                    "packages/views/layout/linked-issues.tsx", "packages/views/issues/components/issue-pages-section.tsx",
                    "packages/views/layout/page-frame.tsx",
                    "apps/web/app/[workspaceSlug]/(dashboard)/apps/page.tsx",
                    "apps/web/app/[workspaceSlug]/(dashboard)/apps/view/page.tsx"):
            source = (ROOT / "overlays/web-direct" / rel).read_text()
            if rel != "packages/views/layout/custom-pages.ts":
                self.assertNotIn("@multica/core/api", source, rel)
            else:
                # Bindings use only upstream issue endpoints; nothing else is sent.
                self.assertEqual(set(re.findall(r"\bapi\.(\w+)\(", source)),
                                 {"listIssues", "setIssueMetadataKey", "deleteIssueMetadataKey"})
            self.assertNotIn("postMessage", source, rel)
            self.assertNotIn("addEventListener(\"message\"", source, rel)

    def test_upstream_checkout_is_rejected(self):
        result = subprocess.run(
            ["python3", str(ROOT / "overlays/web-patch.py"), "apply"],
            env={**os.environ, "MULTICA_WEB_TARGET": str(ROOT / "multica")},
            capture_output=True, text=True,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Refusing to patch a repository checkout", result.stderr)

    def test_issue_transfer_wiring_is_complete(self):
        self.apply()
        menu = (self.target / "packages/views/issues/actions/issue-actions-dropdown.tsx").read_text()
        self.assertIn('role === "owner" || role === "admin"', menu)
        self.assertIn("<IssueTransferDialog", menu)
        client = (self.target / "packages/core/api/client.ts").read_text()
        self.assertIn("/transfer-preview", client)
        self.assertIn("IssueTransferPlanSchema", client)
        events = (self.target / "packages/core/realtime/use-realtime-sync.ts").read_text()
        self.assertIn('ws.on("issue:workspace_changed"', events)
        self.assertIn("unsubIssueTransferred();", events)
        for locale in ("en", "zh-Hans"):
            copy = json.loads((self.target / f"packages/views/locales/{locale}/issues.json").read_text())
            self.assertTrue(copy["transfer"]["warning"])
            self.assertTrue(copy["transfer"]["block_unknown"])

    def test_issue_group_binding_wiring_and_translations(self):
        self.apply()
        client = (self.target / "packages/core/api/client.ts").read_text()
        self.assertIn("async getIssueInfoFlowGroupBinding", client)
        self.assertIn("async bindIssueInfoFlowGroup", client)
        self.assertIn("InfoFlowGroupBindingSchema, null", client)
        self.assertIn("InfoFlowGroupTargetSchema.safeParse(target)", client)
        self.assertIn("async getIssueInfoFlowGroups", client)
        detail = (self.target / "packages/views/issues/components/issue-detail.tsx").read_text()
        self.assertIn("<BindInfoFlowConversation", detail)
        self.assertNotIn("<IssueInfoFlowGroups ", detail)
        fixture = (self.target / "packages/views/issues/components/issue-detail.test.tsx").read_text()
        self.assertIn("getIssueInfoFlowGroupBinding: vi.fn()", fixture)
        exports = json.loads((self.target / "packages/core/package.json").read_text())["exports"]
        self.assertEqual(exports["./issues/infoflow-group-binding"], "./issues/infoflow-group-binding.ts")
        self.assertEqual(exports["./issues/infoflow-groups"], "./issues/infoflow-groups.ts")
        copies = [json.loads((self.target / f"packages/views/locales/{locale}/issues.json").read_text())["infoflow_groups"]
                  for locale in ("en", "zh-Hans")]
        self.assertEqual(set(copies[0]), set(copies[1]))
        for copy in copies:
            for key in ("bind", "warning", "waiting", "approval", "failed", "retry", "invalid", "read_failed", "open"):
                self.assertTrue(copy[key])

    def test_unified_conversation_wiring_and_private_privacy(self):
        self.apply()
        client = (self.target / "packages/core/api/client.ts").read_text()
        for method in ("getIssueInfoFlowConversations", "bindIssueInfoFlowConversation", "unbindIssueInfoFlowConversation", "cancelIssueInfoFlowRequest"):
            self.assertIn(f"async {method}", client)
        self.assertIn("InfoFlowConversationsSchema, null", client)
        self.assertIn("InfoFlowConversationTargetSchema.safeParse(target)", client)
        self.assertIn("InfoFlowConversationBindingIdSchema.safeParse(bindingId)", client)
        self.assertIn("InfoFlowConversationRequestTargetSchema.safeParse(target)", client)
        self.assertIn("const { request_id, ...body } = input.data;", client)
        self.assertIn("/infoflow-conversation-requests/", client)
        self.assertIn('method: "DELETE"', client)
        exports = json.loads((self.target / "packages/core/package.json").read_text())["exports"]
        self.assertEqual(exports["./issues/infoflow-conversations"], "./issues/infoflow-conversations.ts")
        detail = (self.target / "packages/views/issues/components/issue-detail.tsx").read_text()
        self.assertIn('<BindInfoFlowConversation key={`${issue.workspace_id}:${issue.id}`}', detail)
        fixture = (self.target / "packages/views/issues/components/issue-detail.test.tsx").read_text()
        self.assertIn("getIssueInfoFlowConversations: vi.fn()", fixture)
        copies = [json.loads((self.target / f"packages/views/locales/{locale}/issues.json").read_text())["infoflow_conversations"]
                  for locale in ("en", "zh-Hans")]
        self.assertEqual(set(copies[0]), set(copies[1]))
        for copy in copies:
            for key in ("bind", "private", "private_restriction", "needs_rebind", "reassignment", "unbind", "group_pending"):
                self.assertTrue(copy[key])

    def test_brand_is_checked_idempotent_and_keeps_bot_identity(self):
        self.apply()
        command = ["node", str(ROOT / "deploy/apply-brand.cjs"), str(self.target)]
        before = self.snapshot()
        default_env = {**os.environ, "MULTICA_LOGIN_TITLE": ""}
        # Default branding preserves all existing deployment configuration.
        subprocess.run([*command, "--check"], env=default_env, check=True, capture_output=True)
        self.assertEqual(before, self.snapshot())
        subprocess.run(command, env=default_env, check=True, capture_output=True)
        self.assertEqual(before, self.snapshot())

        configured_env = {**default_env, "MULTICA_BRAND_NAME": "Multica · Example"}
        subprocess.run([*command, "--check"], env=configured_env, check=True, capture_output=True)
        self.assertEqual(before, self.snapshot())
        subprocess.run(command, env=configured_env, check=True, capture_output=True)
        branded = self.snapshot()
        subprocess.run(command, env=configured_env, check=True, capture_output=True)
        self.assertEqual(branded, self.snapshot())
        # Removing the override must not reset an already configured build.
        subprocess.run(command, env=default_env, check=True, capture_output=True)
        self.assertEqual(branded, self.snapshot())
        for locale in ("zh-Hans", "en"):
            auth = json.loads(branded[f"packages/views/locales/{locale}/auth.json"])
            self.assertEqual(auth["signin"]["title"], "Multica · Example")
            self.assertEqual(auth["verify"]["title_link"], "Fixture bot")
        self.assertIn('export const SITE_TITLE = "Multica · Example";', branded["apps/web/platform/document-title.ts"])
        self.assertIn('export const TITLE_SUFFIX = " | Multica · Example";', branded["apps/web/platform/document-title.ts"])
        self.assertIn('    short_name: "Multica · Example",', branded["apps/web/app/manifest.ts"])
        self.assertIn('    title: "Multica · Example",', branded["apps/web/app/layout.tsx"])
        self.assertIn('    siteName: "Multica · Example",', branded["apps/web/app/layout.tsx"])
        agents = "packages/views/agents/components/agents-page.tsx"
        self.assertEqual(branded[agents], before[agents])
        self.assertIn("http://bridge.example.test:8002", branded[agents])

        # A separate explicit override can change only the Bridge destination.
        bridge_env = {**default_env, "MULTICA_BRAND_BRIDGE_URL": "https://override.example.test:8002"}
        subprocess.run([*command, "--check"], env=bridge_env, check=True, capture_output=True)
        self.assertEqual(branded, self.snapshot())
        subprocess.run(command, env=bridge_env, check=True, capture_output=True)
        overridden = self.snapshot()
        self.assertEqual(overridden[agents], branded[agents].replace("http://bridge.example.test:8002", "https://override.example.test:8002"))
        self.assertEqual({rel: text for rel, text in overridden.items() if rel != agents},
                         {rel: text for rel, text in branded.items() if rel != agents})
        subprocess.run(command, env=bridge_env, check=True, capture_output=True)
        self.assertEqual(overridden, self.snapshot())

    def test_login_title_is_independent_of_site_brand_and_survives_final_verification(self):
        self.apply()
        command = ["node", str(ROOT / "deploy/apply-brand.cjs"), str(self.target)]
        title = "Team Mesh：组织 AI 协作平台"
        config = {key: os.environ.get(key, "Fixture") or "Fixture" for key in runpy.run_path(str(ROOT / "deploy/prepare-web.py"))["REQUIRED"]}
        config.update(MULTICA_BRAND_NAME="Team Mesh", MULTICA_LOGIN_TITLE=title)
        env = {**os.environ, **config}
        before = self.snapshot()
        subprocess.run([*command, "--check"], env=env, check=True, capture_output=True)
        self.assertEqual(before, self.snapshot())
        subprocess.run(command, env=env, check=True, capture_output=True)
        for locale in ("zh-Hans", "en"):
            auth = json.loads((self.target / f"packages/views/locales/{locale}/auth.json").read_text())
            self.assertEqual(auth["signin"]["title"], title)
            self.assertEqual(auth["verify"]["title_link"], "Fixture bot")
            original_auth = json.loads(before[f"packages/views/locales/{locale}/auth.json"])
            original_auth["signin"]["title"] = title
            self.assertEqual(auth, original_auth)
        self.assertIn('export const SITE_TITLE = "Team Mesh";', (self.target / "apps/web/platform/document-title.ts").read_text())
        runpy.run_path(str(ROOT / "deploy/prepare-web.py"))["verify_output"](self.target, config)
        branded = self.snapshot()
        subprocess.run(command, env=env, check=True, capture_output=True)
        self.assertEqual(branded, self.snapshot())
        login_only_env = {**env, "MULTICA_BRAND_NAME": "", "MULTICA_LOGIN_TITLE": title + "（预览）"}
        subprocess.run(command, env=login_only_env, check=True, capture_output=True)
        after = self.snapshot()
        for rel in BRAND_FILES:
            self.assertEqual(after[rel], branded[rel])
        self.assertEqual(json.loads(after["packages/views/locales/zh-Hans/auth.json"])["signin"]["title"], title + "（预览）")

    def test_brand_anchor_drift_writes_nothing(self):
        self.apply()
        target = self.target / "apps/web/app/manifest.ts"
        target.write_text(target.read_text().replace('    short_name:', '    upstream_short_name:'))
        before = self.snapshot()
        result = subprocess.run(
            ["node", str(ROOT / "deploy/apply-brand.cjs"), str(self.target)],
            env={**os.environ, "MULTICA_BRAND_NAME": "Multica · Example"},
            capture_output=True,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(before, self.snapshot())


if __name__ == "__main__":
    unittest.main(verbosity=2)
