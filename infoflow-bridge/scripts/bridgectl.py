#!/usr/bin/env python3
"""Starts, stops, restarts, or reports the bridge process.

The child is put in its own session so it survives the terminal (or agent
shell) that launched it; a plain `nohup ... &` still shares the process group
and dies when that group is torn down.

start/stop also look at whatever listens on the configured admin port, so a
process started through another path (an older deploy script, a manual
`node index.js`) cannot be left running stale code behind a restart that only
looked successful.
"""
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parent.parent
PID_FILE = ROOT / "bridge.pid"
LOG = ROOT / "logs" / "bridge.log"
ENTRY = ROOT / "index.js"
CONFIG = ROOT / "config.local.json"
DEFAULT_PORT = 4180
STOP_TIMEOUT_SECONDS = 5.0


def admin_port():
    try:
        return int((json.loads(CONFIG.read_text()).get("admin") or {}).get("port") or DEFAULT_PORT)
    except (OSError, ValueError, TypeError, AttributeError):
        return DEFAULT_PORT


def alive(pid):
    return subprocess.run(["ps", "-p", str(pid)], capture_output=True).returncode == 0


def running_pid():
    try:
        pid = int(PID_FILE.read_text().strip())
    except (OSError, ValueError):
        return None
    result = subprocess.run(["ps", "-p", str(pid), "-o", "command="], capture_output=True, text=True)
    if result.returncode != 0 or str(ENTRY) not in result.stdout:
        return None
    return pid


def listener_pid(port):
    for command in (["lsof", "-ti", f"tcp:{port}", "-sTCP:LISTEN"], ["ss", "-ltnpH", f"sport = :{port}"]):
        try:
            result = subprocess.run(command, capture_output=True, text=True)
        except FileNotFoundError:
            continue
        if result.returncode != 0:
            continue
        if command[0] == "lsof":
            for token in result.stdout.split():
                if token.isdigit():
                    return int(token)
        else:
            match = re.search(r"pid=(\d+)", result.stdout)
            if match:
                return int(match.group(1))
    return None


def signal_process(pid, sig):
    try:
        os.killpg(pid, sig)
        return True
    except OSError:
        try:
            os.kill(pid, sig)
            return True
        except OSError:
            return False


def terminate(pid):
    if not signal_process(pid, signal.SIGTERM):
        return
    deadline = time.time() + STOP_TIMEOUT_SECONDS
    while time.time() < deadline:
        if not alive(pid):
            return
        time.sleep(0.2)
    signal_process(pid, signal.SIGKILL)
    deadline = time.time() + STOP_TIMEOUT_SECONDS
    while time.time() < deadline and alive(pid):
        time.sleep(0.2)


def stop(quiet=False):
    stopped = []
    pid = running_pid()
    if pid:
        terminate(pid)
        stopped.append(pid)
    PID_FILE.unlink(missing_ok=True)
    leftover = listener_pid(admin_port())
    if leftover and leftover not in stopped:
        terminate(leftover)
        stopped.append(leftover)
    if stopped:
        print("bridge stopped (pid " + ", ".join(str(entry) for entry in stopped) + ")")
    elif not quiet:
        print("bridge is not running")


def start():
    existing = running_pid()
    if existing:
        print(f"bridge already running (pid {existing})")
        return
    lingering = listener_pid(admin_port())
    if lingering:
        print(f"port {admin_port()} is still held by pid {lingering}; stopping it before start")
        terminate(lingering)
    LOG.parent.mkdir(exist_ok=True)
    env = os.environ.copy()
    env["PATH"] = "/opt/homebrew/bin:" + env.get("PATH", "")
    with LOG.open("a") as log:
        process = subprocess.Popen(
            ["node", str(ENTRY)],
            cwd=ROOT,
            env=env,
            stdin=subprocess.DEVNULL,
            stdout=log,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
    PID_FILE.write_text(f"{process.pid}\n")
    print(f"bridge started (pid {process.pid}), logs: {LOG}")


action = sys.argv[1] if len(sys.argv) > 1 else "status"
if action == "start":
    start()
elif action == "stop":
    stop()
elif action == "restart":
    stop(quiet=True)
    start()
else:
    pid = running_pid() or listener_pid(admin_port())
    print(f"bridge running (pid {pid})" if pid else "bridge is not running")
