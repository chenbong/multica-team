# InfoFlow (如流) ↔ Multica bridge

A standalone Node service that lets you work with Multica from a 如流 robot chat.
Nothing inside the Multica checkout is modified.

## What it does today

One direct message becomes one Multica issue assigned to an agent; when the run finishes,
the agent's answer comes back to the same chat.

```
如流 私聊  --WebSocket-->  bridge  --multica CLI-->  Multica  -->  Codex 运行时
如流 私聊  <-------------  bridge  <--轮询 runs/comments--  Multica
```

Group chats work the same way, but only when the robot is @-mentioned. `allowAllInGroups`
is on, so every member of a group the robot belongs to can dispatch work — and therefore
start an agent run on this machine. Direct chats stay limited to `allowUsers`.

Only `MESSAGE_RECEIVE` group events are acted on. `ALL_MESSAGE_FORWARD` carries every
message in the group and would turn small talk into agent runs.

Chat commands: `/help`, `/agents`, `/agent <名字>` (per-user default, shared between direct
and group chats), `/status` (per conversation).

InfoFlow sometimes delivers the same message twice a few seconds apart, so messages are
de-duplicated by `MsgId`, and identical text from the same person within a minute is
answered with a pointer to the issue it already created.

Design notes worth knowing:

- Multica is reached through the CLI at `../deploy/bin/multica`, which already carries
  the signed-in workspace and token, so this service stores no Multica credential.
- Completion is detected by polling `issue runs`, then reading the agent comment belonging to
  that run (falling back to `run.result.output`). A Multica plugin event hook would replace the
  polling; that is the next increment and needs `MULTICA_PLUGIN_SECRET_KEY` plus an https endpoint.
- `allowUsers` must not be empty. Every accepted message starts a real agent run on this machine,
  so an open robot would hand strangers local execution.

## Setup

## 配置页面（本机）

Bridge 启动后在 <http://127.0.0.1:4180> 提供一个本机配置页：新增机器人时填名称、应用 ID、
App Key、App Secret，再从下拉框里选一个 Multica 智能体绑定，保存即生效，不需要重启进程，也不
需要命令行。列表里能看到每个机器人的连接状态，并可以直接发一条测试消息验证凭据。

一个如流机器人绑定一个智能体；多个机器人各自建立自己的如流长连接，会话状态按机器人隔离。

这个页面只监听回环地址，并拒绝 Host 头不是 localhost 的请求（防 DNS rebinding），并且**要求登录**：
填 baidu 邮箱 → 验证码机器人把一次性验证码私聊给你 → 输入验证码换到一个 HttpOnly 会话 cookie
（SameSite=Strict，12 小时）。除两个登录接口外的所有接口没有会话就是 401。默认放行配置里列出的域名，
要收窄到具体几个人就在 `config.local.json` 里写 `admin.allowEmails`；`admin.allowedDomains` 可以改域名白名单。
验证码限一分钟一次、10 分钟过期、最多试 5 次，会话存在内存里，重启 bridge 等于所有人重新登录。
AppSecret 存在 `config.local.json`（0600），页面上不回显，留空表示保持原值。

页面只管理**任务机器人**：验证码机器人不出现在列表和表单里，也不能通过接口改名或删除，因为它是所有人共用的
登录通道，不需要每个人自己建一个。

页面顶部还内置了创建引导：如流没有创建机器人的开放接口（开放平台的服务端 API 列表里只有查询可见范围一类的应用管理接口），
所以新建机器人仍然要在如流开放平台的 Web 控制台点「新建应用」，免审创建后在「凭证与能力」复制应用 ID / App Key /
App Secret，再到「权限管理」开四项消息权限、在「可见范围」加人和群。列表里每个机器人都带凭证、权限管理、应用主页的直达链接。

## Multica 账号（管理员视角）

页面最上面列出的 Multica 账号，是这台机器上已登录的 CLI profile（默认的 `~/.multica/config.json` 和每个
`~/.multica/profiles/<名字>/config.json`）里**邮箱和你的登录邮箱相同**的那些。别人的 profile 不会出现，
也不能通过接口选中：`/api/robot` 只接受属于你的 profile，删除和发测试消息也只对绑在你账号上的机器人生效，
其余机器人在列表里是只读的。选中哪个账号，下面的智能体下拉框就是那个账号在 Multica 里看得到的智能体，
新建或编辑的机器人也用那个账号建任务。加一个账号：

**不需要在这台机器上做任何手工操作**：在 Multica 的 Web 页面上用同一个邮箱登录一次（邮箱验证码由如流的
验证码机器人私聊发给你），Multica 服务端就会在登录成功时自动完成三件事——按"一人一枚"的口径签发这个用户的
长期 Personal Access Token、把 token 写进 `~/.multica/profiles/<邮箱前缀>/config.json`（0600）、把账号已经
属于的工作区一并写进去。于是重新打开本页面时，顶部就会显示「Multica 已登录」，直接可以选账号、绑机器人。

profile 叫什么无所谓，配对靠的是这个 profile 登录进 Multica 的邮箱。手动登录的 profile 仍然可用
（`multica --profile 名字 login --token mul_...`），会和自动登记的那一份按邮箱合并成同一个账号。
如果 profile 里暂时没有工作区（例如登录时这个账号还没建工作区），页面第一次读到它时会自动选中该账号可见的
第一个工作区并写回 profile，因为 CLI 在没有 `workspace_id` 时会拒绝所有工作区相关的命令。

