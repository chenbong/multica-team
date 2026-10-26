# InfoFlow (如流) ↔ Multica bridge

A standalone Node service that lets you work with Multica from a 如流 robot chat.
Nothing inside the Multica checkout is modified.

## Group reply notifications

Group AI replies use one extended Markdown message with a native AT block.
The visible AT row is below the body and above the full-conversation link.
Replies have an 8192 UTF-16-unit budget. Requesters are prioritized; at most
50 present group members are notified, with an omission notice when necessary.
Private replies have the same budget but no AT. Failed sends retry the pinned
message; they never fall back to a standalone TEXT notification.
See [combined replies](../docs/combined-infoflow-replies.md).

## Bound task subscriptions

All active conversations bound to one task subscribe to its new comments and
agent replies, including scheduled wakeups and replies to older instructions.
This includes cross-conversation private/group sharing. The original human
message is not echoed into its source conversation. Unbound Chats retain their
existing isolation. Other tasks and workspaces never become subscriptions.

Human comments carry the author's name without source-conversation labels and
do not trigger native AT. Agent replies notify the requester and explicit
recipients independently in each destination group. Scheduled wakeups notify
only accounts explicitly named in the instruction snapshot, never the creator
by default. Group membership is checked before applying the 50-person cap.

Apply migration 531 with API and Bridge stopped, after backing up. Existing
bindings start at that cutover; older missing or pending messages are not
replayed. New/recreated bindings start at their own effective time. Unbinding
fences queued/retrying deliveries, and every external send rechecks authority.
Already sent comments are not edited or recalled when the web comment changes.
See [subscription policy](../docs/infoflow-task-subscriptions.md).

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

With the authenticated issue relay enabled, `/help` and `/status` are read-only
conversation commands, independent of quote/task selection:

- `/help` returns static usage without task metadata or a conversation-link footer.
- Group `/status` lists every issue bound to the current group that the sender can
  access through workspace membership, including issues assigned to other agents.
- Private `/status` lists the addressed agent's single-agent and squad issues in
  its workspace, including group-bound, private-bound, and unbound issues. It does
  not expose other users' private conversation identifiers.
- Each status row includes the task status and a direct issue link. Long lists
  use `/status 2`, `/status 3`, etc. rather than sending a burst of messages.
  Rows also show the task title and current responsible agents (single agent,
  or squad leader and members), excluding archived/cross-workspace agents.
  Long display names/titles are safely shortened without breaking task links.
- `/binding` without arguments is a compatibility alias for `/status`; `/unbind
  <issue-key>` remains a task-specific mutation. `/status <issue-key>` reads one
  task bound to the current conversation.
- Ambiguous ordinary input still requires a quote or issue key. Its notice lists
  only that agent's eligible bindings in that conversation, with individual issue
  links instead of an unrelated `/chat` footer. No comment or run is created.

Saving a new/changed agent binding on the robot admin page sends a confirmation
from that robot to the signed-in operator's private chat after persistence and
runtime synchronization. Unchanged saves and credential-only edits do not repeat
the notice. Disabled robots send nothing; delivery failure preserves the saved
binding and shows a clear warning instead of reporting a failed save. No group
announcement, issue comment, or agent run is created by this confirmation.

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

### 如流消息样式与完整对话链接

任务绑定、解绑成功后，在对应群聊或私聊发送任务模式提示及任务链接。
提示使用事务内持久队列，失败重试会轮转；同一任务重新绑定后不重发旧解绑提示。
网页新评论按真实用户名同步到已绑定对话，后续智能体回复同样同步。
仅从绑定成功后的新评论开始，不补发历史内容；解绑后停止同步。
如流输入不回传来源对话，但会同步到该任务的其他有效绑定；智能体新回复同步到全部有效绑定。跨任务、跨工作区内容仍不得混入。

平台聊天按工作区成员资格与智能体调用权限共享，旧聊天也适用。
有权限的成员可查看和继续对话，每条新消息独立记录真实作者；无权限的成员无法通过链接、
工具日志或实时消息读取。删除、归档等会话管理操作仍由创建者控制。

任务进入审核或阻塞时，不再额外私聊通知机器人绑定者，也不扫描历史状态事件。
任务和聊天回复按对应的如流对话投递；群聊绑定不会额外产生绑定者私聊。
验证码发送、命令回复和「收到」表情确认保留。旧状态通知记录仅用于审计，不再读取或重发。

