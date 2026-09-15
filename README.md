# multica-team

在官方 Multica 之上做团队化增量开发的 overlay 仓库。所有定制都通过 **overlay（覆盖层）** 实现：
官方上游 `multica-ai/multica` 以 git submodule 固定版本引入，源码一个字节都不改，升级上游只需对齐
overlay 文件；仓库可直接公开，部署相关的主机、端口、密钥与品牌信息全部走本机配置。

## 相比官方 Multica 的增量

1. **Skill 三级作用域：个人 / 项目 / 团队**。官方只有工作区内的技能；这里把技能拆成
   `personal`、`workspace`（项目）、`team` 三级，团队技能与项目技能都跨工作区、跨用户可见，
   列表里按级别分区展示并标注 `团队` / `项目：<项目名>` / `个人`；创建者可以在三级之间调整级别
   （`PUT /api/skills/{id}/scope`、`POST /api/skills/{id}/promote`），其余人只能按来源项目的管理员策略编辑。
   导入技能时同样按所选级别落库：URL、压缩包/本地目录、从运行时导入都会保留 `团队` 级别，
   不会悄悄降级成项目（团队技能的 `workspace_id` 为空，项目技能才绑定工作区）。
   数据库侧对应 `migrations/457`–`463`：scope 字段、跨工作区可见性、团队唯一索引、owner 与用量统计索引。
2. **技能级别在智能体侧统一呈现**。创建智能体、智能体能力（Skills）页、技能选择器都显示同一套
   级别标签，避免"在 Skills 页是团队、在智能体里看不出来"的不一致。
3. **运行时 profile 的归属与可见性**。为已登录用户自动物化内部运行时 profile（ducx/ducc 等），
   并按 owner 过滤，只把属于当前用户的 profile 下发给 daemon。
4. **daemon 引导令牌**。新增 `GetOrCreateDaemonBootstrapToken`：为认证用户原子地签发或复用
   个人访问令牌（一人一枚，重复调用返回同一枚），配合 `POST /api/daemon-bootstrap-token` 路由，
   让「添加电脑」流程可以一键拿到令牌；**用户登录 Web 成功时**服务端也会走同一条逻辑，
   顺手把令牌和 CLI profile 写到本机，因此用户自己的电脑与服务器上的机器人共用同一个身份，
   Bridge 那边不需要任何手工操作或二次绑定。
5. **新智能体默认带沙箱标记**。新建的用户自建智能体会在自身环境变量里带上 sandbox 标记，
   所有者之后可在智能体的环境变量设置里修改或删除。
6. **Codex 模型目录读本机配置**。上游用 `codex debug models --bundled` 枚举模型，只能拿到
   官方内置列表；overlay 改成读本机 `~/.codex/config.toml` 指定的目录（`codex debug models`），
   因此界面上可选模型与本机实际可用的模型一致。
7. **「添加电脑」命令按用户隔离 profile**。生成的 daemon 命令带 `--profile <用户>`，
   同一台电脑上多个账号各自持有 config、daemon id 与工作区根目录，不会互相覆盖登录态。
8. **登录页文案与验证码机器人可配置**。登录标题、说明、邮箱占位符、验证码机器人名称与对话链接
   都由环境变量注入（留空即保持上游文案），构建产物里不含任何部署专属字符串。
9. **如流（InfoFlow）Bridge**（独立 Node 服务，见 `infoflow-bridge/`）：把 IM 私聊/群消息变成
   Multica 任务并把智能体结果回帖；转发 Multica 的登录验证码到 IM 私聊；把验证码镜像到指定群
   （正文前加 `发送给 <用户名>`）；自带管理页做机器人配置、按登录邮箱隔离 Multica 账号、
   绑定智能体与连通性自检。
10. **可复用的部署工具链**：Go overlay 清单生成与构建、Web 临时树补丁构建、单实例的
    build/start/stop/status 脚本、可选隔离 daemon，以及 GitHub Actions 多平台 daemon 构建
    （`darwin`/`linux`/`windows` × `amd64`/`arm64`）。
