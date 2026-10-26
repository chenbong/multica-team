import importlib.util
from datetime import datetime, timedelta, timezone
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec=importlib.util.spec_from_file_location("daily_backup",Path(__file__).with_name("daily-bos-backup.py"))
backup=importlib.util.module_from_spec(spec)
spec.loader.exec_module(backup)


class BackupTests(unittest.TestCase):
    def test_host_attempt_has_no_nested_retries(self):
        calls=[]
        def fail(_):calls.append(1);raise RuntimeError()
        self.assertEqual(backup.execute_attempt({},2,operation=fail),{"ok":False,"attempts":2,"failed_stage":"backup"})
        self.assertEqual(len(calls),1)
    def test_retries_and_notifies_only_after_final_failure(self):
        attempts=[];waits=[];notices=[]
        def fail(_):
            attempts.append(1)
            raise RuntimeError("must not print private diagnostics")
        result=backup.execute({},operation=fail,sleep=waits.append,notify=lambda:notices.append(1))
        self.assertEqual(result,{"ok":False,"attempts":3,"failed_stage":"backup"})
        self.assertEqual(len(attempts),3)
        self.assertEqual(waits,[600,600])
        self.assertEqual(notices,[1])

    def test_success_after_retry_does_not_notify(self):
        attempts=[];notices=[]
        def operation(_):
            attempts.append(1)
            if len(attempts)==1:raise RuntimeError()
            return {"verified":True}
        result=backup.execute({},operation=operation,sleep=lambda _:None,notify=lambda:notices.append(1))
        self.assertTrue(result["ok"])
        self.assertEqual(result["attempts"],2)
        self.assertEqual(notices,[])

    def test_retention_only_deletes_owned_expired_objects(self):
        now=datetime(2026,10,22,tzinfo=timezone.utc)
        config={"prefix":"platform-backups","bucket":"fixture-bucket"}
        def item(age):
            stamp=now-timedelta(days=age)
            return {"key":"platform-backups/"+stamp.date().isoformat()+"/backup-"+stamp.strftime("%Y-%m-%dT%H-%M-%SZ")+"-abcdef123456.tar.gz","created_at":stamp.isoformat()}
        old,keep,boundary=item(31),item(1),item(30)
        with patch.object(backup,"bos") as bos:
            remaining=backup.prune(config,[old,keep,boundary],now)
            self.assertEqual(remaining,[keep,boundary])
            self.assertEqual(bos.call_count,1)
            self.assertIn(old["key"],bos.call_args.args[2])
        with patch.object(backup,"bos") as bos:
            with self.assertRaises(ValueError):
                backup.prune(config,[{"key":"reply-images/old.tar.gz","created_at":old["created_at"]}],now)
            bos.assert_not_called()

    def test_full_capture_round_trip_before_retention(self):
        with tempfile.TemporaryDirectory() as temp:
            base=Path(temp);(base/"config").mkdir()
            config={"base":str(base),"bucket":"fixture-bucket","prefix":"platform-backups","docker":"/bin/docker","bos_cli":"/bin/bcecmd","bos_config":"/protected/profile","database":"fixture","endpoint":"bj.bcebos.com"}
            remote={}
            def fake_capture(_config,directory):
                (directory/"database.dump").write_bytes(b"fixture dump")
                (directory/"state.tar.gz").write_bytes(b"fixture files")
            def fake_bos(_config,operation,source,target=None,*_):
                if operation=="object-acl":return
                self.assertEqual(operation,"cp")
                if source.startswith("bos:/"):Path(target).write_bytes(remote[source])
                else:remote[target]=Path(source).read_bytes()
            with patch.object(backup,"private_preflight"),patch.object(backup,"verify_private"),patch.object(backup,"capture",side_effect=fake_capture),patch.object(backup,"bos",side_effect=fake_bos),patch.object(backup,"prune",side_effect=lambda _c,entries,_n:entries) as prune:
                result=backup.backup(config)
                self.assertTrue(result["verified"])
                prune.assert_called_once()
                self.assertEqual(len(remote),1)
            self.assertTrue((base/"data/daily-backup/completed.json").exists())

    def test_failed_verification_never_prunes(self):
        with tempfile.TemporaryDirectory() as temp:
            base=Path(temp);(base/"config").mkdir()
            config={"base":str(base),"bucket":"fixture-bucket","prefix":"platform-backups","docker":"/bin/docker","bos_cli":"/bin/bcecmd","bos_config":"/protected/profile","database":"fixture","endpoint":"bj.bcebos.com"}
            def fake_bos(_config,_op,source,target=None,*_):
                if source.startswith("bos:/"):Path(target).write_bytes(b"corrupt")
            with patch.object(backup,"private_preflight"),patch.object(backup,"verify_private"),patch.object(backup,"capture"),patch.object(backup,"bos",side_effect=fake_bos),patch.object(backup,"prune") as prune:
                with self.assertRaises(ValueError):backup.backup(config)
                prune.assert_not_called()
            self.assertFalse((base/"data/daily-backup/completed.json").exists())


if __name__=="__main__":unittest.main()
