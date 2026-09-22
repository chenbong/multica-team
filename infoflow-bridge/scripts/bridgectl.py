#!/usr/bin/env python3
"""Manage the bridge process and keep it available after an unexpected exit.

The watchdog owns the Node process, checks the admin port after startup, and
restarts it with a bounded backoff. bridgectl stop terminates the watchdog first
so an intentional stop is not immediately undone by an automatic restart.
"""
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parent.parent
SCRIPT = Path(__file__).resolve()
PID_FILE = ROOT / "bridge.pid"
WATCHDOG_PID_FILE = ROOT / "bridge-watchdog.pid"
LOG = ROOT / "logs" / "bridge.log"
ENTRY = ROOT / "index.js"
CONFIG = ROOT / "config.local.json"
DEFAULT_PORT = 4180
STOP_TIMEOUT_SECONDS = 5.0
HEALTH_INTERVAL_SECONDS = 5.0
STARTUP_GRACE_SECONDS = 60.0
MAX_RESTART_DELAY_SECONDS = 60.0


def bridge_environment():
    env = os.environ.copy()
    host_file = ROOT.parent / "deploy" / "host.env"
    if host_file.is_file():
        # Load deployment variables for direct bridgectl invocations as well.
        result = subprocess.run(
            ["bash", "-c", 'set -a; source "$1"; env -0', "bridge-env", str(host_file)],
            env=env, capture_output=True, check=True, timeout=10,
        )
        env = dict(entry.split("=", 1) for entry in result.stdout.decode().split("\0") if "=" in entry)
    node_dir = env.get("MULTICA_NODE_BIN_DIR", "/opt/homebrew/bin")
    env["PATH"] = node_dir + ":" + env.get("PATH", "")
    config = json.loads(CONFIG.read_text()) if CONFIG.is_file() else {}
    urls = [env.get("INFOFLOW_BASE_URL", "")]
    urls += [r.get("baseUrl", "") for r in config.get("robots", [])]
    multica = config.get("multica", {})
    urls += [multica.get("serverUrl", ""), multica.get("webUrl", "")]
    urls += multica.get("serverUrls", [])
    bypass = ["localhost", "127.0.0.1", "::1"]
    bypass += [urlsplit(url).hostname for url in urls if url and urlsplit(url).hostname]
    bypass += env.get("no_proxy", "").split(",") + env.get("NO_PROXY", "").split(",")
    env["NO_PROXY"] = env["no_proxy"] = ",".join(dict.fromkeys(x for x in bypass if x))
    return env


def admin_port():
    try:
        return int((json.loads(CONFIG.read_text()).get("admin") or {}).get("port") or DEFAULT_PORT)
    except (OSError, ValueError, TypeError, AttributeError):
        return DEFAULT_PORT


def read_pid(path):
    try:
        return int(path.read_text().strip())
    except (OSError, ValueError):
        return None


def process_command(pid):
    if not pid:
        return ""
    result = subprocess.run(["ps", "-p", str(pid), "-o", "command="], capture_output=True, text=True)
    return result.stdout.strip() if result.returncode == 0 else ""


def alive(pid):
    return subprocess.run(["ps", "-p", str(pid)], capture_output=True).returncode == 0


def running_pid():
    pid = read_pid(PID_FILE)
    command = process_command(pid)
    if not command or str(ENTRY) not in command:
        return None
    return pid


def running_watchdog_pid():
    pid = read_pid(WATCHDOG_PID_FILE)
    command = process_command(pid)
    if not command or str(SCRIPT) not in command or " watchdog" not in command:
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
    watchdog = running_watchdog_pid()
    if watchdog:
        terminate(watchdog)
        stopped.append(watchdog)

    pid = running_pid()
    if pid and pid not in stopped:
        terminate(pid)
        stopped.append(pid)

    PID_FILE.unlink(missing_ok=True)
    WATCHDOG_PID_FILE.unlink(missing_ok=True)

    leftover = listener_pid(admin_port())
    if leftover and leftover not in stopped:
        terminate(leftover)
        stopped.append(leftover)

    if stopped:
        print("bridge stopped (pid " + ", ".join(str(entry) for entry in stopped) + ")")
    elif not quiet:
        print("bridge is not running")