每个机器人把自己的 profile 存进 `config.local.json`，所以在这台机器上重新 `multica login` 换成别人，
不会改变已保存的机器人用谁的身份派任务——之前正是因为没有这个字段，bridge 跟着默认配置漂移到了最后登录的那个账号。
页面提交的 profile 会先和已登录的账号核对再落盘，因为它最终会作为 `--profile` 传给 CLI。

### 一个账号的多个工作区

一个 Multica 账号可以属于多个工作区，而 CLI profile 里的 `workspace_id` 只能钉住其中一个。页面不这样：
它会把该账号**所有工作区**里的智能体一起列出来，每项标成 `【工作区】智能体 · 模型 · 归属`，
所以换工作区不需要改任何配置，也不会出现"第二个工作区建的智能体在页面上看不到"。

实现上，页面对每个工作区各调一次 `multica --profile <账号> --workspace-id <工作区> agent list`
（`--workspace-id` 是 CLI 的全局参数，等价于环境变量 `MULTICA_WORKSPACE_ID`），
读成员列表也用同一个工作区。选中的智能体所属工作区会随机器人一起落盘（`workspaceId` +
`workspaceSlug`），派任务、建 issue、回帖链接全部按这个工作区走；因此同一个账号在不同工作区的智能体
可以各绑一个机器人，互不干扰。某个工作区读不出来（权限、服务端抖动）只会让那一行缺失并在页面顶部提示，
不会影响其它工作区。

下拉框里的每个智能体都标了归属：「本人创建」是当前账号自己的，「来自 someone@example.com」是别人创建但这个账号有权访问的
（Multica 侧的可见性规则说了算，工作区可见和被授权的私有智能体都会出现）。勾上「只看这个账号创建的」就只剩自己的。

账号这一层已经按登录邮箱隔离了。还没做的是机器人本身的归属：别人的机器人仍然在列表里可见（只读），
看不到的是它绑定的智能体和它所属账号的邮箱。要彻底按人收敛，还需要给机器人加 owner 字段并按登录邮箱过滤列表。

1. Create the robot in the IM provider's web console and note AppKey / AppSecret / agentId. The
   default permissions (send/receive in direct and group chats, receive @robot) are enough.
2. Fill `config.local.json` (already created, mode 0600, git-ignored):

   ```json
   { "infoflow": { "appKey": "...", "appSecret": "...", "agentId": "..." } }
   ```

   `INFOFLOW_APP_KEY` / `INFOFLOW_APP_SECRET` / `INFOFLOW_AGENT_ID` override the file.
3. Run in the foreground for the first connection so problems are visible: `npm start`.
   Afterwards `./start.sh` runs it detached and `./stop.sh` stops it.

## Rehearsal without 如流

## 登录验证码转发

一个机器人的用途设为「发送登录验证码」后就只发不收：不绑智能体、不建立 WebSocket 连接。整台机器只需要这一个，
它同时承担两件事——把 Multica 的登录验证码私聊给本人，以及给这个配置页面自己的登录发验证码。它不在页面上管理，
要改就直接编辑 `config.local.json` 里 `purpose: "verification"` 的那条。

Multica 那一半的原理是：Multica 在没有配置邮件后端时，会把 `[DEV] Verification code for <邮箱>: <验证码>` 打进
`multica/.multica/local/logs/api.log`。Bridge 只读该文件的新增部分，取邮箱前缀当如流用户名把码发出去。
配置页面那一半的验证码是 bridge 自己生成的，只在内存里，发送失败时会把码写进 `logs/bridge.log` 兜底，
否则机器人一坏就没人能进页面了。

注意事项：

- 假设邮箱前缀等于如流用户名（`someone@example.com` → `someone`）；不一致时发送会失败并记录日志。
- 验证码机器人的可见范围必须包含所有需要登录的人，否则发送报 40070，日志里是 `could not relay the code to ...`。
- 只转发配置里允许的域名，其他域名只记日志不发送。
- 验证码会明文经过本机日志文件和如流消息；日志这一跳在配置邮件后端之前本来就存在。
- 如流 SDK 会把发出的请求体打进 `logs/bridge.log`，所以页面验证码也会留在本机日志里。
- 转发进程停了就没人能收到验证码（你仍可从 api.log 里读），所以它和 bridge 同生共死，重启用同一套脚本。
- 想改成正规邮件投递就配 `SMTP_HOST` 等变量，Multica 的发信优先级是 SMTP → Resend → 打日志，配上之后这个转发自然失效。


`scripts/dry-run.js` pushes a message through the same code path and prints what the robot
would have sent, so the Multica half can be verified before credentials exist:

```bash
node scripts/dry-run.js '请只回复一句 pong'
node scripts/dry-run.js '/agents'
```

## Verified so far

Dry runs created TEAM-2 and TEAM-3, both dispatched to 「Infra队长」 on the Codex runtime, and
returned the agent's answer in about 40 seconds. `/agents` and `/status` were exercised the same
way. The 如流 side is written against the official Node SDK (`@baidu/infoflow-sdk-nodejs`
2026.8.28), and its exports were checked, but it has not connected yet — that needs the robot
credentials.

## Not covered yet

Group chats, streaming work cards, media, binding 如流 users to Multica members (today the issue
is created as the CLI's signed-in user), and restart after reboot.
