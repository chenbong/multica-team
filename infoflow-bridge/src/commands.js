import { formatReply, noticeReplyMode, replyUrl, withReplyMode } from "./reply-format.js";
import { inlineMarkdown } from "./inline-markdown.js";

function responsibleAgentNames(names) {
  if (!Array.isArray(names)) return "";
  const rendered = [];
  for (let i = 0; i < names.length; i++) {
    if (typeof names[i] !== "string") throw new Error("invalid responsible agent name");
    const name = inlineMarkdown(names[i], 96);
    if (Buffer.byteLength([...rendered, name].join("，")) > 420) {
      rendered.push(`另 ${names.length - i} 个智能体`);
      break;
    }
    if (name) rendered.push(name);
  }
  return rendered.join("，") || "暂无智能体负责人";
}

// Static help is shared by authenticated relay commands and legacy Chat mode.
export const HELP = [
  "**Team Mesh 机器人**",
  "",
  "未绑定任务时普通消息是聊天；绑定后同步为任务评论。群里需要 @ 我。",
  "",
  "- `/help` 查看基本操作命令，无需指定任务",
  "- `/status` 群聊：查看本群绑定的全部任务；私聊：查看当前智能体参与的全部任务（含未绑定会话）",
  "- `/status <任务编号>` 查询当前对话绑定的指定任务",
  "- 任务较多时分页显示，用 `/status 2` 查看第 2 页",
  "- `/bind <任务编号>` 添加任务绑定；私聊仅支持单个智能体负责的任务",
  "- `/unbind <任务编号>` 解除绑定，不删除任务",
  "- `/issue <内容>` 创建并绑定任务",
  "- 关联多个任务时，回复对应任务消息，或在消息中带上任务编号",
  "- 未绑定任务的聊天可用 `/new` 开启新上下文、`/agents` 查看智能体、`/agent <名字>` 切换默认智能体",
].join("\n");

export function formatCommandNotice(multica, result) {
  const options = { mode: noticeReplyMode(result) };
  if (result.notice_kind === "help") return formatReply(HELP, options);
  const heading = result.notice;
  const pages = [[]];
  const choices = result.task_choices || [];
  for (const choice of choices) {
    if (!/^[A-Za-z0-9]{1,10}-\d+$/.test(choice.identifier) || !choice.workspace_slug) throw new Error("invalid task choice");
    const url = replyUrl(multica, { binding: choice }).replace(/[()]/g, c => c === "(" ? "%28" : "%29");
    const kinds = String(choice.binding_kinds || "").split(",").filter(Boolean).map(kind => ({ group: "群聊", private: "私聊" })[kind]).filter(Boolean);
    const status = result.notice_kind === "status";
    const title = status && choice.title ? " " + inlineMarkdown(choice.title) : "";
    const owners = status ? responsibleAgentNames(choice.agent_names) : "";
    const details = [owners, choice.status, kinds.join("、") || "未绑定会话"].filter(Boolean).join("；");
    const row = `- ${choice.identifier}${title}${status ? `（${details}）` : ""}：[完整对话链接](${url})`;
    // Budget whole rows, reserving room for a pagination hint. No task link
    // may be cut in half by the general long-answer summarizer.
    const fits = rows => Buffer.byteLength(withReplyMode(heading + "\n\n" + rows.join("\n"), options.mode)) <= 1600;
    if (!fits([row])) throw new Error("task link exceeds command reply budget");
    if (!fits([...pages.at(-1), row])) pages.push([]);
    pages.at(-1).push(row);
  }
  const page = result.notice_kind === "status" ? result.status_page || 1 : 1;
  if (!Number.isInteger(page) || page < 1) throw new Error("invalid status page");
  if (page > pages.length) return formatReply(`页码超出范围，共 ${pages.length} 页。请发送 /status 1。`, options);
  let content = heading + (choices.length ? "\n\n" + pages[page - 1].join("\n") : "");
  if (pages.length > 1) {
    content += result.notice_kind === "status"
      ? `\n\n共 ${choices.length} 个任务，第 ${page}/${pages.length} 页。${page < pages.length ? `发送 /status ${page + 1} 查看下一页。` : ""}`
      : "\n\n任务较多，其余任务请用 /status 分页查看。";
  }
  return formatReply(content, options);
}