11. **可裁剪的内置运行时探测**。上游 daemon 会把机器上装着的每种 agent CLI（claude、codex、
    cursor…）都注册成一个运行时；overlay 增加了 `MULTICA_DAEMON_RUNTIMES` 白名单，只注册名单内的
    内置 provider。部署自己的自定义运行时 profile（如 `ducc`/`ducx`）走独立注册路径，不受影响，
    因此可以做到"只暴露我自己的运行时"。用
    `multica config set daemon_runtimes ducc,ducx` 写进 profile 即可持久化（环境变量优先级更高），
    留空则保持上游行为。白名单也可以随「添加电脑」下发的命令一起预置：本机 `deploy/host.env`
    里的 `MULTICA_DAEMON_RUNTIME_DEFAULTS=ducc,ducx` 会让页面给出的命令多带一行
    `multica --profile <用户> config set daemon_runtimes ducc,ducx`，用户复制即可生效；
    留空则下发上游命令，仓库里不含任何 provider 取值。注意白名单筛选的是**内置探测**：像
    `ducc`/`ducx` 这种自定义运行时的名字不在内置表里，所以配了这种白名单就等于"内置运行时一个都不要"，
    此时 daemon 依然正常启动（不会报 `no agent CLI found`），只注册工作区里配置的自定义运行时 profile；
    工作区里还没有这样的 profile 时，daemon 起来后是"零运行时"的空转状态，去运行时页建一个即可。
12. **运行时命令包装器（runtime shims）**。有些厂商 CLI 拿到 agent backend 传的**整套参数**就会出问题：
   参考案例是一个内部 Claude Code 构建，`--verbose` 与 `--settings <文件>` 同时出现时会把凭证丢掉、
    回一句 `Not logged in · Please run /login`，而单独给任何一个都正常；偏偏 `--verbose` 不能省
    （`-p --output-format stream-json` 不带它会直接报错），`--settings` 又是运行时技能策略的载体。
    overlay 增加了 `runtime_shims`：`multica config set runtime_shims ducc`（或环境变量
    `MULTICA_DAEMON_RUNTIME_SHIMS`）声明哪些命令需要包装器，daemon 启动/刷新运行时时在
    `~/.multica/shims/<命令名>` 生成一个脚本（0700），把 `--settings` 的内容落到当前任务工作目录的
    `.claude/settings.local.json` 再 `exec` 真正的命令，然后用它替代 PATH 解析结果注册运行时。
    名单之外的命令一个字节都不变（`ducx` 这类本来就正常的 CLI 完全不受影响），名单为空则不生成任何包装器。
    「添加电脑」下发的命令同样可以预置这一行（`deploy/host.env` 里的
    `MULTICA_DAEMON_RUNTIME_SHIM_DEFAULTS=ducc`），用户复制即可，无需在每台机器上手工维护脚本。

## 快速开始

```bash
git clone <this repo> multica-team && cd multica-team
git submodule update --init --recursive
cp deploy/host.env.example deploy/host.env      # 填主机地址与端口
cp infoflow-bridge/config.overlay.example.json infoflow-bridge/config.local.json   # 可选
./deploy/build.sh && ./deploy/start.sh && ./deploy/status.sh
```

文档里的 `192.0.2.10` 是 RFC 5737 保留的占位地址，示例路径统一写成 `/opt/multica-team`；
真实主机地址、端口、数据库名、登录域名与品牌文案只写在机器本地的 `deploy/host.env`
（以及 `infoflow-bridge/config.local.json`），这些文件都不入库。

## 1. 端口与地址

| 服务 | 端口 | 地址 | 说明 |
| --- | --- | --- | --- |
| Web（Next.js） | `8004` | <http://192.0.2.10:8004> | 用户界面，浏览器只访问这一个端口 |
| API（Go server） | `8006` | <http://192.0.2.10:8006> | Web 通过 Next 的 `proxy.ts` 反向代理转发，不要求用户直连 |
| Bridge 管理页 | `8002` | <http://192.0.2.10:8002> | 如流机器人配置页，需要邮箱验证码登录 |
| PostgreSQL | `5432` | `127.0.0.1` | 库名由 `host.env` 指定，角色 `multica` |

端口约定：默认使用从 `8002` 开始的偶数端口（`8002`、`8004`、`8006`…），
新增服务从下一个偶数端口继续。

## 2. 目录结构

