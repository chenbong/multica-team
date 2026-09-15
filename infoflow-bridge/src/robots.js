import { Client, WSClient } from "@baidu/infoflow-sdk-nodejs";
import { Bridge } from "./bridge.js";
import { VerificationCodeRelay } from "./codes.js";
import { DEFAULT_WS_CONNECT_DOMAIN, DEFAULT_WS_GATEWAY, robotIsComplete } from "./config.js";

// Owns one InfoFlow connection per configured robot and keeps that set in step
// with whatever the admin page saved, so a change never needs a process restart.
export class RobotRuntime {
  constructor(config, logger = console) {
    this.config = config;
    this.logger = logger;
    this.instances = new Map();
    this.relay = null;
  }

  async sync(config = this.config) {
    this.config = config;
    const wanted = new Map(
      config.robots.filter((robot) => robot.enabled && robotIsComplete(robot)).map((robot) => [robot.id, robot]),
    );

    for (const [id, instance] of [...this.instances]) {
      const next = wanted.get(id);
      if (!next || instance.fingerprint !== fingerprint(next)) await this.#stop(id);
    }
    for (const [id, robot] of wanted) {
      if (!this.instances.has(id)) await this.#start(robot);
    }
    await this.#syncRelay();
  }

  status() {
    return this.config.robots.map((robot) => {
      const instance = this.instances.get(robot.id);
      if (!robot.enabled) return { id: robot.id, state: "disabled" };
      if (!robotIsComplete(robot)) return { id: robot.id, state: "incomplete" };
      if (!instance) return { id: robot.id, state: "stopped" };
      // A verification robot only ever sends, so it holds no connection to report on.
      if (robot.purpose === "verification") return { id: robot.id, state: "ready" };
      return { id: robot.id, state: instance.error ? "error" : "connected", error: instance.error };
    });
  }

  // Used by the admin page test button: proves the credentials and the send
  // permission without waiting for someone to message the robot.
  async sendDirect(robotId, user, content) {
    const instance = this.instances.get(robotId);
    if (!instance) throw new Error("这个机器人当前没有连接，先保存并启用它。");
    return instance.client.im.message.sendToUser(user, content, "md");
  }

  // One verification robot serves everybody: Multica's login codes and the admin
  // page's own login codes both go out through it, so nobody has to create their
  // own robot just to receive a code.
  verificationRobot() {
    return (
      this.config.robots.find(
        (entry) => entry.purpose === "verification" && entry.enabled && robotIsComplete(entry),
      ) ?? null
    );
  }

  async sendVerification(user, content) {
    const robot = this.verificationRobot();
    if (!robot) throw new Error("这台机器还没有配置验证码机器人，无法发送验证码");
    await this.sendDirect(robot.id, user, content);
    await this.#mirrorVerification(robot.id, user, content);
  }