def launch(env):
    LOG.parent.mkdir(exist_ok=True)
    with LOG.open("a") as log:
        return subprocess.Popen(
            ["node", str(ENTRY)],
            cwd=ROOT,
            env=env,
            stdin=subprocess.DEVNULL,
            stdout=log,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )


def watchdog():
    stopping = False
    child = None

    def handle_signal(_signum, _frame):
        nonlocal stopping
        stopping = True
        if child is not None and child.poll() is None:
            terminate(child.pid)

    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, handle_signal)

    WATCHDOG_PID_FILE.write_text(str(os.getpid()))
    env = bridge_environment()
    restart_delay = 1.0

    try:
        while not stopping:
            try:
                child = launch(env)
            except Exception as error:
                print("bridge launch failed: " + str(error), flush=True)
                child = None
                if stopping:
                    break
                time.sleep(restart_delay)
                restart_delay = min(MAX_RESTART_DELAY_SECONDS, restart_delay * 2)
                continue

            PID_FILE.write_text(str(child.pid))
            started_at = time.monotonic()
            exit_code = None

            while exit_code is None:
                exit_code = child.poll()
                if exit_code is not None:
                    break
                if stopping:
                    terminate(child.pid)
                    exit_code = child.wait()
                    break
                if (
                    time.monotonic() - started_at >= STARTUP_GRACE_SECONDS
                    and listener_pid(admin_port()) is None
                ):
                    print(
                        "bridge health check failed: port "
                        + str(admin_port())
                        + " is not listening; restarting",
                        flush=True,
                    )
                    terminate(child.pid)
                    exit_code = child.wait()
                    break
                time.sleep(HEALTH_INTERVAL_SECONDS)

            PID_FILE.unlink(missing_ok=True)
            child = None
            if stopping:
                break

            print(
                "bridge exited with status "
                + str(exit_code)
                + "; restarting in "
                + str(restart_delay)
                + "s",
                flush=True,
            )
            time.sleep(restart_delay)
            restart_delay = min(MAX_RESTART_DELAY_SECONDS, restart_delay * 2)
    finally:
        if child is not None and child.poll() is None:
            terminate(child.pid)
        PID_FILE.unlink(missing_ok=True)
        WATCHDOG_PID_FILE.unlink(missing_ok=True)


def start():
    watchdog_pid = running_watchdog_pid()
    if watchdog_pid:
        print(f"bridge watchdog already running (pid {watchdog_pid})")
        return

    existing = running_pid()
    if existing:
        print(f"bridge already running (pid {existing})")
        return

    lingering = listener_pid(admin_port())
    if lingering:
        print(f"port {admin_port()} is still held by pid {lingering}; stopping it before start")
        terminate(lingering)

    LOG.parent.mkdir(exist_ok=True)
    env = bridge_environment()
    process = subprocess.Popen(
        [sys.executable, str(SCRIPT), "watchdog"],
        cwd=ROOT,
        env=env,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
    )
    WATCHDOG_PID_FILE.write_text(str(process.pid))
    print(f"bridge watchdog started (pid {process.pid}), logs: {LOG}")


def status():
    watchdog_pid = running_watchdog_pid()
    bridge_pid = running_pid() or listener_pid(admin_port())
    if watchdog_pid:
        print(
            "bridge watchdog running (pid "
            + str(watchdog_pid)
            + "), bridge pid "
            + str(bridge_pid or "starting")
        )
    elif bridge_pid:
        print(f"bridge running (pid {bridge_pid})")
    else:
        print("bridge is not running")


action = sys.argv[1] if len(sys.argv) > 1 else "status"
if action == "start":
    start()
elif action == "stop":
    stop()
elif action == "restart":
    stop(quiet=True)
    start()
elif action == "watchdog":
    watchdog()
else:
    status()