```text
multica/                   官方上游仓库，作为 git submodule 固定，保持干净
overlays/
  go/files/                Go 侧的完整替换文件（服务端与 CLI）
  prepare.py               校验并生成 go-overlay.json / upstream.json
  go-overlay.json          构建时生成的 Go overlay 清单（不入库）
  upstream.json            上游 commit 与每个替换文件的哈希（不入库）
  web-direct/              Web 侧直接覆盖的文件（补丁脚本覆盖不到的部分）
  web-patch.py             Web 侧脚本化补丁配方（精确字符串匹配）
  sql/                     sqlc 查询定义（用于生成 generated/*.go 覆盖文件）
  font-mock.cjs            构建时屏蔽 Google Fonts 在线请求
migrations/                数据库迁移，放在子模块之外
deploy/                    构建、启动、停止、状态脚本与机器配置
infoflow-bridge/           如流机器人 Bridge（独立 Node 服务）
```

## 3. overlay 开发方式

核心原则：**上游 `multica/` 子模块永远保持 `git status` 干净**。`deploy/build.sh` 在构建前后
都会检查这一点，一旦发现脏改动就直接失败退出。

### 3.1 Go 端

Go 使用官方支持的 overlay 机制，不做文本替换：

1. `overlays/go/files/` 下的每个文件都是**完整文件**，路径与上游一一对应，例如
   `overlays/go/files/server/internal/handler/skill.go` 对应
   `multica/server/internal/handler/skill.go`。
2. `overlays/prepare.py` 校验每个替换目标在上游确实存在、子模块干净，然后生成
   `overlays/go-overlay.json`（`go build -overlay` 需要的 `Replace` 映射），
   并把上游 commit 与每个文件的前后哈希写进 `overlays/upstream.json`。
3. 构建命令是 `go build -overlay=overlays/go-overlay.json`，编译产物是
   `deploy/bin/server`、`deploy/bin/multica`、`deploy/bin/migrate`。

因此升级上游时，只要把 `overlays/go/files/` 里的文件与新版本同名文件对齐即可，
上游 checkout 不会有任何改动。

### 3.2 Web 端

Next.js 没有 overlay 机制，所以采用"临时树 + 脚本补丁"的方式：

1. `deploy/build.sh` 把 `multica/` 复制到一次性的 `.web-build/multica`（不复制 `.git`、
   `node_modules`、`.next`）。
2. 先按 `overlays/web-direct/` 里的清单整体覆盖同名文件（如 Skills 页面、技能选择器等整套组件）。
3. 再运行 `python3 overlays/web-patch.py apply`，对少量文件做精确字符串替换（文案、登录命令、locales 等）。
   每条替换都要求在上游文件中**恰好出现一次**，否则直接报错退出，避免上游改动后静默生成没打补丁的站点；
   重复执行是幂等的。
4. 补丁前的原文会备份到 `.web-build/multica/.overlay-backup/`，`web-patch.py restore` 可以还原。
5. 最后在 `.web-build/multica/apps/web` 里执行 `next build --webpack`。整个 `.web-build/` 都是可丢弃的，
   删掉重建即可得到干净的构建。

注意：因为补丁依赖精确锚点，**升级上游后必须重新构建一次**；如果 `web-patch.py` 报
`Upstream changed: ... expected anchor`，说明上游对应代码变了，需要人工比对后更新锚点。

### 3.3 数据库迁移

上游之外的迁移放在 `migrations/`，由 `deploy/start.sh` 在每次启动时按文件名顺序执行（`*.up.sql`），
全部写成可重复执行的语句。当前内容：

- `001_user_daemon_bootstrap_token`：`user` 表新增 daemon 引导令牌字段
- `457_skill_scope` … `463_skill_team_scope`：Skill 多级别（个人 / 项目 / 团队）与统计相关的索引、字段

`overlays/sql/` 里放的是 sqlc 查询定义；对应的 `server/pkg/db/generated/*.go` 生成结果
同时以完整文件形式放在 `overlays/go/files/` 中，两者要一起改。

### 3.4 修改或新增一个 overlay

```bash
cd /opt/multica-team

# 1) 确认子模块干净（脏了先处理，不要直接改上游文件）
git -C multica status --short

# 2) 从上游文件出发修改，结果放回 overlay 目录
#    Go：   cp multica/server/.../x.go overlays/go/files/server/.../x.go 之后编辑
#    Web：  改成整套文件放 overlays/web-direct/，或小改动加进 overlays/web-patch.py 的 CHANGES

# 3) 重建并重启
./deploy/build.sh
./deploy/stop.sh && ./deploy/start.sh
./deploy/status.sh

# 4) 浏览器验证后提交推送
git add -A && git commit -m "..." && git push origin main
```

### 3.5 升级上游子模块

