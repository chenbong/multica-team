"""Lifecycle environment tests: only fake deployment files, never live processes."""
import importlib.util
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

BRIDGE_ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("bridgectl_test_subject", BRIDGE_ROOT / "scripts" / "bridgectl.py")
ctl = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ctl)
TOKEN = "fixture-only-relay-token-not-a-real-credential"


class BridgeEnvironmentTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="bridge-env-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bridge = self.root / "infoflow-bridge"
        self.bridge.mkdir()
        self.deploy = self.root / "deploy"
        self.deploy.mkdir()
        self.host = self.deploy / "host.env"
        self.secret = self.deploy / "secrets.env"
        self.host.write_text("export INFOFLOW_ISSUE_RELAY_ENABLED=true\nexport INFOFLOW_ISSUE_IDENTITY_DOMAIN=example.test\n")
        for name, value in {"ROOT": self.bridge, "CONFIG": self.bridge / "config.local.json",
                            "LOG": self.bridge / "logs" / "bridge.log",
                            "WATCHDOG_PID_FILE": self.bridge / "watchdog.pid"}.items():
            p = patch.object(ctl, name, value)
            p.start()
            self.addCleanup(p.stop)
        p = patch.dict(os.environ, {"PATH": os.environ["PATH"]}, clear=True)
        p.start()
        self.addCleanup(p.stop)

    def write_secret(self, token=TOKEN):
        self.secret.write_text(f"export INFOFLOW_ISSUE_RELAY_TOKEN='{token}'\nexport JWT_SECRET=private-jwt-fixture\nexport MULTICA_DATABASE_PASSWORD=private-db-fixture\n")
        self.secret.chmod(0o600)

    def test_fresh_shell_loads_only_relay_secret(self):
        self.write_secret()
        env = ctl.bridge_environment()
        self.assertEqual(env["INFOFLOW_ISSUE_RELAY_TOKEN"], TOKEN)
        self.assertEqual(env["INFOFLOW_ISSUE_RELAY_ENABLED"], "true")
        self.assertNotIn("JWT_SECRET", env)
        self.assertNotIn("MULTICA_DATABASE_PASSWORD", env)
        self.assertEqual(self.secret.stat().st_mode & 0o777, 0o600)

    def test_file_rotation_overrides_stale_inherited_token(self):
        os.environ["INFOFLOW_ISSUE_RELAY_TOKEN"] = "stale-fixture-token-at-least-32-characters"
        self.write_secret()
        self.assertEqual(ctl.bridge_environment()["INFOFLOW_ISSUE_RELAY_TOKEN"], TOKEN)

    def test_environment_only_container_configuration(self):
        os.environ["INFOFLOW_ISSUE_RELAY_TOKEN"] = TOKEN
        self.assertEqual(ctl.bridge_environment()["INFOFLOW_ISSUE_RELAY_TOKEN"], TOKEN)

    def test_missing_or_invalid_secret_rejected_without_echo(self):
        for token in (None, "short", "a" * 32 + "\n", ""):
            with self.subTest(token_length=None if token is None else len(token)):
                if token is not None:
                    self.write_secret(token)
                with self.assertRaisesRegex(RuntimeError, "must be configured") as error:
                    ctl.bridge_environment()
                self.assertNotIn(TOKEN, str(error.exception))

    def test_disabled_relay_needs_no_secret_and_does_not_inherit_one(self):
        self.host.write_text("export INFOFLOW_ISSUE_RELAY_ENABLED=false\n")
        os.environ["INFOFLOW_ISSUE_RELAY_TOKEN"] = TOKEN
        self.assertNotIn("INFOFLOW_ISSUE_RELAY_TOKEN", ctl.bridge_environment())

    def test_source_failure_does_not_expose_secret_output(self):
        self.secret.write_text(f"echo '{TOKEN}' >&2\nreturn 1\n")
        with self.assertRaisesRegex(RuntimeError, "Cannot load") as error:
            ctl.bridge_environment()
        self.assertNotIn(TOKEN, str(error.exception))

    def test_invalid_restart_does_not_stop_current_process(self):
        with patch.object(sys, "argv", ["bridgectl.py", "restart"]), \
             patch.object(ctl, "stop") as stop, patch.object(ctl, "start") as start:
            with self.assertRaises(RuntimeError):
                ctl.main()
            stop.assert_not_called()
            start.assert_not_called()

    def test_valid_restart_checks_configuration_before_stop(self):
        self.write_secret()
        with patch.object(sys, "argv", ["bridgectl.py", "restart"]), \
             patch.object(ctl, "stop") as stop, patch.object(ctl, "start") as start:
            ctl.main()
            stop.assert_called_once_with(quiet=True)
            start.assert_called_once()

    def test_standalone_restart_passes_resolved_token_to_fake_watchdog(self):
        self.write_secret()
        launched = []
        real_popen = subprocess.Popen

        def fake_watchdog_only(command, **kwargs):
            if command[0] == "bash":
                return real_popen(command, **kwargs)
            self.assertEqual(command, [sys.executable, str(ctl.SCRIPT), "watchdog"])
            launched.append(kwargs)
            return SimpleNamespace(pid=424242)

        with patch.object(sys, "argv", ["bridgectl.py", "restart"]), \
             patch.object(ctl, "stop") as stop, \
             patch.object(ctl, "running_watchdog_pid", return_value=None), \
             patch.object(ctl, "running_pid", return_value=None), \
             patch.object(ctl, "listener_pid", return_value=None), \
             patch.object(ctl.subprocess, "Popen", side_effect=fake_watchdog_only):
            ctl.main()
        stop.assert_called_once_with(quiet=True)
        self.assertEqual(len(launched), 1)
        self.assertEqual(launched[0]["env"]["INFOFLOW_ISSUE_RELAY_TOKEN"], TOKEN)
        self.assertNotIn("JWT_SECRET", launched[0]["env"])
        self.assertEqual(ctl.WATCHDOG_PID_FILE.read_text(), "424242")

    def test_invalid_watchdog_config_creates_no_pid_or_child(self):
        with patch.object(ctl.signal, "signal"), patch.object(ctl, "launch") as launch:
            with self.assertRaises(RuntimeError):
                ctl.watchdog()
        self.assertFalse(ctl.WATCHDOG_PID_FILE.exists())
        launch.assert_not_called()

    def test_invalid_start_cannot_stop_listener_or_spawn_watchdog(self):
        with patch.object(ctl, "running_watchdog_pid", return_value=None), \
             patch.object(ctl, "running_pid", return_value=None), \
             patch.object(ctl, "listener_pid") as listener, patch.object(ctl, "terminate") as terminate, \
             patch.object(ctl.subprocess, "Popen") as spawn:
            # No host file means the subprocess loader is not needed here.
            self.host.unlink()
            os.environ["INFOFLOW_ISSUE_RELAY_ENABLED"] = "true"
            with self.assertRaises(RuntimeError):
                ctl.start()
            listener.assert_not_called()
            terminate.assert_not_called()
            spawn.assert_not_called()


