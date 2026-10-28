# 自动接入机器和智能体

## 查询安装脚本

调用 `POST /api/integrations/machine-provision/command`，使用接入服务专用 Bearer：

```json
{"username":"zhangsan","machine_name":"zhangsan-dev","workspace_name":"pdc-loop"}
```

只有 `username` 必填。`workspace_name` 可选，未传或留空时默认 `pdc-loop`。接口准备账号，在同一事务中把用户加入指定的现有工作区（新成员角色为 `member`），并准备用户登录凭据、返回 Bash 脚本；已有成员的 owner/admin/member 角色保持不变。目标按工作区显示名称精确匹配，不存在返回 404，同名目标不唯一返回 409，失败时不会留下部分账号或成员关系。接口不创建工作区，也不运行脚本。原来的 `agent_name`、`runtime`、`model` 和 `thinking_level` 请求字段不再接受。

脚本在对应用户的 Linux/macOS 机器上执行与网页相同的安装命令，默认使用 GitHub 的 Latest Release；不额外查询版本或预检命令。需要指定版本时，执行方可自行在环境中设置 `MULTICA_VERSION`。脚本先非交互登录，再通过原有 CLI 复用所选工作区，并重启该 profile 的 daemon，同时执行 `ducc config model 'Opus 4.8'`。脚本不修改该 profile 的默认工作区；如果执行环境已设置 `MULTICA_TOKEN`，后续 CLI 命令会优先使用该环境变量。传入机器名称时，还会重命名机器并创建或复用同名智能体，绑定本机 `ducc`；不传时跳过这两步。所选 CLI 版本必须支持这些新增命令。

脚本包含用户的长期登录凭据，只能通过受保护的通道传给目标机器。不要输出脚本或凭据到日志、Git 或公共制品。

API 加入工作区后会通知该用户正在运行的 daemon 刷新工作区列表，无需重启。独立 CLI 的工作区创建/复用规则保持原样；如果成员关系在脚本执行前被移除，仍按原有规则处理。

## 独立 CLI 命令

```bash
multica --profile zhangsan tmesh-workspace ensure --name 'pdc-loop'
multica --profile zhangsan tmesh-machine rename \
  --workspace-name 'pdc-loop' --machine-name 'zhangsan-dev'
multica --profile zhangsan tmesh-machine agent \
  --workspace-name 'pdc-loop' --agent-name 'zhangsan-dev' --runtime ducc
```

为智能体绑定平台上已有的 skill：

```bash
multica --profile zhangsan tmesh-machine agent \
  --workspace-name 'pdc-loop' --agent-name 'zhangsan-dev' --runtime ducc \
  --skill-list 'search,report-writing'
```

`--skill-list` 接受逗号分隔的精确名称或完整 skill ID，也可重复传入。仅查找当前账号在目标工作区可绑定的 skill；不存在、不可访问、名称重名或空列表会报错，校验失败时不会创建、改绑或取消智能体任务，也不会修改机器名称。重名时请使用完整 ID。新建智能体会在创建时一并保存绑定；复用智能体时补充指定项并启用，保留其他绑定及配置。重复执行不会重复添加；不传该参数则保持原有行为。该参数不导入或创建 skill，也不改变安装命令 API 的请求字段。绑定期间接口失败会返回非零退出码，保留已完成步骤，修复后可重试。

- 工作区按当前用户可访问的精确名称查找。普通成员也可复用；多个匹配报错并列出候选项；没有匹配时创建，并让当前用户成为 owner。
- `--workspace-name` 可用于机器和智能体命令。省略时沿用环境变量或 profile 的默认工作区；没有默认值时需要指定。脚本不修改 profile 的默认工作区。
- 机器显示名称按本人和本机 daemon 身份保存，同一用户在不同工作区看到的名称一致；不会修改操作系统 hostname。
- 智能体只绑定本人在目标工作区、本机上在线的指定运行时。默认等待最多 90 秒，可用 `--wait` 调整；不会自动切换到其他运行时。
- 新智能体对工作区可见。同名智能体属于本人时复用 ID、历史和配置，已归档的会恢复；属于其他人或多个同名项时停止。
- 同名智能体需要改绑时，先取消其运行中和排队的任务，再更新绑定。已完全一致时不会重复取消或更新。
- 并发创建或改绑同一工作区的同名智能体时，一个操作持有协调锁，另一个返回冲突；锁连接断开时释放，后续可重试。工作区准备使用服务端事务锁，避免同一用户并发创建重复工作区。
- 中途失败会立即停止并保留已完成的操作。执行方通过进程退出码判断结果：0 表示各步骤正常结束，非零表示失败。各条 CLI 命令仍可输出各自的结果，不再汇总为单个 JSON。

原 `tmesh-machine enroll` 已移除。保存了旧安装脚本的调用方需要重新获取脚本；新脚本分别调用工作区、机器和智能体命令。