```bash
cd multica && git fetch origin && git checkout <新版本 tag 或 commit> && cd ..
git -C multica status --short          # 必须为空
./deploy/build.sh                      # 补丁锚点若有变动会在这里报错
```

构建成功后再把 `multica` 子模块指针的变更一起提交。`prepare.py` 会挡住"上游没有该文件"的情况，
`web-patch.py` 会挡住"锚点变了"的情况，两者都是刻意设计的失败保护。

## 4. 构建、启动与停止

### 4.1 依赖

本机已就绪的版本（`deploy/env.sh` 会把它们加进 `PATH`）：

| 组件 | 位置 | 版本 |
| --- | --- | --- |
| Go | `/usr/bin/go` | 1.22.2 |
| Node / pnpm | `/opt/node-v24.18.0/bin` | v24.18.0 / pnpm 10.28.2 |
| PostgreSQL | 系统服务 | 16.15（集群 `16/main`，端口 5432） |

首次构建时若 `.web-build` 里缺少依赖，`build.sh` 会先尝试 `pnpm install --offline`，
失败再走线上安装（可能需要设置 `http_proxy`/`https_proxy`）。

### 4.2 机器配置

仓库只放通用代码与模板：**所有与本机、内网、品牌相关的取值都不入库**，只存在于运行实例的
机器上（`.gitignore` 已排除）。因此同一套代码既能直接部署，又能对外公开，仓库里搜不到内网 IP、
主机名、群 ID、机器人名称、registry 地址或任何密钥。

| 文件（机器本地，已忽略） | 内容 | 模板 |
| --- | --- | --- |
| `deploy/host.env` | 主机地址与端口、数据库与 profile 名、允许登录的邮箱域名、登录页品牌文案、CLI 安装脚本地址、「添加电脑」下发的运行时白名单与 shim 名单、如流端点、`clone-db.sh` 的源库 | `deploy/host.env.example` |
| `deploy/secrets.env` | `JWT_SECRET`、`MULTICA_PLUGIN_SECRET_KEY`（缺失时由 `start.sh` 自动生成） | `deploy/secrets.env.example` |
| `infoflow-bridge/config.local.json` | Bridge 的 Multica 地址、管理端口、机器人凭据、验证码机器人对话链接、镜像群、如流开放平台控制台地址 | `infoflow-bridge/config.overlay.example.json` |
| `infoflow-bridge/.npmrc`、`infoflow-bridge/package-lock.json` | 承载如流 SDK 的私有 npm registry | `infoflow-bridge/npmrc.example` |
| `deploy/ssh-config.github` | 让本机通过 `ssh.github.com:443` 访问 GitHub（出方向 22 端口被封时用） | 已入库，无密钥 |

登录页文案由环境变量驱动（`MULTICA_LOGIN_TITLE`、`MULTICA_LOGIN_DESCRIPTION`、
`MULTICA_EMAIL_PLACEHOLDER`、`MULTICA_VERIFICATION_BOT_NAME`、`MULTICA_VERIFICATION_BOT_URL`），
全部留空时保持 Multica 上游文案，不会指向任何机器人；`MULTICA_ALLOWED_EMAIL_DOMAINS` 为空时
由 API 自身的默认值决定谁可以登录。

`deploy/start.sh` 每次启动都会依据 `host.env` 重写 `config.local.json` 里的
`multica.webUrl`、`multica.serverUrl(s)`、`admin.port`、`admin.publicHosts`，
机器人条目、`admin.infoflowConsoleUrl` 等保持不变。**改端口请改 `host.env`。**

另外两个只影响「添加电脑」下发内容的变量：`MULTICA_DAEMON_RUNTIME_DEFAULTS`（预置
`config set daemon_runtimes`，见增量列表第 11 条）与 `MULTICA_DAEMON_RUNTIME_SHIM_DEFAULTS`
（预置 `config set runtime_shims`，见第 12 条）；`MULTICA_CLI_INSTALL_URL` 决定页面给出的安装命令
指向哪个 `scripts/install.sh`。三个都留空时下发的是上游原样命令。

### 4.3 命令

