import { mkdir, readFile, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { homedir } from "node:os";
import { join, resolve } from "node:path";
import { Accounts } from "./accounts.js";
import { AdminAuth } from "./auth.js";
import { DEFAULT_BASE_URL, ROOT, loadConfig, redactRobot, saveRobots } from "./config.js";
import { fetchRobotLink, fetchRobotName } from "./infoflow.js";
import { Multica } from "./multica.js";

const PAGE = resolve(ROOT, "public/index.html");
const SESSION_COOKIE = "bridge_admin";

// A local-only admin page: it edits config.local.json and asks the runtime to
// reconnect. It binds to loopback, and everything except the login endpoints
// needs a session obtained by receiving a one-time code in 如流.
export function startAdminServer({ runtime, logger = console }) {
  const accounts = new Accounts(logger);
  const auth = new AdminAuth({
    allowedDomains: runtime.config.admin.allowedDomains,
    allowEmails: runtime.config.admin.allowEmails,
    send: (user, content) => runtime.sendVerification(user, content),
    logger,
  });
  const server = createServer(async (request, response) => {
    try {
      if (!hostIsAllowed(request.headers.host, runtime.config.admin)) {
        send(response, 403, { error: "only reachable from this machine" });
        return;
      }
      await route(request, response, runtime, accounts, auth, logger);
    } catch (error) {
      logger.error(`admin request failed: ${error.message}`);
      send(response, 500, { error: error.message });
    }
  });

  server.listen(runtime.config.admin.port, runtime.config.admin.host, () => {
    logger.log(`admin page on http://${runtime.config.admin.host}:${runtime.config.admin.port}`);
  });
  return server;
}

async function route(request, response, runtime, accounts, auth, logger) {
  const url = new URL(request.url, "http://127.0.0.1");

  if (request.method === "GET" && (url.pathname === "/" || url.pathname === "/index.html")) {
    // The page ships no deployment address of its own: whatever this instance
    // is configured to serve is substituted in at request time. Values that are
    // not configured stay empty and the corresponding affordance disappears.
    const html = (await readFile(PAGE, "utf8")).replaceAll(
      "{{BRIDGE_PAGE_CONFIG}}",
      JSON.stringify(pageConfig(runtime)).replace(/</g, "\\u003c"),
    );
    response.writeHead(200, { "content-type": "text/html; charset=utf-8", "cache-control": "no-store" });
    response.end(html);
    return;
  }

  function pageConfig(runtime) {
    const config = runtime.config;
    const verification = (config.robots ?? []).find((robot) => robot.purpose === "verification");
    const domain = config.admin.allowedDomains[0] ?? "example.com";
    return {
      multicaWebUrl: config.multica.webUrl ?? "",
      verificationBotName: verification?.name ?? "",
      verificationBotChatUrl: verification?.chatUrl ?? "",
      emailPlaceholder: `you@${domain}`,
      infoflowConsoleUrl: config.admin.infoflowConsoleUrl ?? "",
    };
  }

  // Login: ask for a code, then trade email plus code for a session cookie.
  // These two are reachable without a session; everything after them is not.
  if (request.method === "POST" && url.pathname === "/api/login/code") {
    const input = await body(request);
    try {
      const { user } = await auth.requestCode(input.email);
      send(response, 200, { ok: true, user });
    } catch (error) {
      send(response, 400, { error: error.message });
    }
    return;
  }

  if (request.method === "POST" && url.pathname === "/api/login/verify") {
    const input = await body(request);
    try {
      const session = auth.verify(input.email, input.code);
      response.writeHead(200, {
        "content-type": "application/json; charset=utf-8",
        "cache-control": "no-store",
        // HttpOnly keeps the token out of page scripts; SameSite=Strict stops
        // another origin from driving these endpoints through the browser.
        "set-cookie": `${SESSION_COOKIE}=${session.token}; Path=/; HttpOnly; SameSite=Strict; Max-Age=${session.maxAgeSeconds}`,
      });
      response.end(JSON.stringify({ ok: true, email: session.email }));
    } catch (error) {
      send(response, 400, { error: error.message });
    }
    return;
  }

  if (request.method === "POST" && url.pathname === "/api/logout") {
    auth.logout(cookie(request, SESSION_COOKIE));
    response.writeHead(200, {
      "content-type": "application/json; charset=utf-8",
      "cache-control": "no-store",
      "set-cookie": `${SESSION_COOKIE}=; Path=/; HttpOnly; SameSite=Strict; Max-Age=0`,
    });
    response.end(JSON.stringify({ ok: true }));
    return;
  }

  const session = auth.session(cookie(request, SESSION_COOKIE));
  if (!session) {
    send(response, 401, {
      error: "请先登录",
      login: { domains: runtime.config.admin.allowedDomains, robot: Boolean(runtime.verificationRobot()) },
    });
    return;
  }

  // Linking the page's own Multica account to this machine. The page session
  // already proved the e-mail through an IM one-time code, so these two routes
  // only ever act for that address — there is no way to link somebody else's
  // account from here. The token they end up storing is the same per-user PAT
  // the "add a computer" dialog hands out, so both paths share one credential.
  if (request.method === "POST" && url.pathname === "/api/multica/login-code") {
    try {
      await requestMulticaCode(runtime, session);
      send(response, 200, { ok: true });
    } catch (error) {
      send(response, 502, { error: error.message });
    }
    return;
  }

  if (request.method === "POST" && url.pathname === "/api/multica/verify") {
    try {
      const input = await body(request);
      send(response, 200, await linkMulticaAccount(runtime, accounts, session, input));
    } catch (error) {
      send(response, 400, { error: error.message });
    }
    return;
  }

  if (request.method === "GET" && url.pathname === "/api/data") {
    // ?profile= selects which signed-in Multica account the page is looking
    // through; omitting it keeps the CLI default.
    send(response, 200, await snapshot(runtime, accounts, url.searchParams.get("profile"), session));
    return;
  }

  if (request.method === "POST" && url.pathname === "/api/robot") {
    const input = await body(request);
    // The profile name becomes a CLI argument, and it also decides whose Multica
    // identity creates the issues, so it must be an account signed in on this
    // machine *and* belong to the person logged into this page.
    const { mine } = await visibleAccounts(accounts, session, runtime.config.multica.serverUrls);
    const account = mine.find((entry) => entry.profile === String(input.profile ?? "").trim());
    if (!account) {
      send(response, 400, { error: "这个 Multica 账号不是你的，或者它还没有在这台机器上登录" });
      return;
    }
    const robots = [...runtime.config.robots];
    const index = robots.findIndex((robot) => robot.id === input.id);
    const previous = index === -1 ? {} : robots[index];
    // The page only manages task robots, so it can neither turn one into the
    // shared verification robot nor reach that robot by guessing its id.
    if (previous.purpose === "verification") {
      send(response, 400, { error: "验证码机器人不在这个页面管理" });
      return;
    }

    // An empty secret field means "keep what is stored" — the page never sees the
    // current value, so it cannot send it back.
    const appSecret = String(input.appSecret ?? "").trim() === "" ? (previous.appSecret ?? "") : String(input.appSecret);
    // The IM open-platform API address is deployment configuration. Nothing in
    // the repository hardcodes it, so a missing value is an operator error
    // rather than something to guess.
    const baseUrl = String(input.baseUrl ?? "").trim() || previous.baseUrl || DEFAULT_BASE_URL;
    if (!baseUrl) {
      send(response, 400, {
        error: "缺少 IM 开放平台 API 地址：设置 INFOFLOW_BASE_URL，或在 config.local.json 的机器人条目里写 baseUrl",
      });
      return;
    }
    // The robot's name comes from 如流 rather than from a text field, which also
    // makes this the point where wrong credentials are caught.
    let name;
    try {
      name = await fetchRobotName({
        appId: String(input.appId ?? "").trim(),
        appKey: String(input.appKey ?? "").trim(),
        appSecret,
        baseUrl,
      });
    } catch (error) {
      send(response, 400, { error: `如流没认这套凭据：${error.message}` });
      return;
    }

    const merged = {
      ...previous,
      id: input.id || previous.id,
      name,
      purpose: "task",
      profile: account.profile,
      workspaceSlug: account.workspaceSlug ?? previous.workspaceSlug ?? "",
      appId: input.appId,
      appKey: input.appKey,
      appSecret,
      baseUrl,
      agent: input.agent,
      // Neither list is configured on the page anymore. Groups are open — every
      // member of a group the robot is in can @ it — and a direct chat is limited
      // to whoever created the robot, which is the one person we can name here.
      allowUsers:
        input.allowUsers === undefined
          ? (previous.allowUsers ?? [session.email.split("@")[0]])
          : Array.isArray(input.allowUsers)
            ? input.allowUsers
            : splitList(input.allowUsers),
      allowAllInGroups: input.allowAllInGroups === undefined ? true : input.allowAllInGroups === true,
      enabled: input.enabled !== false,
    };
    if (index === -1) robots.push(merged);
    else robots[index] = merged;
    saveRobots(robots);
    await runtime.sync(loadConfig());
    logger.log(`admin saved robot ${merged.name}`);
    send(response, 200, await snapshot(runtime, accounts, account.profile, session));
    return;
  }

  if (request.method === "POST" && url.pathname === "/api/robot/delete") {
    const input = await body(request);
    const target = runtime.config.robots.find((robot) => robot.id === input.id);
    if (target?.purpose === "verification") {
      send(response, 400, { error: "验证码机器人不在这个页面管理" });
      return;
    }
    if (target && !(await ownsRobot(accounts, session, target, runtime.config.multica.serverUrls))) {
      send(response, 400, { error: "这个机器人绑在别人的 Multica 账号上，不能在这里删" });
      return;
    }
    saveRobots(runtime.config.robots.filter((robot) => robot.id !== input.id));
    await runtime.sync(loadConfig());
    logger.log(`admin deleted robot ${input.id}`);
    send(response, 200, await snapshot(runtime, accounts, input.profile, session));
    return;
  }

  if (request.method === "POST" && url.pathname === "/api/robot/link") {
    const input = await body(request);
    const robot = runtime.config.robots.find((entry) => entry.id === input.id);
    if (!robot) {
      send(response, 404, { error: "没有这个机器人" });
      return;
    }
    if (!(await ownsRobot(accounts, session, robot, runtime.config.multica.serverUrls))) {
      send(response, 400, { error: "这个机器人绑在别人的 Multica 账号上，不能打开机器人对话" });
      return;
    }
    try {
      const url = await fetchRobotLink(robot);
      send(response, 200, { url });
    } catch (error) {
      send(response, 400, { error: `无法生成机器人对话链接：${error.message}` });
    }
    return;
  }

  if (request.method === "POST" && url.pathname === "/api/robot/test") {
    const input = await body(request);
    const robot = runtime.config.robots.find((entry) => entry.id === input.id);
    if (!robot) {
      send(response, 404, { error: "没有这个机器人" });
      return;
    }
    if (!(await ownsRobot(accounts, session, robot, runtime.config.multica.serverUrls))) {
      send(response, 400, { error: "这个机器人绑在别人的 Multica 账号上，不能在这里发测试消息" });
      return;
    }
    const target = String(input.user ?? "").trim() || robot.allowUsers[0];
    if (!target) {
      send(response, 400, { error: "先填一个允许使用的用户名，测试消息要有收件人" });
      return;
    }
    await runtime.sendDirect(robot.id, target, `**${robot.name}** 配置成功，绑定的 agent 是「${robot.agent}」。`);
    send(response, 200, { ok: true, target });
    return;
  }

  send(response, 404, { error: "not found" });
}

// The page always looks through exactly one signed-in Multica account: that
// account decides which agents can be bound, and the robot saved from the form
// dispatches as it. Everything an account can see is what Multica itself grants
// it — the page only adds the owner label and the "mine" flag on top.
async function snapshot(runtime, accounts, requestedProfile, session) {
  const config = runtime.config;
  // The verification robot is shared infrastructure for login codes, not
  // something anyone binds an agent to, so the page never shows it.
  const managed = config.robots.filter((robot) => robot.purpose !== "verification");
  // Only the Multica account whose email matches the page login is offered. The
  // machine may hold other people's CLI profiles; they are not this person's to
  // look through.
  const { mine: list, unresolved } = await visibleAccounts(accounts, session, config.multica.serverUrls);
  // Robots are private to the Multica account they are pinned to. The previous
  // response returned other users' robots as read-only rows, which still leaked
  // their names, agents, and delivery scope on the page.
  const visibleRobots = managed.filter((robot) => list.some((entry) => entry.profile === (robot.profile ?? "")));
  const wanted = String(requestedProfile ?? "").trim();
  const active =
    list.find((entry) => entry.profile === wanted) ??
    list.find((entry) => entry.profile === "") ??
    list[0] ??
    null;

  let agents = [];
  let agentsError = null;
  if (!active) {
    agentsError = `这台机器上没有以 ${session.email} 登录的 Multica 账号，先在终端里执行 multica --profile 名字 login --token mul_...`;
  } else if (active.error) {
    agentsError = `账号 ${active.label} 不可用：${active.error}`;
  } else {
    try {
      const [raw, members] = await Promise.all([
        new Multica({ ...config.multica, profile: active.profile }).listAgents(),
        accounts.members(active.profile),
      ]);
      agents = (raw ?? []).map((agent) => ({
        name: agent.name,
        model: agent.model,
        ownerId: agent.owner_id,
        ownerEmail: members[agent.owner_id]?.email ?? null,
        mine: Boolean(active.userId) && agent.owner_id === active.userId,
        visibility: agent.visibility,
      }));
    } catch (error) {
      agentsError = error.message;
    }
  }

  return {
    session: { email: session.email },
    accounts: list.map((entry) => ({ ...entry, robots: visibleRobots.filter((robot) => (robot.profile ?? "") === entry.profile).length })),
    // How many signed-in profiles could not be identified at all (expired token,
    // server down). Reported as a count so one person's page never names another
    // person's profile.
    unresolvedAccounts: unresolved,
    activeProfile: active?.profile ?? "",
    me: active ? { profile: active.profile, name: active.name, email: active.email, workspaceName: active.workspaceName } : null,
    robots: visibleRobots.map((robot) => ({
      ...redactRobot(robot),
      mineAccount: true,
    })),
    status: runtime.status().filter((entry) => visibleRobots.some((robot) => robot.id === entry.id)),
    agents,
    agentsError,
    webUrl: config.multica.webUrl,
  };
}

// Splits the machine's signed-in Multica accounts into "this person's" and
// "unidentifiable", keyed off the email they logged into the page with.
async function visibleAccounts(accounts, session, serverUrls = []) {
  const all = await accounts.describeAll();
  return splitAccounts(all, session, serverUrls);
}

// Same split, with the deployment's own API taken into account.
function splitAccounts(all, session, serverUrls) {
  const onThisServer = (entry) => serverUrls.length === 0 || serverUrls.includes(entry.serverUrl);
  return {
    all,
    mine: all.filter((entry) => (entry.email ?? "").toLowerCase() === session.email && onThisServer(entry)),
    unresolved: all.filter((entry) => !entry.email && onThisServer(entry)).length,
  };
}

async function ownsRobot(accounts, session, robot, serverUrls = []) {
  const { mine } = await visibleAccounts(accounts, session, serverUrls);
  return mine.some((entry) => entry.profile === (robot.profile ?? ""));
}

function splitList(value) {
  return String(value ?? "")
    .split(/[\s,，、]+/)
    .map((item) => item.trim())
    .filter(Boolean);
}

// Guards against a page on another origin resolving a hostname to this address
// and driving the API through the browser. Loopback is always accepted; a
// deployment reachable from elsewhere lists the names it answers to.
function hostIsAllowed(host, admin) {
  if (!host) return false;
  const name = host.toLowerCase().replace(/:\d+$/, "");
  if (["127.0.0.1", "localhost", "[::1]", "::1"].includes(name)) return true;
  return admin.publicHosts.includes(name);
}

// --- linking a Multica account -------------------------------------------
//
// A user logs into this page with an IM one-time code, then links the Multica
// account with the same address. Linking trades that address for the user's
// long-lived personal access token and writes the CLI profile the account list
// already reads, so nothing has to be installed on anybody's laptop.

async function requestMulticaCode(runtime, session) {
  const api = multicaApiBase(runtime);
  const response = await fetch(`${api}/auth/send-code`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ email: session.email }),
  });
  if (!response.ok) throw new Error(await readApiError(response, "发送验证码失败"));
}

