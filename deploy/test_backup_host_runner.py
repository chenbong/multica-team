from pathlib import Path
import json
import os
import subprocess
import sys
import tempfile
import unittest


class HostRunnerTests(unittest.TestCase):
    def run_fixture(self,failures,*,skip=False,unverified=False,notify_failure=False):
        temporary=tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root=Path(temporary.name)
        instance=root/"instance with spaces"
        (instance/"config").mkdir(parents=True)
        (instance/"config/daily-backup.json").write_text("{}")
        state=instance/"data/daily-backup"
        state.mkdir(parents=True)
        (state/"completed.json").write_text("existing-good-backup")
        bindir=root/"bin";bindir.mkdir()
        trace=root/"trace";sleeps=root/"sleeps";count=root/"count"
        docker=bindir/"docker"
        docker.write_text("#!"+sys.executable+"\n"+'''
import json,os,sys
from pathlib import Path
trace=Path(os.environ["BACKUP_TEST_TRACE"])
with trace.open("a") as file:file.write(sys.argv[1]+"\\n")
if sys.argv[1]=="run":
 p=Path(os.environ["BACKUP_TEST_COUNT"]);n=int(p.read_text())+1 if p.exists() else 1;p.write_text(str(n))
 if n<=int(os.environ["BACKUP_TEST_FAILURES"]):sys.exit(125)
 if os.environ["BACKUP_TEST_SKIP"]=="1":print(json.dumps({"ok":True,"skipped":"already-running"}));sys.exit(0)
 print(json.dumps({"ok":True,"attempts":n,"verified":os.environ["BACKUP_TEST_UNVERIFIED"]!="1","archive_files_verified":7,"database_dump_readable":True,"bytes":1234,"completed_at":"2026-10-27T00:00:00Z"}))
if sys.argv[1]=="exec" and "notify-backup-success.mjs" in " ".join(sys.argv):
 try:
  result=json.load(sys.stdin)
  if not result.get("ok") or not result.get("verified") or not result.get("database_dump_readable"):sys.exit(7)
 except Exception:sys.exit(7)
 if os.environ["BACKUP_TEST_NOTIFY_FAILURE"]=="1":sys.exit(8)
sys.exit(0)
''')
        docker.chmod(0o700)
        for name,body in [("flock","exit 0"),("sleep",'printf "%s\\n" "$1" >> "$BACKUP_TEST_SLEEPS"')]:
            path=bindir/name;path.write_text("#!/bin/sh\n"+body+"\n");path.chmod(0o700)
        env={**os.environ,"PATH":str(bindir)+os.pathsep+os.environ["PATH"],"BACKUP_TEST_TRACE":str(trace),"BACKUP_TEST_COUNT":str(count),"BACKUP_TEST_SLEEPS":str(sleeps),"BACKUP_TEST_FAILURES":str(failures),"BACKUP_TEST_SKIP":"1" if skip else "0","BACKUP_TEST_UNVERIFIED":"1" if unverified else "0","BACKUP_TEST_NOTIFY_FAILURE":"1" if notify_failure else "0","TEAM_MESH_BACKUP_NOTIFY_USER":"fixture-operator"}
        script=Path(__file__).with_name("run-daily-bos-backup.sh")
        result=subprocess.run(["bash",str(script),str(instance),str(docker),"sha256:"+"a"*64],env=env,capture_output=True,text=True,timeout=10)
        return result,trace.read_text().splitlines(),sleeps.read_text().splitlines() if sleeps.exists() else [],state

    def test_container_launch_failures_retry_twice_then_notify_verified_success(self):
        result,trace,waits,state=self.run_fixture(2)
        self.assertEqual(result.returncode,0)
        self.assertEqual(trace.count("run"),3)
        self.assertEqual(trace.count("exec"),1)
        self.assertEqual(waits,["600","600"])
        self.assertEqual((state/"completed.json").read_text(),"existing-good-backup")

    def test_three_failures_send_exactly_one_notice_and_keep_backups(self):
        result,trace,waits,state=self.run_fixture(3)
        self.assertEqual(result.returncode,1)
        self.assertEqual(trace.count("run"),3)
        self.assertEqual(trace.count("exec"),1)
        self.assertEqual(waits,["600","600"])
        self.assertFalse(json.loads((state/"last-result.json").read_text())["ok"])
        self.assertTrue(json.loads((state/"notification-result.json").read_text())["ok"])
        self.assertEqual((state/"completed.json").read_text(),"existing-good-backup")

    def test_first_verified_success_notifies_once_without_sleep(self):
        result,trace,waits,state=self.run_fixture(0)
        self.assertEqual(result.returncode,0)
        self.assertEqual(trace,["run","exec"])
        self.assertEqual(waits,[])
        self.assertEqual(json.loads((state/"notification-result.json").read_text())["kind"],"success")

    def test_already_running_does_not_send_false_success(self):
        result,trace,waits,_=self.run_fixture(0,skip=True)
        self.assertEqual(result.returncode,0)
        self.assertEqual(trace,["run"])
        self.assertEqual(waits,[])

    def test_unverified_or_unsent_result_does_not_retry_backup(self):
        for option in ({"unverified":True},{"notify_failure":True}):
            result,trace,waits,state=self.run_fixture(0,**option)
            self.assertEqual(result.returncode,1)
            self.assertEqual(trace,["run","exec"])
            self.assertEqual(waits,[])
            self.assertFalse(json.loads((state/"notification-result.json").read_text())["ok"])
            self.assertEqual((state/"completed.json").read_text(),"existing-good-backup")


if __name__=="__main__":unittest.main()