```bash
./deploy/clone-db.sh       # 一次性：从一个已有库克隆数据快照（源库由 MULTICA_CLONE_SOURCE_DB 指定）
./deploy/build.sh          # 生成 Go overlay 清单、编译 API/CLI/migrate、构建 Web
./deploy/start.sh          # 起 Postgres、建角色/库、跑迁移、启动 API + Web + Bridge
./deploy/stop.sh           # 停止 Bridge、Web、API
./deploy/status.sh         # 查看进程、健康检查、子模块是否干净
./deploy/start-daemon.sh   # 可选：以独立 profile 启动本地运行时 daemon
```

Bridge 也可以单独控制：

```bash
python3 infoflow-bridge/scripts/bridgectl.py status|start|restart|stop
```

`start-daemon.sh` 使用一个独立 profile（`host.env` 里的 `MULTICA_PROFILE_NAME`），默认**不**由
`start.sh` 拉起，避免同一批待执行任务被两个实例重复消费。

### 4.4 构建做了什么

1. 校验 `multica` 子模块干净；
2. `overlays/prepare.py` 生成 Go overlay 清单，并执行一次
   `go test -overlay=... -run '^$'` 做编译级检查；
3. 编译 `server`、`multica`（CLI）、`migrate` 到 `deploy/bin/`；
4. 复制上游到 `.web-build/multica`，套用 `web-direct` 与 `web-patch.py`；
5. 需要时安装 Web 依赖，然后 `next build --webpack`（用 `overlays/font-mock.cjs`
   屏蔽 Google Fonts 在线请求，离线也能构建）；
6. 再次校验子模块仍然干净。

### 4.5 启动做了什么

1. 确保 PostgreSQL 在跑，`multica` 角色与 `host.env` 指定的数据库存在（缺则创建）；
2. 执行上游迁移 `migrate up`，再按顺序执行 `migrations/*.up.sql`（幂等）；
3. 依据 `host.env` 重写 Bridge 的 Multica 地址与管理端口；
4. 用 `setsid nohup` 分别拉起 API、Web、Bridge，PID 写入 `deploy/logs/*.pid`。

### 4.6 日志

| 文件 | 内容 |
| --- | --- |
| `deploy/logs/api.log` | API 日志，也是验证码转发的来源 |
| `deploy/logs/web.log` | Next.js 日志 |
| `deploy/logs/migrate.log` | 迁移日志 |
| `infoflow-bridge/logs/bridge.log` | Bridge 与如流侧交互日志 |

## 5. 如流 Bridge

`infoflow-bridge/` 是一个独立的 Node 服务，把如流消息接到 Multica 上，同时承担登录验证码的投递。
详细说明见 `infoflow-bridge/README.md`，这里只列部署时要配的几项。

### 5.1 验证码机器人

`config.local.json` 中 `purpose: "verification"` 的机器人只发不收（不建立 WebSocket 长连接）：

- 读取 API 日志里 `[DEV] Verification code for <邮箱>: <验证码>` 行，用邮箱前缀当如流用户名私聊发送；
- 同时给 Bridge 管理页自身的登录发验证码；
- 每条验证码消息会额外镜像一份到群 `<镜像群 ID>`，并在正文前加上 `发送给 <用户名>`；
- 它不在管理页里出现，要改只改 `config.local.json`。

### 5.2 任务机器人与管理页

`purpose: "task"` 的机器人负责把如流消息变成 Multica 任务（私聊直接派活，群里需要 @ 机器人）。
在 <http://192.0.2.10:8002> 用 baidu 邮箱 + 验证码登录后可以新增/编辑机器人、
查看连接状态、发送测试消息；AppSecret 不回显，留空表示保持原值。

管理页本身不保存 Multica 凭据，它读取**本机** `~/.multica` 下的 CLI profile
（默认 `config.json` 和 `profiles/<名字>/config.json`），只把登录邮箱与页面登录邮箱一致、
且 `server_url` 指向本部署的那些 profile 列为"已登录"。所以这台机器上必须有这样一个 profile，
否则页面顶部会显示「Multica 未登录」、机器人列表也会是空的。

账号的登记**不需要在这个页面上做任何操作**：用户在 Web 端（`8004`）用自己的邮箱登录成功时，
API 就地为该用户签发/复用那枚长期令牌，并把 `~/.multica/profiles/<邮箱前缀>/config.json`
写好（0600，profile 名与邮箱一一对应）。之后同一个邮箱再登录本页面，顶部直接显示「已登录」。
写入的是「一人一枚」的同一枚令牌——用户在自己电脑上用「添加电脑」拿到的也是它；
profile 里暂时没有工作区时，Bridge 第一次读到会自动选中该账号可见的第一个工作区并写回，
因为 CLI 在没有 `workspace_id` 时会拒绝所有工作区相关的命令。

