// Called only after a verified backup; the JSON result is read from stdin.
import fs from "node:fs";
import {createRequire} from "node:module";
import {backupSuccessNotice} from "./backup-success-notice.mjs";

try {
  const recipient=process.argv[2];
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(recipient??"")) throw new Error("Invalid recipient");
  const chunks=[]; let size=0;
  for await (const chunk of process.stdin) {
    size+=chunk.length;
    if (size>65536) throw new Error("Result exceeds limit");
    chunks.push(chunk);
  }
  const prefix=process.argv[3]==="--test-existing" ? "【验收测试：复核已有备份】\n\n" : "";
  const content=prefix+backupSuccessNotice(JSON.parse(Buffer.concat(chunks).toString("utf8")));
  const config=JSON.parse(fs.readFileSync("/team/config/bridge.json","utf8"));
  const robots=(config.robots??[]).filter(robot=>robot.name==="TMesh日报机器人"&&robot.enabled!==false);
  if (robots.length!==1) throw new Error("Backup notifier unavailable");
  const robot=robots[0],require=createRequire("/team/code/bridge/package.json");
  const {Client}=require("@baidu/infoflow-sdk-nodejs");
  const quiet={info(){},warn(){},error(){},debug(){},trace(){},log(){}};
  const client=new Client({appKey:robot.appKey,appSecret:robot.appSecret,agentId:robot.appId,baseUrl:robot.baseUrl,logger:quiet});
  const result=await client.im.message.sendToUser(recipient,content,"md");
  if (typeof result?.msgkey!=="string" || !result.msgkey.trim()) throw new Error("Notification receipt missing");
  console.log("Verified backup success notice delivered");
} catch {
  console.error("Verified backup success notice could not be delivered");
  process.exitCode=1;
}
