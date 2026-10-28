#!/usr/bin/env python3
"""Host-owned complete private BOS backups. Configuration must be mode 0600."""
import argparse
from datetime import datetime, timedelta, timezone
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import tarfile
import tempfile
import time
import uuid
import urllib.error
import urllib.request
import urllib.parse
import ssl


def sha256(path):
    digest = hashlib.sha256()
    with open(path, "rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def run(command, *, output=None, env=None, timeout=3600, input_data=None):
    # External diagnostics can contain credentials or signed URLs. Only report
    # our own fixed stage labels; protected source data never reaches the journal.
    subprocess.run(command, check=True, input=input_data, stdout=output or subprocess.DEVNULL,
                   stderr=subprocess.DEVNULL, env=env, timeout=timeout)


def validate_config(config):
    base = Path(config["base"]).resolve()
    if not base.is_absolute() or len(base.parts) < 4 or not (base / "config").is_dir():
        raise ValueError("Invalid instance base")
    if not re.fullmatch(r"[a-z0-9][a-z0-9.-]{2,62}", config["bucket"]):
        raise ValueError("Invalid bucket")
    if not re.fullmatch(r"[a-z0-9][a-z0-9/-]{2,100}", config["prefix"]) or ".." in config["prefix"] or config["prefix"].endswith("/"):
        raise ValueError("Invalid owned backup prefix")
    for key in ("docker", "bos_cli", "bos_config"):
        if not Path(config[key]).is_absolute():
            raise ValueError("Backup tool paths must be absolute")
    if not re.fullmatch(r"[A-Za-z0-9_-]+", config["database"]):
        raise ValueError("Invalid database name")
    if not re.fullmatch(r"[a-z0-9-]+\.bcebos\.com",config["endpoint"]):
        raise ValueError("Invalid BOS endpoint")
    return base


def bos(config, *args):
    env = os.environ.copy()
    if config.get("ca_file"):
        env["SSL_CERT_FILE"] = config["ca_file"]
        env["REQUESTS_CA_BUNDLE"] = config["ca_file"]
    run([config["bos_cli"], "--conf-path", config["bos_config"], "bos", *args], env=env)


def verify_private(config,key):
    url="https://"+config["bucket"]+"."+config["endpoint"]+"/"+urllib.parse.quote(key,safe="/")
    try:
        with urllib.request.urlopen(urllib.request.Request(url,method="HEAD"),context=ssl.create_default_context(cafile=config.get("ca_file")),timeout=30):
            raise ValueError("Backup object allows anonymous access")
    except urllib.error.HTTPError as error:
        if error.code not in (401,403):
            raise ValueError("Unable to verify private backup access")


def private_preflight(config,directory):
    key=config["prefix"]+"/private-probe-"+uuid.uuid4().hex
    remote="bos:/"+config["bucket"]+"/"+key
    probe=directory/"private-probe.txt"
    probe.write_text("Team Mesh private backup access verification. No sensitive content.")
    bos(config,"cp",str(probe),remote,"--quiet","--yes","--disable-bar")
    try:
        verify_private(config,key)
    finally:
        bos(config,"rm",remote,"--yes")
        probe.unlink()


def prune(config, entries, now):
    cutoff = now - timedelta(days=30)
    remaining = []
    exact = re.compile(re.escape(config["prefix"]) + r"/\d{4}-\d{2}-\d{2}/backup-[0-9TZA-Fa-f-]+\.tar\.gz")
    for item in entries:
        # The ledger is the allowlist: never recursively delete a bucket/prefix,
        # and never delete images or objects merely resembling a backup.
        if not exact.fullmatch(item.get("key", "")):
            raise ValueError("Invalid backup ledger object")
        if datetime.fromisoformat(item["created_at"]) >= cutoff:
            remaining.append(item)
            continue
        bos(config, "rm", "bos:/" + config["bucket"] + "/" + item["key"], "--yes")
    return remaining


def record(path, value):
    temp = path.with_suffix(".tmp")
    with open(temp, "w") as stream:
        os.chmod(temp, 0o600)
        json.dump(value, stream)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temp, path)


