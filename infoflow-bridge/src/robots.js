import { Client, WSClient } from "@baidu/infoflow-sdk-nodejs";
import { Bridge } from "./bridge.js";
import { ReviewNotifications } from "./review-notifications.js";
import { acknowledge } from "./reactions.js";
import { VerificationCodeRelay } from "./codes.js";
import { DEFAULT_WS_CONNECT_DOMAIN, DEFAULT_WS_GATEWAY, robotIsComplete } from "./config.js";

const INITIAL_RECONNECT_DELAY_MS = 5000;
const MAX_INITIAL_RECONNECT_DELAY_MS = 120000;

// Owns one InfoFlow connection per configured robot and keeps that set in step
// with whatever the admin page saved, so a change never needs a process restart.
export class RobotRuntime {
  constructor(config, logger = console) {
    this.config = config;
    this.logger = logger;
    this.instances = new Map();
    this.relay = null;
    this.reviewNotifications = new ReviewNotifications(this);
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
    await this.reviewNotifications.poll();
  }

  status() {
    return this.config.robots.map((robot) => {
      const instance = this.instances.get(robot.id);
      if (!robot.enabled) return { id: robot.id, state: "disabled" };
      if (!robotIsComplete(robot)) return { id: robot.id, state: "incomplete" };
      if (!instance) return { id: robot.id, state: "stopped" };
      // A verification robot only ever sends, so it holds no connection to report on.
      if (robot.purpose === "verification") return { id: robot.id, state: "ready" };

      const state = connectionState(instance.wsClient);
      if (instance.reconnecting || state === "reconnecting") {
        const result = { id: robot.id, state: "reconnecting", error: instance.error };
        const retryAttempt = Math.max(instance.retryAttempt, reconnectAttempts(instance.wsClient));
        if (retryAttempt > 0) result.retryAttempt = retryAttempt;
        if (instance.nextRetryAt) result.nextRetryAt = new Date(instance.nextRetryAt).toISOString();
        return result;
      }
      if (state === "connecting") return { id: robot.id, state: "connecting" };
      if (instance.error) return { id: robot.id, state: "error", error: instance.error };
      return {
        id: robot.id,
        state: "connected",
        connectedAt: instance.connectedAt ? new Date(instance.connectedAt).toISOString() : undefined,
      };
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
    this.reviewNotifications.stop();
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

    const bridge = new Bridge(robotConfig(this.config, robot), send, this.logger, {
      getImageAccessToken: () => client.getAccessToken(),
    });
    const instance = {
      robot,
      client,
      bridge,
      wsClient: null,
      fingerprint: fingerprint(robot),
      error: null,
      reconnecting: false,
      retryAttempt: 0,
      retryTimer: null,
      nextRetryAt: null,
      connectInFlight: false,
      stopping: false,
    };
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

    wsClient.on("error", (event) => {
      const error = event?.data?.error ?? event?.error ?? event;
      const message = errorMessage(error);
      if (instance.stopping) {
        this.logger.warn("[" + robot.name + "] ignored InfoFlow error while stopping: " + message);
        return;
      }
      instance.error = message;
      this.logger.error("[" + robot.name + "] InfoFlow error: " + message);
    });

    registerHandlers({ wsClient, client, bridge, robot, logger: this.logger });
    // Established connections use the SDK's reconnect loop (infinite by default).
    // Initial endpoint failures need a local retry loop because this SDK deliberately
    // does not start reconnect() after connect() rejects before the first connection.
    wsClient.on("connected", () => {
      const previousError = instance.error;
      const wasReconnecting = instance.reconnecting;
      if (instance.retryTimer) clearTimeout(instance.retryTimer);
      instance.retryTimer = null;
      instance.nextRetryAt = null;
      instance.error = null;
      instance.reconnecting = false;
      instance.retryAttempt = 0;
      instance.connectedAt = Date.now();
      if (wasReconnecting || previousError) {
        this.logger.log("[" + robot.name + "] reconnected after: " + (previousError || "retrying"));
      }
    });
    wsClient.on("disconnected", () => {
      if (instance.stopping) return;
      instance.reconnecting = true;
      instance.error = "连接已断开，正在自动重连";
      this.logger.warn("[" + robot.name + "] InfoFlow connection dropped");
    });

    try {
      await wsClient.connect();
      if (connectionState(wsClient) !== "connected") {
        throw new Error("连接未建立");
      }
      instance.error = null;
      instance.connectedAt = Date.now();
      this.logger.log("[" + robot.name + "] connected — messages dispatch to agent 「" + robot.agent + "」");
    } catch (error) {
      instance.error = errorMessage(error);
      instance.reconnecting = true;
      instance.retryAttempt += 1;
      this.logger.error("[" + robot.name + "] connect failed: " + instance.error + "（将自动重试）");
      this.#scheduleInitialReconnect(instance);
    }
  }

  #scheduleInitialReconnect(instance) {
    if (instance.stopping || instance.retryTimer || instance.connectInFlight) return;
    instance.reconnecting = true;
    const attempt = Math.max(instance.retryAttempt, 1);
    const delay = initialReconnectDelay(attempt);
    instance.nextRetryAt = Date.now() + delay;
    this.logger.warn(
      "[" + instance.robot.name + "] retry " + attempt + " scheduled in " + delay + "ms",
    );
    instance.retryTimer = setTimeout(() => {
      instance.retryTimer = null;
      instance.nextRetryAt = null;
      void this.#retryInitialConnect(instance);
    }, delay);
    instance.retryTimer.unref?.();
  }

  async #retryInitialConnect(instance) {
    if (instance.stopping || !this.instances.has(instance.robot.id)) return;
    instance.connectInFlight = true;
    try {
      await instance.wsClient.connect();
      if (connectionState(instance.wsClient) !== "connected") {
        throw new Error("连接未建立");
      }
    } catch (error) {
      instance.error = errorMessage(error);
      instance.reconnecting = true;
      instance.retryAttempt += 1;
      this.logger.error(
        "[" + instance.robot.name + "] retry failed: " + instance.error,
      );
    } finally {
      instance.connectInFlight = false;
    }
    if (instance.stopping || !this.instances.has(instance.robot.id)) return;
    if (connectionState(instance.wsClient) !== "connected") {
      this.#scheduleInitialReconnect(instance);
    }
  }