聊天和任务回复仅在如流发送时添加加粗模式标记，不改变平台评论或模型输入。
新的回复只发送一条消息，总预算为 1800 UTF-8 字节，包含模式标题、队长代发标签和
加粗可点击的「完整对话链接」。短正文原样保留；超长正文按完整 Unicode 字符保留前段，
并提示在链接查看全文。平台评论、聊天正文和附件不截断。
新的任务回复使用原生任务路径并附 `#comment-<comment_id>`，identifier 优先，UUID 同样可用；
聊天回复使用该次 pending 的 `sessionId`，链接为 `/<workspaceSlug>/chat?session=<sessionId>`。
命令或权限通知没有评论或会话标识时使用任务页或聊天入口。
可通过 `INFOFLOW_ISSUE_WEB_URL`（或 `multica.issueWebUrl`）为两类链接指定同一个入口；
未配置时沿用 `multica.webUrl`，地址完全来自配置。

新的任务投递在首次发送前记录 `summary-v1` 格式及发送路由，成功后记录消息编号；
发送失败保持 pending，Ack 失败只重试 Ack。摘要校验包含完整原文，尾部改变也不能误 Ack。
没有格式标记的旧投递 checkpoint 继续原有分片策略，保持 digest、messageIds 和路由 pin，
只发送尚未投递的部分，全部成功后再 Ack；因此升级时已有的旧长回复可能仍发送多条。
无法证明路由的旧群 checkpoint 仍保留待核查。新回复的 1800 字节预算不重新切分旧分片。

聊天、任务和命令都使用「收到」表情确认。同一消息编号的近期成功确认在
持久记录中去重；表情仅表示收到，不代表有执行权限或任务成功。
接口拒绝或缺少消息标识时，聊天模式才回退文字确认。
持久输入队列保留表情所需的消息编号，不保存 SDK 客户端或机器人凭据。

### 可选：仅网关与 WebSocket 使用代理

设置 `INFOFLOW_WS_PROXY=http://<proxy-host>:<port>` 后重启 Bridge，端点申请和
WebSocket 握手（包括自动重连）均使用该代理，TLS 证书仍正常校验。
留空维持直连；此设置不会改变 Multica API、验证码和其他内网 HTTP 请求的路由。
不要为此设置进程级 `HTTP_PROXY` / `HTTPS_PROXY`。

安装依赖时 `postinstall` 会给固定版本 SDK 添加实例级 agent 注入点，
不修改 Node 全局网络方法。升级 SDK 需要重新审查补丁；版本或匹配点不符会拒绝应用。
如果安装时使用了 `--ignore-scripts`，需要补跑 `npm run postinstall`。
WebSocket 自动注册回调使用机器人的 `baseUrl`，与发送消息的 API 地址一致。

## 配置页面（本机）

Bridge 启动后在 <http://127.0.0.1:4180> 提供一个本机配置页：新增机器人时填名称、应用 ID、
App Key、App Secret，再从下拉框里选一个 Multica 智能体绑定，保存即生效，不需要重启进程，也不
需要命令行。列表里能看到每个机器人的连接状态，并可以直接发一条测试消息验证凭据。

一个如流机器人绑定一个智能体；多个机器人各自建立自己的如流长连接，会话状态按机器人隔离。

这个页面默认只监听回环地址；需要远程访问时配置 `admin.host`。不再校验请求域名，已有 `admin.publicHosts` 配置保留兼容但不再限制访问。新增域名无需修改白名单。页面仍然**要求登录**，账号归属及权限检查不变：
填 baidu 邮箱 → 验证码机器人把一次性验证码私聊给你 → 输入验证码换到一个 HttpOnly 会话 cookie
（SameSite=Strict，30 天）。受保护的管理接口没有会话就是 401。默认允许配置里列出的邮箱域名登录，
要收窄到具体几个人就在 `config.local.json` 里写 `admin.allowEmails`；`admin.allowedDomains` 可以改域名白名单。
验证码限一分钟一次、10 分钟过期、最多试 5 次。会话持久化在受保护的本机文件中，重启后保留，验证码仍为一次性。
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

本地回归运行 `npm test` 和 `npm run check`。根目录的 `reply-markdown.check.mjs` 是
显式非默认检查，不在 `npm test` 中自动执行。它复用本机已安装的 marked 模块，
不改变 dependencies、不下载依赖，也不调用部署服务或 SDK：

```bash
node reply-markdown.check.mjs <marked-module-path>
```

该检查覆盖代码围栏、列表和引用内的代码、链接标签/地址/标题、行内代码和自动链接，
同时验证两种模式标题与底部加粗链接实际渲染为超链接，完整消息仍在字节预算内。

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
