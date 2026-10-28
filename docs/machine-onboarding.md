# 自动接入机器和智能体

## 查询安装脚本

调用 `POST /api/integrations/machine-provision/command`，使用接入服务专用 Bearer：

```json
{"username":"zhangsan","machine_name":"zhangsan-dev"}
```

只有 `username` 必填。接口准备账号和用户登录凭据，返回 Bash 脚本；它不创建工作区，也不运行脚本。原来的 `agent_name`、`runtime`、`model` 和 `thinking_level` 请求字段不再接受。

脚本在对应用户的 Linux/macOS 机器上安装最新已发布的稳定 CLI Release，检查所需命令，创建或复用用户名同名工作区，登录并重启该 profile 的 daemon。版本查询由平台缓存五分钟，不改变 GitHub 的 Latest 标记或主动升级已有客户端。它每次执行 `ducc config model 'Opus 4.8'`。传入机器名称时，还会重命名机器并创建或复用同名智能体，绑定本机 `ducc`；不传时跳过这两步。

脚本包含用户的长期登录凭据，只能通过受保护的通道传给目标机器。不要输出脚本或凭据到日志、Git 或公共制品。

## 独立 CLI 命令

```bash
multica --profile zhangsan tmesh-workspace ensure --name 'zhangsan'
multica --profile zhangsan tmesh-machine rename \
  --workspace-name 'zhangsan' --machine-name 'zhangsan-dev'
multica --profile zhangsan tmesh-machine agent \
  --workspace-name 'zhangsan' --agent-name 'zhangsan-dev' --runtime ducc
```

- 工作区按当前用户可访问的精确名称查找。普通成员也可复用；多个匹配报错并列出候选项；没有匹配时创建，并让当前用户成为 owner。
- `--workspace-name` 可用于机器和智能体命令。省略时沿用环境变量或 profile 的默认工作区；没有默认值时需要指定。脚本只为没有默认值的 profile 设置默认工作区。
- 机器显示名称按本人和本机 daemon 身份保存，同一用户在不同工作区看到的名称一致；不会修改操作系统 hostname。
- 智能体只绑定本人在目标工作区、本机上在线的指定运行时。默认等待最多 90 秒，可用 `--wait` 调整；不会自动切换到其他运行时。
- 新智能体对工作区可见。同名智能体属于本人时复用 ID、历史和配置，已归档的会恢复；属于其他人或多个同名项时停止。
- 同名智能体需要改绑时，先取消其运行中和排队的任务，再更新绑定。已完全一致时不会重复取消或更新。
- 并发创建或改绑同一工作区的同名智能体时，一个操作持有协调锁，另一个返回冲突；锁连接断开时释放，后续可重试。工作区准备使用服务端事务锁，避免同一用户并发创建重复工作区。
- 中途失败保留已完成的操作。CLI 默认输出 JSON；完整脚本的标准输出汇总为单个 JSON，普通日志写入标准错误，不包含登录凭据。

原 `tmesh-machine enroll` 已移除。保存了旧安装脚本的调用方需要重新获取脚本；新脚本分别调用工作区、机器和智能体命令。