页面上的 Multica 入口、验证码机器人名称与对话链接、邮箱占位符、如流开放平台控制台链接，
都来自实例配置（`config.local.json` 的 `multica.webUrl`、验证码机器人的 `name`/`chatUrl`、
`admin.allowedDomains`、`admin.infoflowConsoleUrl`）；没配置的会直接隐藏，页面本身不带任何内网地址。

## 6. 登录与账号

- 浏览器打开 <http://192.0.2.10:8004>，填企业邮箱（如 `xxx@example.com`），点继续；
- 验证码有两种查看方式：如流验证码机器人的私聊消息，或点页面上的机器人链接直接打开对话窗口；
- 首次使用需要先在如流里打开过该机器人的会话，否则收不到消息；
- **登录成功时服务端会自动把账号登记到本机**：签发该用户那枚长期 PAT（一人一枚，与「添加电脑」
  发给用户的是同一枚），并写 `~/.multica/profiles/<邮箱前缀>/config.json`（`server_url`、`app_url`、
  `token`，账号已有工作区时连 `workspace_id` 一起写；文件 0600、目录 0700，已存在的其他字段如
  `device_name`/`workspaces_root`/`daemon_runtimes` 会被保留）。如流 Bridge 管理页认账号读的就是它，
  所以用户不需要在服务器上做任何手工操作。
- 账号暂时没有工作区时 profile 里会缺 `workspace_id`，Bridge 第一次读到会选中该账号可见的第一个
  工作区并写回（CLI 没有 `workspace_id` 会拒绝所有工作区相关命令）。
- 手工维护 profile 的方式仍然有效：`multica --profile <名字> config set server_url …` +
  `login --token mul_…` + 需要时 `config set workspace_id <id>`；改端口后要同步更新并
  `multica --profile <名字> daemon restart`。

## 7. 从零重建

```bash
git clone <this repo> multica-team
cd multica-team
git submodule update --init --recursive      # 拉取固定版本的上游

# 机器配置不入库，按模板创建：cp deploy/host.env.example deploy/host.env 后改成实际地址与端口
# secrets.env 可以留空，deploy/start.sh 会在缺失时自动生成
# Bridge 配置：cp infoflow-bridge/config.overlay.example.json infoflow-bridge/config.local.json
# 数据库：全新库由 start.sh 自动创建；要从已有实例迁移数据则先跑 ./deploy/clone-db.sh

./deploy/build.sh
./deploy/start.sh
./deploy/status.sh
```

全新机器**不需要手工补 Multica 的 CLI 登录态**：只要有人在 Web 端登录一次，服务端就会为这个邮箱
自动写好 `~/.multica/profiles/<邮箱前缀>/config.json`，如流绑定页随即认得这个账号。要手工做一个
（例如给一个不会用 Web 端的账号，或想固定 profile 名）也可以：

```bash
./deploy/bin/multica --profile profile-a config set server_url http://127.0.0.1:8006
./deploy/bin/multica --profile profile-a config set app_url   http://192.0.2.10:8004
./deploy/bin/multica --profile profile-a login --token mul_xxx    # token 从 Web 端「添加电脑」获取
./deploy/bin/multica --profile profile-a config set workspace_id <工作区 id>
```

手工写的 profile 同样按登录邮箱与自动登记的那一份配对。`workspace_id` 必须是这个账号真的能看到的那个
（`./deploy/bin/multica --profile profile-a workspace list` 可查），否则页面上该账号会带一条"工作区不在
可见范围"的告警；留空则会在 Bridge 第一次读到该 profile 时自动选中可见的第一个工作区并写回。

重建后还需要两件仓库之外的事：一是让本机能访问 GitHub（把 `deploy/ssh-config.github` 追加到
`~/.ssh/config` 顶部，并放入对应的私钥）；二是确认如流机器人的可见范围包含所有需要登录的人。

## 8. 日常运维与排障

- **改端口**：改 `deploy/host.env` → `./deploy/build.sh`（Web 构建时会写入 API 地址）→
  `./deploy/stop.sh && ./deploy/start.sh` → `./deploy/status.sh`。
