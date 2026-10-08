#!/usr/bin/env python3
"""Offline integration checks for staging-only patching and branding."""
import contextlib
import io
import json
import os
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

    def test_reapplication_does_not_clobber_changed_staging_files(self):
        self.apply()
        target = self.target / "apps/web/proxy.ts"
        target.write_text(target.read_text() + "\n// Manual staging change\n")
        before = self.snapshot()
        with self.assertRaisesRegex(SystemExit, "changed after patching"):
            self.apply()
        self.assertEqual(before, self.snapshot())

    def test_upstream_checkout_is_rejected(self):
        result = subprocess.run(
            ["python3", str(ROOT / "overlays/web-patch.py"), "apply"],
            env={**os.environ, "MULTICA_WEB_TARGET": str(ROOT / "multica")},
            capture_output=True, text=True,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Refusing to patch a repository checkout", result.stderr)

    def test_brand_is_checked_idempotent_and_keeps_bot_identity(self):
        self.apply()
        command = ["node", str(ROOT / "deploy/apply-brand.cjs"), str(self.target)]
        before = self.snapshot()
        # Default branding preserves all existing deployment configuration.
        subprocess.run([*command, "--check"], check=True, capture_output=True)
        self.assertEqual(before, self.snapshot())
        subprocess.run(command, check=True, capture_output=True)
        self.assertEqual(before, self.snapshot())

        configured_env = {**os.environ, "MULTICA_BRAND_NAME": "Multica · Example"}
        subprocess.run([*command, "--check"], env=configured_env, check=True, capture_output=True)
        self.assertEqual(before, self.snapshot())
        subprocess.run(command, env=configured_env, check=True, capture_output=True)
        branded = self.snapshot()
        subprocess.run(command, env=configured_env, check=True, capture_output=True)
        self.assertEqual(branded, self.snapshot())
        # Removing the override must not reset an already configured build.
        subprocess.run(command, check=True, capture_output=True)
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
        bridge_env = {**os.environ, "MULTICA_BRAND_BRIDGE_URL": "https://override.example.test:8002"}
        subprocess.run([*command, "--check"], env=bridge_env, check=True, capture_output=True)
        self.assertEqual(branded, self.snapshot())
        subprocess.run(command, env=bridge_env, check=True, capture_output=True)
        overridden = self.snapshot()
        self.assertEqual(overridden[agents], branded[agents].replace("http://bridge.example.test:8002", "https://override.example.test:8002"))
        self.assertEqual({rel: text for rel, text in overridden.items() if rel != agents},
                         {rel: text for rel, text in branded.items() if rel != agents})
        subprocess.run(command, env=bridge_env, check=True, capture_output=True)
        self.assertEqual(overridden, self.snapshot())

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
