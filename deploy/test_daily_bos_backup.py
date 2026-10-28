import importlib.util
import json
from datetime import datetime, timedelta, timezone
from pathlib import Path
import tempfile
import os
import tarfile
import unittest
from unittest.mock import patch

spec=importlib.util.spec_from_file_location("daily_backup",Path(__file__).with_name("daily-bos-backup.py"))
backup=importlib.util.module_from_spec(spec)
spec.loader.exec_module(backup)


class BackupTests(unittest.TestCase):
    @staticmethod
    def write_snapshot(directory):
        for name in ("database.dump", "globals.sql", "state.tar.gz", "source.tar.gz",
                     "web-source.tar.gz", "bridge-source.tar.gz", "containers.json"):
            (directory/name).write_bytes(("fixture " + name).encode())
        backup.record(directory/"manifest.json", {"files": {
            file.name: backup.sha256(file) for file in directory.iterdir() if file.is_file()
        }})

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

    def test_standalone_success_notifies_once_after_verification(self):
        notices=[]
        verified={"verified":True,"archive_files_verified":7,"database_dump_readable":True}
        result=backup.execute({},operation=lambda _:verified,sleep=lambda _:None,
                              notify_success=lambda outcome:notices.append(outcome.copy()))
        self.assertTrue(result["notification_ok"])
        self.assertEqual(len(notices),1)
        self.assertEqual(notices[0]["archive_files_verified"],7)

    def test_standalone_notification_failure_does_not_retry_backup(self):
        operations=[]
        def operation(_):operations.append(1);return {"verified":True}
        def notice(_):raise RuntimeError("private transport failure")
        result=backup.execute({},operation=operation,sleep=lambda _:None,notify_success=notice)
        self.assertTrue(result["ok"])
        self.assertFalse(result["notification_ok"])
        self.assertEqual(operations,[1])

    def test_success_notice_reuses_configured_private_recipient_via_stdin(self):
        config={"notify_command":["/usr/local/bin/docker","exec","team-mesh-bridge","node",
                                  "/team/code/multica-team/deploy/notify-backup-failure.mjs","fixture-user"]}
        outcome={"ok":True,"verified":True,"bytes":123}
        with patch.object(backup,"run") as run:
            backup.notify_verified_success(config,outcome)
        args=run.call_args.args[0]
        self.assertEqual(args[1:5],["exec","-i","team-mesh-bridge","node"])
        self.assertTrue(args[5].endswith("/notify-backup-success.mjs"))
        self.assertEqual(args[6],"fixture-user")
        self.assertEqual(json.loads(run.call_args.kwargs["input_data"]),outcome)

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
            base=Path(temp)/"instance";(base/"config").mkdir(parents=True)
            config={"base":str(base),"bucket":"fixture-bucket","prefix":"platform-backups","docker":"/bin/docker","bos_cli":"/bin/bcecmd","bos_config":"/protected/profile","database":"fixture","endpoint":"bj.bcebos.com"}
            remote={}
            def fake_capture(_config,directory):
                self.write_snapshot(directory)
            def fake_bos(_config,operation,source,target=None,*_):
                if operation=="object-acl":return
                self.assertEqual(operation,"cp")
                if source.startswith("bos:/"):Path(target).write_bytes(remote[source])
                else:remote[target]=Path(source).read_bytes()
            real_run=backup.run
            def run_or_verify(command,**kwargs):
                if "pg_restore" in command:
                    self.assertIn("--file=/dev/null",command)
                    return
                return real_run(command,**kwargs)
            with patch.dict(os.environ,{"COPYFILE_DISABLE":"1"}),patch.object(backup,"private_preflight"),patch.object(backup,"verify_private"),patch.object(backup,"capture",side_effect=fake_capture),patch.object(backup,"bos",side_effect=fake_bos),patch.object(backup,"run",side_effect=run_or_verify),patch.object(backup,"prune",side_effect=lambda _c,entries,_n:entries) as prune:
                result=backup.backup(config)
                self.assertTrue(result["verified"])
                self.assertEqual(result["archive_files_verified"],7)
                self.assertTrue(result["database_dump_readable"])
                prune.assert_called_once()
                self.assertEqual(len(remote),1)
            self.assertTrue((base/"data/daily-backup/completed.json").exists())

    def test_downloaded_archive_must_match_every_manifest_hash(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp);snapshot=root/"snapshot";snapshot.mkdir();self.write_snapshot(snapshot)
            (snapshot/"state.tar.gz").write_bytes(b"tampered after manifest")
            archive=root/"backup.tar.gz"
            with tarfile.open(archive,"w:gz") as bundle:
                bundle.add(snapshot,arcname=".")
            with patch.object(backup,"run") as restore:
                with self.assertRaisesRegex(ValueError,"component checksum"):
                    backup.verify_downloaded_archive({"docker":"/bin/docker"},archive,root)
                restore.assert_not_called()

    def test_database_decode_failure_prevents_verified_result(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp);snapshot=root/"snapshot";snapshot.mkdir();self.write_snapshot(snapshot)
            archive=root/"backup.tar.gz"
            with tarfile.open(archive,"w:gz") as bundle:
                bundle.add(snapshot,arcname=".")
            with patch.object(backup,"run",side_effect=RuntimeError("decode failed")):
                with self.assertRaisesRegex(RuntimeError,"decode failed"):
                    backup.verify_downloaded_archive({"docker":"/bin/docker"},archive,root)

    def test_failed_verification_never_prunes(self):
        with tempfile.TemporaryDirectory() as temp:
            base=Path(temp)/"instance";(base/"config").mkdir(parents=True)
            config={"base":str(base),"bucket":"fixture-bucket","prefix":"platform-backups","docker":"/bin/docker","bos_cli":"/bin/bcecmd","bos_config":"/protected/profile","database":"fixture","endpoint":"bj.bcebos.com"}
            def fake_bos(_config,_op,source,target=None,*_):
                if _op=="object-acl":return
                if source.startswith("bos:/"):Path(target).write_bytes(b"corrupt")
            with patch.object(backup,"private_preflight"),patch.object(backup,"verify_private"),patch.object(backup,"capture"),patch.object(backup,"bos",side_effect=fake_bos),patch.object(backup,"prune") as prune:
                with self.assertRaises(ValueError):backup.backup(config)
                prune.assert_not_called()
            self.assertFalse((base/"data/daily-backup/completed.json").exists())


if __name__=="__main__":unittest.main()