- **只重启**：`./deploy/stop.sh && ./deploy/start.sh`；Bridge 单独重启用 `bridgectl.py restart`。
- **Web 页面打不开**：先看 `deploy/logs/web.log`；再确认 `curl http://127.0.0.1:8004/health`
  能通（该路径会被代理到 API，能通说明 Web→API 链路正常）。
- **收不到验证码**：看 `infoflow-bridge/logs/bridge.log`。机器人不在可见范围会报 `40070`；
  邮箱前缀与如流用户名不一致也会失败（默认假设 `someone@example.com` → `someone`）。
- **绑定页显示「Multica 未登录」或机器人列表为空**：本机还没有这个登录邮箱的 CLI profile。正常路径是在
  Web 端（`8004`）用同一个邮箱登录一次，服务端会自动写好；也可以按 §7 手工补一个。想确认服务端到底
  写没写、账号认没认出来，可在 `infoflow-bridge/` 目录里用
  `node --input-type=module -e 'import {Accounts} from "./src/accounts.js"; console.log(await new Accounts().describeAll())'`
  直接确认账号是否已被识别。
- **Bridge 重启后管理页需要重新登录**：会话保存在内存里，这是既有设计。
- **构建报 `Upstream changed`**：上游对应代码变了，按提示改 `overlays/web-patch.py` 的锚点。
- **构建报子模块不干净**：说明有人改了 `multica/` 里的文件，先 `git -C multica checkout -- .` 或把改动挪进 overlay。
- **Bridge 里的任务机器人连接超时**：`connect failed: Request timeout` 属于如流侧网络/长连接问题，
  验证码机器人通常不受影响，可稍后重试或检查该机器人的可见范围与权限。
- **daemon 加不到服务端、日志里是 `502`（或 `login --token` 报 "token 无效"）**：多半是这台机器设了
  `http_proxy`/`https_proxy`，把发往部署地址的请求也交给代理了，而代理到内网这台机器是间歇性 502。
  症状很好认：`curl http://<部署地址>:8006/health` 时通时不通，加 `--noproxy "*"` 立刻 200，而服务端
  `deploy/logs/api.log` 里根本没有这次请求。修法是把部署地址加进 `NO_PROXY`
  （`export NO_PROXY="<部署地址>,${NO_PROXY}"`，写进 `/etc/environment` 或登录 profile 更省事），
  或者启动 daemon 时 `env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY …`。
- **智能体回「Not logged in · Please run /login」或"模型账号登录已失效"**：先确认这台机器是不是
  用 `runtime_shims` 覆盖了那个 CLI（见增量列表第 12 条）——`multica --profile <名字> config show`
  里的 `runtime_shims` 应为出问题的命令名（如 `ducc`），daemon 日志里应有
  `runtime shim installed` 与 `command_path=~/.multica/shims/<命令>`。shim 是**在跑 daemon 的那台
  客户端机器上**生成的，服务端不参与；`ducx` 之类本来就正常的 CLI 不要写进名单。
- **daemon 启动报 `no agent CLI found`**：这是 `daemon_runtimes` 白名单把内置探测过滤空了；
  `0.4.42-overlay.4` 起这种情况不再报错（显式白名单视为"只要自定义运行时"），旧二进制请升级，
  或先把白名单清空（`multica config set daemon_runtimes ""`）再启动。

## 9. Git 工作流

部署机上已配置好直连 GitHub（SSH over 443，见 `deploy/ssh-config.github`），日常同步直接在服务器上完成：

```bash
cd <仓库目录>
git fetch origin && git merge --ff-only origin/main    # 拉取
git add -A && git commit -m "..." && git push origin main   # 提交推送
```

约定：

- 按**常规提交历史**维护：一个改动一个 commit，直接 `git push`，不需要 `--amend`、也不需要
  强制推送（早期为了对外发布曾把历史压成单提交，现在不再这样做）；
- 提交前保持 `git -C multica status --short` 为空；
- `deploy/host.env`、`deploy/secrets.env`、`infoflow-bridge/config.local.json`（以及
  `config.local.json.before-*` 这类备份）是机器本地文件，永远不要提交，改动只留在机器上；
- `.web-build/`、`deploy/bin/`、`deploy/logs/`、`deploy/data/`、`overlays/go-overlay.json`、
  `overlays/upstream.json`、`infoflow-bridge/logs/` 等运行期产物保持忽略。

## 10. CI：多平台 daemon 构建

`.github/workflows/build-daemon.yml` 在 push、PR、打 tag 或手动触发时，用本仓库的 overlay
构建 `multica` CLI（**daemon 就在这个二进制里**，没有单独的 daemon 可执行文件）：