def capture(config, directory):
    base = Path(config["base"]).resolve()
    docker = config["docker"]
    container = config["postgres_container"]
    with open(directory / "database.dump", "wb") as out:
        run([docker, "exec", container, "sh", "-ec",
             'exec pg_dump -U "$POSTGRES_USER" -d "$1" -Fc', "backup", config["database"]], output=out)
    with open(directory / "globals.sql", "wb") as out:
        run([docker, "exec", container, "sh", "-ec",
             'exec pg_dumpall -U "$POSTGRES_USER" --globals-only'], output=out)
    # Validate the actual dump, without mounting unknown host paths or restoring
    # over a live database.
    # Older Docker clients fail when pg_restore --list closes stdin after
    # reading only the TOC. Read a mounted dump file instead of streaming it.
    run([docker,"run","--rm","--network","none","--log-driver","none",
         "-v",str(directory)+":/snapshot:ro",config.get("postgres_tools_image","postgres:16-alpine"),
         "pg_restore","--list","/snapshot/database.dump"],timeout=180)
    paths = ["config", "services", "runtime/bin", "data/uploads", "data/home", "data/bridge"]
    for relative in paths:
        if not (base / relative).exists():
            raise ValueError("Required backup directory missing")
    run(["tar","--exclude=task_progress","-czf",str(directory / "state.tar.gz"),"-C",str(base),*paths])
    for name, relative in (("source","code/multica-team"),("web-source","code/web"),("bridge-source","code/bridge")):
        source = (base / relative).resolve(strict=True)
        if base not in source.parents:
            raise ValueError("Code source outside instance")
        run(["tar","--exclude=node_modules","--exclude=.next","--exclude=.git","--exclude=build","--exclude=__pycache__",
             "-czf",str(directory / (name+".tar.gz")),"-C",str(source),"."])
    with open(directory / "containers.json","wb") as out:
        run([docker,"inspect","team-mesh-api","team-mesh-web","team-mesh-bridge","team-mesh-postgres"],output=out,timeout=60)
    metadata = {"format":1,"created_at":datetime.now(timezone.utc).isoformat(),
                "database":config["database"],"links":{p:os.readlink(base/p) for p in ("code/multica-team","code/web","code/bridge")},
                "files":{path.name:sha256(path) for path in directory.iterdir() if path.is_file()}}
    record(directory / "manifest.json", metadata)


def verify_downloaded_archive(config, archive, directory):
    """Read the downloaded archive fully and validate its recorded components."""
    expected = {"database.dump", "globals.sql", "state.tar.gz", "source.tar.gz",
                "web-source.tar.gz", "bridge-source.tar.gz", "containers.json", "manifest.json"}
    seen = set()
    hashes = {}
    manifest = None
    verified_dir = directory / "verified-components"
    verified_dir.mkdir(mode=0o700)
    with tarfile.open(archive, mode="r|gz") as bundle:
        for member in bundle:
            name = member.name[2:] if member.name.startswith("./") else member.name
            if name in ("", ".") and member.isdir():
                continue
            if not member.isfile() or name not in expected or name in seen:
                raise ValueError("Unexpected backup archive entry")
            seen.add(name)
            source = bundle.extractfile(member)
            if source is None:
                raise ValueError("Unreadable backup archive entry")
            if name == "manifest.json":
                if member.size > 1024 * 1024:
                    raise ValueError("Backup manifest exceeds limit")
                manifest = json.loads(source.read().decode("utf-8"))
                continue
            digest = hashlib.sha256()
            total = 0
            dump = open(verified_dir / "database.dump", "xb") if name == "database.dump" else None
            try:
                for block in iter(lambda: source.read(1024 * 1024), b""):
                    total += len(block)
                    digest.update(block)
                    if dump is not None:
                        dump.write(block)
            finally:
                if dump is not None:
                    dump.close()
            if total != member.size:
                raise ValueError("Incomplete backup archive entry")
            hashes[name] = digest.hexdigest()
    if seen != expected or not isinstance(manifest, dict) or manifest.get("files") != hashes:
        raise ValueError("Backup component checksum mismatch")
    # Decode the downloaded database dump completely, not just its table of
    # contents. This checks readability without restoring into the live DB.
    run([config["docker"], "run", "--rm", "--network", "none", "--log-driver", "none",
         "-v", str(verified_dir) + ":/snapshot:ro",
         config.get("postgres_tools_image", "postgres:16-alpine"),
         "pg_restore", "--no-owner", "--file=/dev/null", "/snapshot/database.dump"], timeout=1800)
    return {"archive_files_verified": len(hashes), "database_dump_readable": True}


