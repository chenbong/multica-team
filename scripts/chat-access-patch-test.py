#!/usr/bin/env python3
"""Offline atomicity and regeneration checks for the chat sidecar."""
import runpy
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
Base = runpy.run_path(str(ROOT / "scripts/v061-web-patch-test.py"))["FrontendPatches"]


class SharedChatPatchPlan(Base):
    def setUp(self):
        super().setUp()
        runpy.run_path(str(ROOT / "overlays/chat-access-web-patch.py"))["extend"](self.module["CHANGES"])
        for relative in self.module["CHANGES"]:
            if relative in self.originals:
                continue
            source = (ROOT / "multica" / relative).read_text()
            self.originals[relative] = source
            target = self.target / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(source)

    def test_chat_plan_retains_login_copy_and_owner_gates(self):
        self.apply()
        login = (self.target / "packages/views/auth/login-page.tsx").read_text()
        self.assertIn("$.verify.title_link", login)
        page = (self.target / "packages/views/chat/chat-page.tsx").read_text()
        self.assertIn("canManage={c.isSessionOwner}", page)
        self.assertIn("userId={c.user?.id}", page)
        self.assertNotIn("is_private", page)

    def test_chat_anchor_failure_is_atomic(self):
        relative = "packages/views/chat/components/chat-session-header.tsx"
        target = self.target / relative
        target.write_text(target.read_text().replace("  onArchive,", "  changedArchive,"))
        before = self.snapshot()
        with self.assertRaises(SystemExit):
            self.apply()
        self.assertEqual(before, self.snapshot())
        self.assertFalse((self.target / ".overlay-backup").exists())


if __name__ == "__main__":
    unittest.main()