async function linkMulticaAccount(runtime, accounts, session, input) {
  const api = multicaApiBase(runtime);
  const verification = await callMultica(api, "/auth/verify-code", {
    method: "POST",
    body: { email: session.email, code: String(input.code ?? "").trim() },
    errorLabel: "验证码校验失败",
  });
  // The session JWT is short-lived; exchange it for the per-user PAT that the
  // "add a computer" dialog also receives, so a user owns exactly one token.
  const issued = await callMultica(api, "/api/daemon-bootstrap-token", {
    method: "POST",
    token: verification.token,
    errorLabel: "获取长期令牌失败",
  });
  const workspaces = await callMultica(api, "/api/workspaces", {
    token: issued.token,
    errorLabel: "读取工作区失败",
  });
  const wanted = String(input.workspaceId ?? "").trim();
  const workspace = wanted ? workspaces.find((entry) => entry.id === wanted) : workspaces[0];
  if (!workspace) {
    if (workspaces.length > 1) {
      return {
        ok: false,
        needsWorkspace: true,
        workspaces: workspaces.map((entry) => ({ id: entry.id, name: entry.name, slug: entry.slug })),
      };
    }
    throw new Error("这个账号没有可用的工作区");
  }
  const profile = await writeMulticaProfile(runtime, accounts, session.email, issued.token, workspace);
  return { ok: true, profile, email: session.email, workspace: { slug: workspace.slug ?? "" } };
}