  // A copy of every code can go to one or more shared groups. A group the robot
  // cannot post to (not a member, no permission) must not stop the person who is
  // waiting for their own code, so a failure is logged instead of thrown.
  async #mirrorVerification(robotId, user, content) {
    const groupIds = this.config.verificationMirrorGroupIds ?? [];
    const instance = this.instances.get(robotId);
    if (!instance || groupIds.length === 0) return;
    const mirroredContent = `发送给 ${user}\n\n${content}`;
    for (const groupId of groupIds) {
      try {
        await instance.client.im.message.sendToGroupWithOptions({
          groupId,
          msgtype: "MD",
          body: [{ type: "MD", content: mirroredContent }],
        });
        this.logger.log(`[${instance.robot.name}] verification message mirrored to group ${groupId}`);
      } catch (error) {
        this.logger.warn(`[${instance.robot.name}] verification mirror to group ${groupId} failed: ${error.message}`);
      }
    }
  }

  async stopAll() {
    for (const id of [...this.instances.keys()]) await this.#stop(id);
    this.relay?.stop();
    this.relay = null;
  }

  // Keeps the login-code relay pointed at whichever verification robot is enabled,
  // and stops it when there is none.
  async #syncRelay() {
    const robot = this.config.robots.find(
      (entry) => entry.purpose === "verification" && entry.enabled && robotIsComplete(entry),
    );
    if (!robot) {
      this.relay?.stop();
      this.relay = null;
      return;
    }
    if (this.relay?.robotId === robot.id) return;

    this.relay?.stop();
    this.relay = new VerificationCodeRelay({
      logPath: this.config.multica.apiLogPath,
      send: (user, content) => this.sendVerification(user, content),
      // Which e-mail domains this deployment relays for. It is the same
      // admin.allowedDomains the pages use, so the repository itself carries no
      // domain and an unconfigured clone simply relays nothing.
      allowedDomains: this.config.admin.allowedDomains,
      logger: this.logger,
    });
    this.relay.robotId = robot.id;
    await this.relay.start();
  }

  async #start(robot) {
    const client = new Client({
      appKey: robot.appKey,
      appSecret: robot.appSecret,
      agentId: robot.appId,
      baseUrl: robot.baseUrl,
    });

    const send = async (conversation, content, options = {}) => {
      if (conversation.kind === "group") {
        const body = [];
        if (options.atUser) body.push({ type: "AT", atall: false, atuserids: [options.atUser] });
        body.push({ type: "MD", content });
        return client.im.message.sendToGroupWithOptions({ groupId: conversation.id, msgtype: "MD", body });
      }
      // Single-chat markdown is lowercase "md"; the uppercase spelling belongs to
      // the group endpoint and is rejected here.
      return client.im.message.sendToUser(conversation.id, content, "md");
    };

    const bridge = new Bridge(robotConfig(this.config, robot), send, this.logger);
    const instance = { robot, client, bridge, wsClient: null, fingerprint: fingerprint(robot), error: null };
    this.instances.set(robot.id, instance);

    // A verification robot needs no inbound channel: opening one would both waste a
    // connection and accept messages nobody handles.
    if (robot.purpose === "verification") {
      this.logger.log(`[${robot.name}] ready — sends login codes only`);
      return;
    }

    const wsClient = new WSClient({
      appKey: robot.appKey,
      appSecret: robot.appSecret,
      // Only override what the deployment configured, so an unconfigured clone
      // keeps whatever gateway the SDK ships with.
      ...(DEFAULT_WS_GATEWAY ? { wsGateway: DEFAULT_WS_GATEWAY } : {}),
      ...(DEFAULT_WS_CONNECT_DOMAIN ? { wsConnectDomain: DEFAULT_WS_CONNECT_DOMAIN } : {}),
    });
    instance.wsClient = wsClient;

    registerHandlers({ wsClient, bridge, robot, logger: this.logger });
    // The SDK reconnects on its own (maxReconnectAttempts defaults to -1), so a
    // failed or dropped connection is transient. Keep the page's badge honest:
    // mark the disconnect while the SDK is retrying, and clear the recorded error
    // the moment a connection is (re)established. Without this, one startup
    // timeout left the page showing 「连接失败 · Request timeout after 30000ms」
    // forever — for a robot that was already dispatching messages again.
    wsClient.on("connected", () => {
      if (instance.error) this.logger.log(`[${robot.name}] reconnected after: ${instance.error}`);
      instance.error = null;
      instance.connectedAt = Date.now();
    });
    wsClient.on("disconnected", () => {
      instance.error = "连接已断开，正在自动重连";
      this.logger.warn(`[${robot.name}] InfoFlow connection dropped`);
    });

    try {
      await wsClient.connect();
      instance.error = null;
      instance.connectedAt = Date.now();
      this.logger.log(`[${robot.name}] connected — messages dispatch to agent 「${robot.agent}」`);
    } catch (error) {
      instance.error = error.message;
      this.logger.error(`[${robot.name}] connect failed: ${error.message}（SDK 会自动重连）`);
    }
  }

  async #stop(id) {
    const instance = this.instances.get(id);
    this.instances.delete(id);
    if (!instance) return;
    if (!instance.wsClient) return;
    try {
      instance.wsClient.disconnect();
      this.logger.log(`[${instance.robot.name}] disconnected`);
    } catch (error) {
      this.logger.warn(`[${instance.robot.name}] disconnect failed: ${error.message}`);
    }
  }
}