- 目标平台与上游 `.goreleaser.yml` 一致：darwin / linux / windows × amd64 / arm64
- 编译参数与上游 `make build`、goreleaser 一致：`CGO_ENABLED=0`、`-trimpath`、
  `-ldflags "-s -w -X main.version=… -X main.commit=… -X main.date=…"`
- 版本号形如 `0.4.42-overlay.<team 仓库短 sha>`，commit 带 `-overlay` 后缀，方便与官方发行版区分
- 产物：`dist/multica-<os>-<arch>` 原始二进制，以及 `multica_<os>_<arch>.tar.gz`
  （Windows 为 `.zip`，压缩包内可执行文件名是 `multica`）
- 打 `v*.*.*` tag 时额外生成 `checksums.txt` 并创建 Release
- 另一个 job 先用 overlay 做编译检查（`go test -overlay=… -run '^$'`），并断言构建后
  `multica/` 子模块仍然干净——和 `deploy/build.sh` 的约束保持一致

**客户端机器装的就是这个二进制**（daemon 跑在里面）。仓库自带 `scripts/install.sh`，会按平台和架构
从 Releases 下载对应压缩包、校验 `checksums.txt`、装到 `/usr/local/bin`（不可写且 sudo 不可用时
自动落到 `~/.local/bin` 并写进 shell 配置）：

```bash
curl -fsSL https://raw.githubusercontent.com/<owner>/<repo>/main/scripts/install.sh | bash
# 不想输 sudo 密码就指定用户目录：
curl -fsSL https://raw.githubusercontent.com/<owner>/<repo>/main/scripts/install.sh | MULTICA_BIN_DIR=$HOME/.local/bin bash
```

「添加电脑」对话框里那条安装命令就是它——把 `deploy/host.env` 的
`MULTICA_CLI_INSTALL_URL` 指到本仓库的 `scripts/install.sh`，页面就会下发这个地址（见 §4.2）。
也可以用 `--version <tag>` / `MULTICA_VERSION=<tag>` 固定某个版本，例如 `v0.4.42-overlay.4`。

在 macOS 上手工替换 CI 产物（daemon 宿主就是跑这个二进制）：

```bash
# 从仓库 Actions 页面下载 multica_darwin_arm64.tar.gz
tar -xzf multica_darwin_arm64.tar.gz
install -m 0755 multica /opt/homebrew/bin/multica   # 覆盖 Homebrew 版
multica daemon restart                              # 或让它自动跟随
```

daemon 会比较自身版本与它将要重新执行的 `multica --version`，发现不同就在当前任务结束后
自动切到新二进制；要关掉这个行为可以用 `MULTICA_DAEMON_AUTO_RELOAD=0`、
`multica daemon start --no-auto-reload`，或 `multica config set disable_auto_reload true`。

这也是修"模型下拉列表与本地实际可用模型不一致"的通道：daemon 侧的 `codex debug models`
调用被 overlay 改过（见 `overlays/go/files/server/pkg/agent/thinking.go`），只有用本仓库
构建的二进制才会带上这个修复。

## 11. 版本与发布记录

CLI/daemon 走 tag 发版（`v*.*.*` → CI 构建 + Release），服务端与 Web 的改动随 `main` 部署即可，
不需要新版本号。以下是已经发布过的 tag 及其中影响客户端的那部分内容：

| Tag | 客户端侧内容 |
| --- | --- |
| `v0.4.42-overlay.1` | 首个 overlay 构建：技能三级作用域、运行时 profile 归属、模型目录、`--profile` 隔离 |
| `v0.4.42-overlay.2` | `daemon_runtimes` 白名单（只注册名单内的内置运行时）；安装脚本的可写性判断与 `~/.local/bin` 回退 |
| `v0.4.42-overlay.3` | 显式白名单允许把内置探测过滤空（不再报 `no agent CLI found`） |
| `v0.4.42-overlay.4` | `runtime_shims`：为声明过的命令自动生成包装器（`~/.multica/shims/<命令>`），修 `--verbose` + `--settings` 同时出现时 CLI 掉登录态的问题 |

保持升级到最新的办法就是让客户端装最新 Release（`scripts/install.sh` 默认取 Latest，或按
`MULTICA_VERSION` 指定）；daemon 侧的自动跟随见上一节。