function multicaApiBase(runtime) {
  const url = String(runtime.config.multica.serverUrl ?? "").trim();
  if (!url) throw new Error("bridge 还没有配置 Multica 的 serverUrl");
  return url.replace(/\/+$/, "");
}

async function callMultica(api, path, { method = "GET", body, token, errorLabel }) {
  const headers = {};
  if (body !== undefined) headers["content-type"] = "application/json";
  if (token) headers.authorization = `Bearer ${token}`;
  const response = await fetch(`${api}${path}`, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!response.ok) throw new Error(await readApiError(response, errorLabel));
  return response.json();
}

async function readApiError(response, label) {
  try {
    const data = await response.json();
    if (data?.error) return `${label}：${data.error}`;
  } catch {
    // fall through to the status line
  }
  return `${label}（HTTP ${response.status}）`;
}

// One profile per account, named after the e-mail prefix; an account that is
// already linked keeps the profile directory it was linked under.
async function writeMulticaProfile(runtime, accounts, email, token, workspace) {
  const existing = (await accounts.describeAll()).find(
    (entry) => (entry.email ?? "").toLowerCase() === email.toLowerCase(),
  );
  const name = existing?.profile || profileSlug(email);
  const dir = join(process.env.MULTICA_HOME || join(homedir(), ".multica"), "profiles", name);
  await mkdir(dir, { recursive: true, mode: 0o700 });
  const config = {
    server_url: runtime.config.multica.serverUrl,
    app_url: runtime.config.multica.webUrl,
    token,
    workspace_id: workspace.id,
  };
  await writeFile(join(dir, "config.json"), JSON.stringify(config, null, 2) + "\n", { mode: 0o600 });
  return name;
}

function profileSlug(email) {
  const prefix = String(email)
    .split("@")[0]
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, "-")
    .replace(/^[-.]+|[-.]+$/g, "");
  return prefix || "account";
}

function cookie(request, name) {
  const header = request.headers.cookie;
  if (!header) return null;
  for (const part of header.split(";")) {
    const [key, ...rest] = part.trim().split("=");
    if (key === name) return rest.join("=");
  }
  return null;
}

function body(request) {
  return new Promise((resolvePromise, reject) => {
    const chunks = [];
    request.on("data", (chunk) => chunks.push(chunk));
    request.on("error", reject);
    request.on("end", () => {
      const raw = Buffer.concat(chunks).toString("utf8");
      if (raw.trim() === "") {
        resolvePromise({});
        return;
      }
      try {
        resolvePromise(JSON.parse(raw));
      } catch (error) {
        reject(new Error(`invalid JSON body: ${error.message}`));
      }
    });
  });
}

function send(response, status, payload) {
  response.writeHead(status, { "content-type": "application/json; charset=utf-8", "cache-control": "no-store" });
  response.end(JSON.stringify(payload));
}