// Restart a connection only when something it depends on changed; renaming the
// robot or editing an unrelated field should not drop the WebSocket.
function fingerprint(robot) {
  return [robot.purpose, robot.appKey, robot.appSecret, robot.appId, robot.baseUrl, robot.agent, robot.profile, robot.workspaceId, robot.allowAllInGroups, robot.allowUsers.join(",")].join("|");
}

function robotConfig(base, robot) {
  return {
    robotId: robot.id,
    multica: {
      ...base.multica,
      // Each robot dispatches as its own Multica account, and links point at
      // the workspace its agent lives in — which is not necessarily the one the
      // CLI profile is pinned to.
      profile: robot.profile ?? "",
      workspaceId: robot.workspaceId || base.multica.workspaceId || "",
      workspaceSlug: robot.workspaceSlug || base.multica.workspaceSlug,
      defaultAgent: robot.agent,
    },
    allowUsers: robot.allowUsers,
    allowAllInGroups: robot.allowAllInGroups,
    userMap: {},
    pollIntervalMs: base.pollIntervalMs,
    pollTimeoutMs: base.pollTimeoutMs,
    statePath: base.statePath,
  };
}

function registerHandlers({ wsClient, bridge, robot, logger }) {
  for (const event of ["private.text", "private.markdown", "private.richtext"]) {
    wsClient.on(event, async (message) => {
      const raw = message?.data?.raw ?? {};
      // Despite the names, FromUserId is the readable username and FromUserName is
      // a numeric uid. Replies are addressed by username.
      const user = raw.FromUserId ?? raw.FromUserName;
      const text = raw.Content ?? raw.content ?? raw.Text ?? raw.text;
      if (!user || !text) {
        logger.warn(`[${robot.name}] ${event} without a usable sender/content:`, JSON.stringify(raw).slice(0, 600));
        return;
      }
      logger.log(`[${robot.name}] <- ${user} (${event}): ${String(text).slice(0, 200)}`);
      await bridge.handleMessage({
        conversation: { kind: "private", id: user },
        user,
        text,
        messageId: raw.MsgId ?? raw.msgId,
      });
    });
  }

  for (const event of ["group.text", "group.markdown", "group.mixed"]) {
    wsClient.on(event, async (message) => {
      const raw = message?.data?.raw ?? {};
      // MESSAGE_RECEIVE is the @robot / slash-command delivery. ALL_MESSAGE_FORWARD
      // copies every message in the group and would turn small talk into runs.
      if (raw.eventtype && raw.eventtype !== "MESSAGE_RECEIVE") return;

      const groupId = raw.groupid ?? raw.groupId;
      const header = raw.message?.header ?? {};
      const user = header.fromuserid ?? raw.fromuserid;
      const blocks = Array.isArray(raw.message?.body) ? raw.message.body : [];
      const mentionsRobot = blocks.some((block) => block.type === "AT" && (block.robotid !== undefined || block.name));
      const text = blocks
        .filter((block) => block.type === "TEXT" || block.type === "MD")
        .map((block) => block.content ?? "")
        .join(" ")
        .trim();

      if (!groupId || !user || text === "" || !mentionsRobot) return;
      logger.log(`[${robot.name}] <- ${user} (${event} group ${groupId}): ${text.slice(0, 200)}`);
      await bridge.handleMessage({
        conversation: { kind: "group", id: groupId },
        user,
        text,
        messageId: header.messageid ?? raw.messageid,
      });
    });
  }
}