  async #stop(id) {
    const instance = this.instances.get(id);
    if (!instance) return;
    instance.stopping = true;
    instance.bridge.stop();
    if (instance.retryTimer) clearTimeout(instance.retryTimer);
    instance.retryTimer = null;
    instance.nextRetryAt = null;
    instance.reconnecting = false;
    this.instances.delete(id);
    if (!instance.wsClient) return;
    try {
      instance.wsClient.disconnect();
      this.logger.log("[" + instance.robot.name + "] disconnected");
    } catch (error) {
      this.logger.warn(
        "[" + instance.robot.name + "] disconnect failed: " + error.message,
      );
    }
  }
}

function connectionState(wsClient) {
  if (!wsClient) return "stopped";
  return typeof wsClient.getState === "function" ? wsClient.getState() : wsClient.state;
}

function reconnectAttempts(wsClient) {
  const value = Number(wsClient?.reconnectAttempts ?? 0);
  return Number.isFinite(value) ? value : 0;
}

function errorMessage(error) {
  const candidate = error?.data?.error ?? error?.error ?? error;
  const message = candidate instanceof Error ? candidate.message : String(candidate?.message ?? candidate ?? "");
  return message || "连接失败";
}

function initialReconnectDelay(attempt) {
  return Math.min(
    MAX_INITIAL_RECONNECT_DELAY_MS,
    INITIAL_RECONNECT_DELAY_MS * 2 ** Math.max(0, attempt - 1),
  );
}

// Restart a connection only when something it depends on changed; renaming the
// robot or editing an unrelated field should not drop the WebSocket.
function fingerprint(robot) {
  return [robot.purpose, robot.appKey, robot.appSecret, robot.appId, robot.baseUrl, robot.agent, robot.profile, robot.workspaceId, robot.allowAllInGroups, robot.allowUsers.join(",")].join("|");
}