def backup(config):
    base = validate_config(config)
    work = base / "data/daily-backup"
    work.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(work,0o700)
    ledger = work / "completed.json"
    entries = json.loads(ledger.read_text()) if ledger.exists() else []
    now = datetime.now(timezone.utc)
    local_day = (now + timedelta(hours=8)).date().isoformat()
    stamp = now.strftime("%Y-%m-%dT%H-%M-%SZ") + "-" + uuid.uuid4().hex[:12]
    key = config["prefix"] + "/" + local_day + "/backup-" + stamp + ".tar.gz"
    with tempfile.TemporaryDirectory(prefix="snapshot-",dir=work) as temporary:
        directory=Path(temporary)
        private_preflight(config,directory)
        snapshot=directory/"snapshot"
        snapshot.mkdir(mode=0o700)
        capture(config,snapshot)
        archive=directory/"backup.tar.gz"
        run(["tar","-czf",str(archive),"-C",str(snapshot),"."])
        digest=sha256(archive)
        remote="bos:/"+config["bucket"]+"/"+key
        bos(config,"cp",str(archive),remote,"--disable-bar","--quiet","--yes")
        bos(config,"object-acl",remote,"--method","put","--canned","private")
        verify_private(config,key)
        verified=directory/"verified.tar.gz"
        bos(config,"cp",remote,str(verified),"--disable-bar","--quiet","--yes")
        if sha256(verified)!=digest:
            raise ValueError("BOS round-trip checksum mismatch")
        checks = verify_downloaded_archive(config, verified, directory)
        # Persist the successful object before retention so pruning failure
        # cannot orphan the new backup from the owned-object ledger.
        entries.append({"key":key,"created_at":now.isoformat(),"sha256":digest,"bytes":archive.stat().st_size})
        record(ledger,entries)
        entries=prune(config,entries,now)
        record(ledger,entries)
        return {"completed_at":datetime.now(timezone.utc).isoformat(),"key":key,"sha256":digest,
                "bytes":archive.stat().st_size,"verified":True,**checks}


def notify_verified_success(config, result):
    """Use the existing operator/robot target, with verified facts on stdin."""
    current = config.get("notify_command")
    if (not isinstance(current, list) or len(current) != 6 or
            current[1:4] != ["exec", "team-mesh-bridge", "node"] or
            not isinstance(current[4], str) or
            not current[4].endswith("/notify-backup-failure.mjs")):
        raise ValueError("Backup notification target is not configured")
    script = current[4][:-len("notify-backup-failure.mjs")] + "notify-backup-success.mjs"
    command = [current[0], "exec", "-i", current[2], current[3], script, current[5]]
    run(command, timeout=60, input_data=json.dumps(result).encode("utf-8"))


def execute(config, *, sleep=time.sleep, operation=backup, notify=None, notify_success=None):
    last_stage="backup"
    for attempt in range(3):
        try:
            result=operation(config)
        except Exception:
            # No exception text: subprocess output and storage URLs can carry secrets.
            if attempt<2:sleep(600)
            continue
        outcome={"ok":True,"attempts":attempt+1,**result}
        if notify_success:
            try:
                notify_success(outcome)
                outcome["notification_ok"]=True
            except Exception:
                # The completed backup remains successful; don't make another.
                outcome["notification_ok"]=False
        return outcome
    if notify:
        try:notify()
        except Exception:last_stage="backup-and-notification"
    return {"ok":False,"attempts":3,"failed_stage":last_stage}


def execute_attempt(config,attempt,operation=backup):
    try:return {"ok":True,"attempts":attempt,**operation(config)}
    except Exception:return {"ok":False,"attempts":attempt,"failed_stage":"backup"}


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config",required=True,type=Path)
    parser.add_argument("--attempt",type=int,choices=(1,2,3),help="Host-runner attempt; disables nested retries and notification")
    args=parser.parse_args()
    if args.config.is_symlink() or args.config.stat().st_mode & 0o077:
        raise ValueError("Backup configuration must be private")
    config=json.loads(args.config.read_text())
    base=validate_config(config)
    state=base/"data/daily-backup"
    state.mkdir(mode=0o700,parents=True,exist_ok=True)
    with open(state/"backup.lock","a") as lock:
        try:fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
        except BlockingIOError:
            print('{"ok":true,"skipped":"already-running"}')
            return 0
        result=execute_attempt(config,args.attempt) if args.attempt else execute(
            config,notify=lambda:run(config["notify_command"],timeout=60),
            notify_success=lambda outcome:notify_verified_success(config,outcome))
        record(state/"last-result.json",result)
        with open(state/"runs.jsonl","a") as history:
            history.write(json.dumps(result)+"\n")
        print(json.dumps(result))
        return 0 if result["ok"] and result.get("notification_ok",True) else 1


if __name__=="__main__":
    os.umask(0o077)
    def terminate(signum,_frame):
        raise SystemExit(128+signum)
    signal.signal(signal.SIGTERM,terminate)
    signal.signal(signal.SIGINT,terminate)
    try:raise SystemExit(main())
    except Exception:
        print('{"ok":false,"failed_stage":"configuration"}')
        raise SystemExit(1)
