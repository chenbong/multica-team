// Run only by the host backup service after its final failed attempt.
import fs from "node:fs";
import {createRequire} from "node:module";
const config=JSON.parse(fs.readFileSync("/team/config/bridge.json","utf8"));
const robots=(config.robots??[]).filter(robot=>robot.name==="TMesh日报机器人"&&robot.enabled!==false);
if(robots.length!==1)throw new Error("Backup notifier robot is unavailable");
const robot=robots[0];
const recipient=process.argv[2];
if(!/^[a-z0-9][a-z0-9._-]{0,127}$/.test(recipient??""))throw new Error("Invalid backup notification recipient");
const require=createRequire("/team/code/bridge/package.json");
const {Client}=require("@baidu/infoflow-sdk-nodejs");
const quiet={info(){},warn(){},error(){},debug(){},trace(){},log(){}};
try{
 const client=new Client({appKey:robot.appKey,appSecret:robot.appSecret,agentId:robot.appId,baseUrl:robot.baseUrl,logger:quiet});
 const prefix=process.argv[3]==="--test"?"【验收测试，仅验证通知通道】\n\n":"";
 await client.im.message.sendToUser(recipient,prefix+"**Team Mesh 每日备份失败**\n\n备份已重试 2 次，仍未成功。请检查服务器的每日备份执行记录；不要删除已有可用备份。","md");
 console.log("Backup failure notice delivered");
}catch{console.error("Backup failure notice could not be delivered");process.exitCode=1;}
