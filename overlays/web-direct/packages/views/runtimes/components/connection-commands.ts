import type { CliInstallPlatform } from "../../common/cli-install-command";

export interface ConnectionCommandOptions {
  serverUrl?: string;
  appUrl?: string;
  email?: string;
  token?: string;
  installUrl?: string;
  windowsInstallUrl?: string;
  runtimeDefaults?: string;
  shimDefaults?: string;
}

export function profileNameFromEmail(email: string | undefined): string {
  return (email ?? "").split("@")[0]!.toLowerCase()
    .replace(/[^a-z0-9._-]/g, "-")
    .replace(/^[-._]+|[-._]+$/g, "");
}

function shellQuote(value: string): string {
  return "'" + value.replace(/'/g, "'\"'\"'") + "'";
}

function powershellQuote(value: string): string {
  return "'" + value.replace(/'/g, "''") + "'";
}

/** One copy operation installs, signs in with a token, and starts the daemon. */
export function connectionCommand(
  installCommand: string,
  platform: CliInstallPlatform,
  options: ConnectionCommandOptions,
): string {
  const server = options.serverUrl?.trim().replace(/\/+$/, "");
  const app = options.appUrl?.trim().replace(/\/+$/, "");
  const serverUrl = server && app ? server : "https://api.multica.ai";
  const appUrl = server && app ? app : "https://multica.ai";
  const profile = profileNameFromEmail(options.email);
  const cli = `multica${profile ? ` --profile ${profile}` : ""}`;
  const windows = platform === "windows";
  const quote = windows ? powershellQuote : shellQuote;
  const installUrl = windows ? options.windowsInstallUrl : options.installUrl;
  const install = installUrl
    ? windows
      ? `irm ${quote(installUrl)} | iex`
      : `curl -fsSL ${quote(installUrl)} | bash`
    : installCommand;
  const defaults = [
    options.runtimeDefaults && `${cli} config set daemon_runtimes ${quote(options.runtimeDefaults)}`,
    options.shimDefaults && `${cli} config set runtime_shims ${quote(options.shimDefaults)}`,
  ].filter(Boolean).join("\n");
  const network = windows
    ? `$env:no_proxy = ".bcebos.com,.duckdns.org,10.0.0.0/8" + $(if ($env:no_proxy) { "," + $env:no_proxy } else { "" })
$env:NO_PROXY = $env:no_proxy
$env:https_proxy = "http://agent.baidu.com:8188"
$env:http_proxy = $env:https_proxy`
    : `export no_proxy=".bcebos.com,.duckdns.org,10.0.0.0/8\${no_proxy:+,$no_proxy}"
export NO_PROXY="$no_proxy"
export https_proxy="http://agent.baidu.com:8188"
export http_proxy="$https_proxy"`;
  const path = windows
    ? '$env:PATH = "$env:USERPROFILE\\.baidu-cc\\baidu-cc\\bin;$env:USERPROFILE\\.comate\\baidu-cc\\bin;$env:PATH"'
    : 'export PATH="$HOME/.baidu-cc/baidu-cc/bin:$HOME/.comate/baidu-cc/bin:$PATH"';

  return `# 1. 配置网络并安装 Multica CLI / Configure network and install Multica CLI
${network}

${install}

# 2. 配置平台地址并登录 / Configure platform URLs and sign in
${cli} config set server_url ${quote(serverUrl)}
${cli} config set app_url ${quote(appUrl)}
${cli} login --token ${quote(options.token || "<YOUR_TOKEN>")}
# 3. 配置运行时并准备 ducc / Configure runtimes and prepare ducc
${defaults ? `\n${defaults}\n` : ""}
${cli} ducc setup
${path}
ducc config model 'Opus 4.8'

# 4. 启动守护进程，连接平台 / Start the daemon and connect
${cli} daemon start`;
}
