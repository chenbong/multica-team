#!/usr/bin/env python3
"""Fail a configured Web release on missing compiled or rendered login content."""
import argparse
import html
import importlib.util
import json
from pathlib import Path
import re
from urllib.parse import unquote, urlparse


def preparation():
    spec = importlib.util.spec_from_file_location("prepare_web", Path(__file__).with_name("prepare-web.py"))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


TEXT_KEYS = (
    "MULTICA_LOGIN_TITLE", "MULTICA_LOGIN_DESCRIPTION",
    "MULTICA_VERIFICATION_BOT_NAME", "MULTICA_EMAIL_PLACEHOLDER",
)
LINK_KEYS = (
    "MULTICA_VERIFICATION_BOT_URL", "MULTICA_QUICKSTART_URL",
    "MULTICA_INFOFLOW_USER_GROUP_URL",
)


def decoded(text):
    return re.sub(r"\\u([0-9a-fA-F]{4})", lambda m: chr(int(m[1], 16)), text)


def verify_build(target, config):
    prep = preparation()
    prep.verify_output(target, config)
    output = target / "apps/web/.next"
    if not (output / "BUILD_ID").is_file():
        raise ValueError("Missing completed production build")
    missing = set(TEXT_KEYS + LINK_KEYS)
    for directory in (output / "static", output / "server"):
        for path in directory.rglob("*"):
            if path.suffix not in (".js", ".json", ".html") or not path.is_file():
                continue
            text = decoded(path.read_text(errors="replace"))
            missing = {key for key in missing if config[key] not in text}
    if missing:
        raise ValueError("Compiled Web lost configured login fields: " + ", ".join(sorted(missing)))


def normalized_url(value):
    return unquote(html.unescape(value))


def verify_rendered(snapshot, config):
    if urlparse(snapshot.get("url", "")).path.rstrip("/") != "/login":
        raise ValueError("Rendered acceptance must inspect the login route")
    text = " ".join(snapshot.get("text", "").split())
    for key in TEXT_KEYS[:-1]:
        if " ".join(config[key].split()) not in text:
            raise ValueError("Rendered login missing " + key)
    if config["MULTICA_EMAIL_PLACEHOLDER"] not in snapshot.get("placeholders", []):
        raise ValueError("Rendered login lost configured email placeholder")
    links = {normalized_url(link) for link in snapshot.get("links", [])}
    for key in LINK_KEYS:
        if normalized_url(config[key]) not in links:
            raise ValueError("Rendered login lost " + key)
    if not snapshot.get("emailLabel") or not snapshot.get("continueButton"):
        raise ValueError("Rendered login lost accessible email/continue controls")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("target", type=Path)
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--dom", type=Path, help="Visible DOM snapshot from browser acceptance; no cookies or storage")
    args = parser.parse_args()
    config = preparation().load_config(args.config)
    verify_build(args.target.resolve(), config)
    if args.dom:
        verify_rendered(json.loads(args.dom.read_text()), config)
    print(json.dumps({"compiled_login": "passed", "rendered_login": "passed" if args.dom else "not_run"}))


if __name__ == "__main__":
    main()
