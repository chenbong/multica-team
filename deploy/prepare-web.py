#!/usr/bin/env python3
"""Apply frontend overlays using one explicit, machine-local build manifest."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1]
KEYS = (
    "MULTICA_BRAND_NAME", "MULTICA_BRAND_BRIDGE_URL", "MULTICA_LOGIN_TITLE",
    "MULTICA_LOGIN_DESCRIPTION", "MULTICA_EMAIL_PLACEHOLDER",
    "MULTICA_VERIFICATION_BOT_NAME", "MULTICA_VERIFICATION_BOT_URL",
    "MULTICA_CLI_INSTALL_URL", "MULTICA_CLI_INSTALL_WINDOWS_URL",
    "MULTICA_INFOFLOW_BRIDGE_URL", "MULTICA_INFOFLOW_BRIDGE_LABEL",
    "MULTICA_DAEMON_RUNTIME_DEFAULTS", "MULTICA_DAEMON_RUNTIME_SHIM_DEFAULTS",
    "MULTICA_ONEAPI_TRACE_URL", "MULTICA_QUICKSTART_URL", "MULTICA_INFOFLOW_USER_GROUP_URL",
)
REQUIRED = tuple(key for key in KEYS if key not in (
    "MULTICA_BRAND_BRIDGE_URL", "MULTICA_CLI_INSTALL_WINDOWS_URL",
))


def load_config(path):
    config = json.loads(Path(path).read_text())
    if not isinstance(config, dict) or set(config) - set(KEYS):
        raise ValueError("Unknown frontend build configuration keys")
    if any(not isinstance(value, str) or any(c in value for c in '\n\r\0"\\')
           for value in config.values()):
        raise ValueError("Frontend build values must be single-line strings")
    missing = [key for key in REQUIRED if not config.get(key, "").strip()]
    if missing:
        raise ValueError("Missing frontend build configuration: " + ", ".join(missing))
    return config


def verify_output(target, config):
    auth = json.loads((target / "packages/views/locales/zh-Hans/auth.json").read_text())
    for section, field, key in (
        ("signin", "title", "MULTICA_BRAND_NAME"),
        ("signin", "description", "MULTICA_LOGIN_DESCRIPTION"),
        ("common", "email_placeholder", "MULTICA_EMAIL_PLACEHOLDER"),
        ("verify", "title_link", "MULTICA_VERIFICATION_BOT_NAME"),
    ):
        if auth[section][field] != config[key]:
            raise ValueError(f"Generated frontend configuration mismatch: {key}")
    page = (target / "packages/views/auth/login-page.tsx").read_text()
    if config["MULTICA_VERIFICATION_BOT_URL"] not in page or "$.signin.verification_bot_hint" not in page or "$.verify.title_link" not in page:
        raise ValueError("Generated login page lost verification robot instructions")
    for key in ("MULTICA_QUICKSTART_URL", "MULTICA_INFOFLOW_USER_GROUP_URL"):
        if config[key] not in page:
            raise ValueError(f"Generated login page lost {key}")
    sidebar = (target / "packages/views/layout/app-sidebar.tsx").read_text()
    for key in ("MULTICA_ONEAPI_TRACE_URL", "MULTICA_QUICKSTART_URL", "MULTICA_INFOFLOW_USER_GROUP_URL"):
        if config[key] not in sidebar:
            raise ValueError(f"Generated sidebar lost {key}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("target", type=Path)
    parser.add_argument("--config", required=True, type=Path)
    args = parser.parse_args()
    config = load_config(args.config)  # Validate before making any changes.
    target = args.target.resolve()
    if (target / ".git").exists() or not (target / "apps/web/package.json").exists():
        raise ValueError("Use a separate generated frontend tree, not a checkout")
    env = {key: value for key, value in os.environ.items() if key not in KEYS}
    env.update(config)
    env.update(MULTICA_WEB_TARGET=str(target), MULTICA_WEB_BACKUP=str(target / ".overlay-backup"))
    shutil.copytree(ROOT / "overlays/web-direct", target, dirs_exist_ok=True)
    subprocess.run(["python3", str(ROOT / "overlays/web-patch.py"), "apply"], env=env, check=True)
    subprocess.run(["node", str(ROOT / "deploy/apply-brand.cjs"), str(target)], env=env, check=True)
    verify_output(target, config)
    print("Frontend build configuration and generated login instructions verified.")


if __name__ == "__main__":
    main()