class RelayStateIgnoreTests(unittest.TestCase):
    def test_private_runtime_state_and_atomic_temps_are_ignored(self):
        paths = [
            "infoflow-bridge/state.json.tmp",
            "infoflow-bridge/state.json.issue-group-deliveries.json",
            "infoflow-bridge/state.json.issue-group-deliveries.json.tmp",
            "infoflow-bridge/state.json.issue-group-invites.json",
            "infoflow-bridge/state.json.issue-group-invites.json.tmp",
            "custom/nested/state.issue-group-invites.json",
            "infoflow-bridge/state.json.issue-deliveries-example.json.inbox",
            "infoflow-bridge/state.json.issue-deliveries-example.json.decisions/fixture.json",
            "infoflow-bridge/state.json.issue-deliveries-example.json.decisions/fixture.json.tmp",
        ]
        result = subprocess.run(["git", "check-ignore", "--no-index", "--stdin"],
                                cwd=BRIDGE_ROOT.parent, input="\n".join(paths) + "\n",
                                text=True, capture_output=True, check=True)
        self.assertEqual(set(result.stdout.splitlines()), set(paths))

    def test_public_templates_and_tests_are_not_ignored(self):
        paths = ["deploy/secrets.env.example", "infoflow-bridge/issue-groups.test.js"]
        result = subprocess.run(["git", "check-ignore", "--no-index", "--stdin"],
                                cwd=BRIDGE_ROOT.parent, input="\n".join(paths) + "\n",
                                text=True, capture_output=True)
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout, "")


if __name__ == "__main__":
    unittest.main()
