import {test} from "node:test";
import assert from "node:assert/strict";
import {backupSuccessNotice} from "./backup-success-notice.mjs";

const verified={ok:true,attempts:1,verified:true,archive_files_verified:7,database_dump_readable:true,bytes:3221225472,completed_at:"2026-10-26T19:07:33Z",key:"private-path",sha256:"private-hash"};
test("success notice reports verified facts without object paths or hashes",()=>{
 const text=backupSuccessNotice(verified);
 assert.match(text,/每日备份成功/);assert.match(text,/2026\/10\/27/);assert.match(text,/03:07:33/);
 assert.match(text,/3\.00 GiB/);assert.match(text,/数据库转储可完整读取/);
 assert.doesNotMatch(text,/private-path|private-hash/);
});
test("unverified, skipped, malformed, or failed results never produce a success notice",()=>{
 for(const patch of [{ok:false},{verified:false},{database_dump_readable:false},{archive_files_verified:6},{bytes:0},{attempts:4},{completed_at:"invalid"}]){
  assert.throws(()=>backupSuccessNotice({...verified,...patch}));
 }
 assert.throws(()=>backupSuccessNotice({ok:true,skipped:"already-running"}));
});
