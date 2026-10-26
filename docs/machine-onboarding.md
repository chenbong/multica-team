# 自动接入机器和智能体

## 调用安装命令 API

沿用 `POST /api/integrations/machine-provision/command` 和现有专用 Bearer。
服务端只返回脚本，由开发机分配平台在对应用户的机器执行。

```json
{
  "username": "zhangsan",
  "machine_name": "zhangsan-dev",
  "agent_name": "开发助手",
  "runtime": "ducc",
  "model": "custom-model",
  "thinking_level": "high"
}
```

只有 `username` 必填。不传机器名称就不重命名；不传智能体名称就不创建或改绑。
`model`、`thinking_level` 未传时，新智能体沿用运行时默认值，已有智能体保留兼容值；
传入空字符串表示恢复默认值。运行时要求明确模型时，需要同时传入模型。

返回脚本依次安装 CLI、设置新客户端的默认工作区、登录、准备运行时、重启该用户的
daemon，再重命名机器、创建或复用智能体。重复执行也会重启 daemon。
Linux/macOS 和 CPU 架构由安装器自动识别，API 不需要系统参数。

响应包含用户登录凭据，使用受保护的文件传给对应开发机，不写入 Git、公共制品或任务日志。
专用服务 Bearer 只保存在调用后端，不下发给开发机；客户端后续操作使用该用户的登录凭据。

## 独立客户端命令

以下命令均使用当前登录账号与当前机器的稳定 daemon ID，不通过机器显示名称寻找机器。
请把示例 UUID 替换为 API 准备的用户同名工作区 ID。

```bash
multica --profile zhangsan tmesh-machine prepare \
  --workspace-id 11111111-1111-4111-8111-111111111111

multica --profile zhangsan \
  --workspace-id 11111111-1111-4111-8111-111111111111 \
  tmesh-machine rename --machine-name zhangsan-dev

multica --profile zhangsan \
  --workspace-id 11111111-1111-4111-8111-111111111111 \
  tmesh-machine agent --agent-name 开发助手 --runtime ducc \
  --model custom-model --thinking-level high

multica --profile zhangsan \
  --workspace-id 11111111-1111-4111-8111-111111111111 \
  tmesh-machine enroll --machine-name zhangsan-dev --agent-name 开发助手 --runtime ducc
```

- `prepare` 只在配置缺少默认工作区时补上，保留凭据及其他配置。
- `rename` 沿用平台机器重命名接口，跨工作区保持一致，不修改操作系统 hostname。
- `agent` 和 `enroll` 只选择本人在本机、本工作区的在线运行时；默认 `ducc`，也可明确指定
  `ducx` 或其他已注册命令。最多等待 90 秒，可用 `--wait` 调整；找不到时不会选择其他运行时。
- 工作区内同名智能体须属于当前用户。复用原 ID、历史、指令、Skill、环境变量等配置；
  已归档的解除归档。不同所有者、多个同名记录或准备期间发生并发修改时明确报错。
- 模型、思考强度兼容时保留；不兼容时恢复目标运行时默认值。明确传入的值优先。
  切换运行时或覆盖模型时先查询目标目录；查询失败或明确参数不兼容时不取消原任务。
- 需要改绑或更新执行配置时，先取消原有排队及运行中的执行，再更新智能体。
  已经完全一致的重复执行不会再取消任务或重写智能体。
- 不迁移旧机器的代码、未提交文件和本地会话；平台历史继续保留。
- 后续步骤失败时保留已完成步骤并返回失败，修正参数后可以重试。

## 发布顺序

先发布包含上述命令的 CLI，再更新 API，确保返回的脚本可以安装到所需版本。
安装器先验证 `tmesh-machine` 命令可用，再配置账号和启动 daemon。服务端升级不会替现有机器执行接入脚本。

## 权限与输出

独立命令默认使用当前 profile 的工作区，可用 `--workspace-id` 明确覆盖；普通工作区成员可操作自己拥有的本机和智能体。API 返回的脚本始终明确指定用户同名工作区。

安装脚本将安装、登录及 daemon 日志写到标准错误输出，标准输出仅返回一个 JSON 对象。成功时 `success=true`、退出码为 0；失败时 `success=false`、退出码非零，并返回 `failed_step`、`completed_steps` 和已有的机器、运行时、工作区及智能体 ID。CLI 的细分结果位于脚本结果的 `result` 字段。未完成的操作可以重试，已完成操作不回滚。JSON 和错误信息不包含登录凭据。

脚本默认将 CLI 安装到 `$HOME/.local/bin`，也可用 `MULTICA_BIN_DIR` 指定目录，避免安装后的命令被其他目录中的旧版 CLI 遮蔽。仅传用户名时也会等待并核对本机上线，不改机器名或创建智能体。
