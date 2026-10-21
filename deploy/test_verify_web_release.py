import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("verify_web_release", Path(__file__).with_name("verify-web-release.py"))
verify = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verify)


class LoginReleaseAcceptanceTest(unittest.TestCase):
    def setUp(self):
        self.config = {
            "MULTICA_LOGIN_TITLE": "Example：协作平台", "MULTICA_LOGIN_DESCRIPTION": "输入企业邮箱",
            "MULTICA_VERIFICATION_BOT_NAME": "Example机器人", "MULTICA_EMAIL_PLACEHOLDER": "user@example.com",
            "MULTICA_VERIFICATION_BOT_URL": "infoflow://APICenter?data=example%3D",
            "MULTICA_QUICKSTART_URL": "https://example.com/start",
            "MULTICA_INFOFLOW_USER_GROUP_URL": "https://example.com/community",
        }
        self.snapshot = {"url": "http://localhost/login", "text": "Example：协作平台 输入企业邮箱 如果从未使用过Example机器人，请先打开对话窗口",
                         "links": [self.config[key] for key in verify.LINK_KEYS],
                         "placeholders": [self.config["MULTICA_EMAIL_PLACEHOLDER"]], "emailLabel": True, "continueButton": True}

    def test_rendered_login_passes(self):
        verify.verify_rendered(self.snapshot, self.config)

    def test_http_success_or_default_login_is_not_acceptance(self):
        for text in ("", "登录 Multica 输入邮箱以获取登录验证码"):
            with self.subTest(text=text), self.assertRaises(ValueError):
                verify.verify_rendered({**self.snapshot, "text": text}, self.config)

    def test_every_custom_field_is_required(self):
        for key in verify.TEXT_KEYS[:-1]:
            with self.subTest(key=key), self.assertRaises(ValueError):
                verify.verify_rendered({**self.snapshot, "text": self.snapshot["text"].replace(self.config[key], "")}, self.config)
        for key in verify.LINK_KEYS:
            with self.subTest(key=key), self.assertRaises(ValueError):
                verify.verify_rendered({**self.snapshot, "links": [v for v in self.snapshot["links"] if v != self.config[key]]}, self.config)
        with self.assertRaises(ValueError):
            verify.verify_rendered({**self.snapshot, "placeholders": []}, self.config)

    def test_controls_and_route_are_required(self):
        for field in ("emailLabel", "continueButton"):
            with self.subTest(field=field), self.assertRaises(ValueError):
                verify.verify_rendered({**self.snapshot, field: False}, self.config)
        with self.assertRaises(ValueError):
            verify.verify_rendered({**self.snapshot, "url": "http://localhost/issues"}, self.config)

    def test_compiled_build_not_just_customized_source(self):
        with tempfile.TemporaryDirectory() as folder:
            target = Path(folder)
            output = target / "apps/web/.next"
            (output / "server").mkdir(parents=True)
            (output / "static").mkdir()
            (output / "BUILD_ID").write_text("fixture")
            bundle = output / "static/login.js"
            bundle.write_text("登录 Multica")
            with patch.object(verify, "preparation") as prep:
                prep.return_value.verify_output.return_value = None
                with self.assertRaises(ValueError):
                    verify.verify_build(target, self.config)
                bundle.write_text(" ".join(self.config.values()).encode("unicode_escape").decode())
                verify.verify_build(target, self.config)
                (output / "BUILD_ID").unlink()
                with self.assertRaises(ValueError):
                    verify.verify_build(target, self.config)


if __name__ == "__main__":
    unittest.main()
