// @vitest-environment node
import { describe, expect, it } from "vitest";
import { execFileSync } from "node:child_process";
import { connectionCommand, profileNameFromEmail } from "./connection-commands";

describe("connection commands", () => {
  it.each(["macosLinux", "windows"] as const)("keeps numbered shell guidance for %s", (platform) => {
    const command = connectionCommand("fixture-installer", platform, {
      email: "user@example.test", token: "fixture-token",
      runtimeDefaults: "ducc", shimDefaults: "ducc",
    });
    const sections = [
      "# 1. 配置网络并安装 Multica CLI / Configure network and install Multica CLI",
      "# 2. 配置平台地址并登录 / Configure platform URLs and sign in",
      "# 3. 配置运行时并准备 ducc / Configure runtimes and prepare ducc",
      "# 4. 启动守护进程，连接平台 / Start the daemon and connect",
    ];
    expect(command.match(/^# .*$/gm)).toEqual(sections);
    expect(command.startsWith(`${sections[0]}\n`)).toBe(true);
    const bodies = command.split(/^# .*\n/gm).slice(1);
    expect(bodies[0]).toContain("fixture-installer");
    expect(bodies[1]).toContain("login --token 'fixture-token'");
    expect(bodies[2]).toContain("config set daemon_runtimes 'ducc'");
    expect(bodies[2]).toContain("config set runtime_shims 'ducc'");
    expect(bodies[2]).toContain("multica --profile user ducc setup");
    expect(bodies[2]).toContain("PATH");
    expect(bodies[2]).toContain("ducc config model 'Opus 4.8'");
    expect(bodies[3]?.trim()).toBe("multica --profile user daemon start");
  });

  it("combines the deployment installer, token, ducc and profile setup in shell order", () => {
    const command = connectionCommand("curl https://example.com/install.sh | bash", "macosLinux", {
      serverUrl: "http://api.example.test:8080/", appUrl: "http://app.example.test/",
      email: "Test.User@example.test", token: "fixture-token",
      runtimeDefaults: "ducc,codex", shimDefaults: "ducc",
      installUrl: "https://example.test/custom.sh",
    });
    execFileSync("bash", ["-n"], { input: command });
    expect(command).toContain('.bcebos.com,.duckdns.org,10.0.0.0/8${no_proxy:+,$no_proxy}');
    expect(command).toContain('export NO_PROXY="$no_proxy"');
    expect(command).toContain("curl -fsSL 'https://example.test/custom.sh' | bash");
    expect(command).toContain("multica --profile test.user config set server_url 'http://api.example.test:8080'");
    expect(command).toContain("multica --profile test.user login --token 'fixture-token'");
    expect(command).toContain("config set daemon_runtimes 'ducc,codex'");
    expect(command.indexOf("ducc setup")).toBeLessThan(command.indexOf('export PATH='));
    expect(command.indexOf('export PATH=')).toBeLessThan(command.indexOf("ducc config model 'Opus 4.8'"));
    expect(command).toContain('$HOME/.baidu-cc/baidu-cc/bin:$HOME/.comate/baidu-cc/bin:$PATH');
    expect(command.trim()).toMatch(/multica --profile test.user daemon start$/);
    expect(command).not.toContain("setup self-host");
  });

  it("keeps the upstream Windows installer tab and uses PowerShell syntax", () => {
    const install = "irm https://example.test/install.ps1 | iex";
    const command = connectionCommand(install, "windows", { email: "user@example.test", token: "fixture" });
    expect(command).toContain(install);
    expect(command).toContain('$env:NO_PROXY = $env:no_proxy');
    expect(command).toContain('$env:PATH = "$env:USERPROFILE\\.baidu-cc');
    expect(command).toContain("multica --profile user ducc setup");
    expect(command).toContain("ducc config model 'Opus 4.8'");
    expect(command).not.toContain("export ");
  });

  it("quotes deployment values and uses a visible placeholder when token creation fails", () => {
    const command = connectionCommand("true", "macosLinux", {
      serverUrl: "https://example.test/a'$(printf test)", appUrl: "https://app.example.test",
      email: "../../Unsafe User@example.test",
    });
    execFileSync("bash", ["-n"], { input: command });
    expect(command).toContain("login --token '<YOUR_TOKEN>'");
    expect(command).toContain("'https://example.test/a'\"'\"'$(printf test)'");
    expect(profileNameFromEmail("../../Unsafe User@example.test")).toBe("unsafe-user");
    expect(profileNameFromEmail(undefined)).toBe("");
  });
});