function robotConfig(base, robot) {
  return {
    robotId: robot.id,
    reviewNotifications: true,
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

export function parseGroupBlocks(blocks) {
  const images = [];
  const parts = [];

  for (const block of Array.isArray(blocks) ? blocks : []) {
    const type = String(block?.type ?? "").toUpperCase();

    if (type === "IMAGE") {
      images.push(block);
      continue;
    }

    if (type === "REPLYDATA" || type === "REPLY" || type === "QUOTE") {
      if (Array.isArray(block.replyImages)) images.push(...block.replyImages.filter(Boolean));
    }

    if (type === "AT") continue;

    if (type === "LINK") {
      parts.push(block.label ?? block.url ?? block.href ?? "");
      continue;
    }

    parts.push(block.content ?? block.text ?? "");
  }

  return {
    text: parts.filter(Boolean).join(" ").trim(),
    images,
  };
}
function registerHandlers({ wsClient, client, bridge, robot, logger }) {
  for (const event of ["private.text", "private.markdown", "private.richtext", "private.image"]) {
    wsClient.on(event, async (message) => {
      const raw = message?.data?.raw ?? {};
      // Despite the names, FromUserId is the readable username and FromUserName is
      // a numeric uid. Replies are addressed by username.
      const user = raw.FromUserId ?? raw.FromUserName;
      const rawContent = raw.Content ?? raw.content ?? raw.Text ?? raw.text;
      const imageContent =
        event === "private.image" && typeof rawContent === "string" ? rawContent : "";
      const images = imageContent
        ? [{ type: "IMAGE", inline: true, content: imageContent }]
        : [];
      const text = event === "private.image" ? "" : rawContent;
      const body = String(text ?? "").trim() || (images.length > 0 ? "（图片消息，详见附件）" : "");
      if (!user || body === "") {
        logger.warn(`[${robot.name}] ${event} without a usable sender/content:`, JSON.stringify(raw).slice(0, 600));
        return;
      }
      logger.log(`[${robot.name}] <- ${user} (${event}): ${String(text).slice(0, 200)}`);
      await bridge.handleMessage({
        conversation: { kind: "private", id: user },
        user,
        text: body,
        images,
        messageId: raw.MsgId ?? raw.msgId,
        acknowledge: () => acknowledge(client, raw, "private", robot.appId, logger),
      });
    });
  }

  for (const event of ["group.text", "group.markdown", "group.mixed", "group.image"]) {
    wsClient.on(event, async (message) => {
      const raw = message?.data?.raw ?? {};
      // MESSAGE_RECEIVE is the @robot / slash-command delivery. ALL_MESSAGE_FORWARD
      // copies every message in the group and would turn small talk into runs.
      if (raw.eventtype && raw.eventtype !== "MESSAGE_RECEIVE") return;

      const groupId = raw.groupid ?? raw.groupId;
      const header = raw.message?.header ?? {};
      const user = header.fromuserid ?? raw.fromuserid;
      const blocks = Array.isArray(raw.message?.body) ? raw.message.body : [];
      const mentionsRobot = blocks.some((block) => String(block?.type ?? "").toUpperCase() === "AT" && (block.robotid !== undefined || block.name));
      const parsed = parseGroupBlocks(blocks);
      const text = parsed.text || (parsed.images.length > 0 ? "（图片消息，详见附件）" : "");

      if (!groupId || !user || text === "" || !mentionsRobot) return;
      logger.log(`[${robot.name}] <- ${user} (${event} group ${groupId}): ${text.slice(0, 200)}`);
      await bridge.handleMessage({
        conversation: { kind: "group", id: groupId },
        user,
        text,
        images: parsed.images,
        messageId: header.messageid ?? raw.messageid,
        acknowledge: () => acknowledge(client, raw, "group", robot.appId, logger),
      });
    });
  }
}
