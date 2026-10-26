from pathlib import Path
import json
import os
import subprocess
import sys
import tempfile
import unittest


class HostRunnerTests(unittest.TestCase):
    def run_fixture(self,failures):
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
import os,sys
from pathlib import Path
trace=Path(os.environ["BACKUP_TEST_TRACE"])
with trace.open("a") as file:file.write(sys.argv[1]+"\\n")
if sys.argv[1]=="run":
 p=Path(os.environ["BACKUP_TEST_COUNT"]);n=int(p.read_text())+1 if p.exists() else 1;p.write_text(str(n))
 sys.exit(125 if n<=int(os.environ["BACKUP_TEST_FAILURES"]) else 0)
sys.exit(0)
''')
        docker.chmod(0o700)
        for name,body in [("flock","exit 0"),("sleep",'printf "%s\\n" "$1" >> "$BACKUP_TEST_SLEEPS"')]:
            path=bindir/name;path.write_text("#!/bin/sh\n"+body+"\n");path.chmod(0o700)
        env={**os.environ,"PATH":str(bindir)+os.pathsep+os.environ["PATH"],"BACKUP_TEST_TRACE":str(trace),"BACKUP_TEST_COUNT":str(count),"BACKUP_TEST_SLEEPS":str(sleeps),"BACKUP_TEST_FAILURES":str(failures),"TEAM_MESH_BACKUP_NOTIFY_USER":"fixture-operator"}
        script=Path(__file__).with_name("run-daily-bos-backup.sh")
        result=subprocess.run(["bash",str(script),str(instance),str(docker),"sha256:"+"a"*64],env=env,capture_output=True,text=True,timeout=10)
        return result,trace.read_text().splitlines(),sleeps.read_text().splitlines() if sleeps.exists() else [],state

    def test_container_launch_failures_retry_twice_then_succeed_without_notice(self):
        result,trace,waits,state=self.run_fixture(2)
        self.assertEqual(result.returncode,0)
        self.assertEqual(trace.count("run"),3)
        self.assertEqual(trace.count("exec"),0)
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

    def test_first_success_never_sleeps_or_notifies(self):
        result,trace,waits,_=self.run_fixture(0)
        self.assertEqual(result.returncode,0)
        self.assertEqual(trace,["run"])
        self.assertEqual(waits,[])


if __name__=="__main__":unittest.main()
