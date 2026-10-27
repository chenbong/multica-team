import assert from "node:assert/strict";

// The Go HTTP/DB tests own tenant authorization. This transport double checks
// real-sender propagation without changing Chat session and receipt assertions.
export function installChatInputFixture(bridge, { bypassRelay = false } = {}) {
  const original = bridge.issueRelay.request.bind(bridge.issueRelay);
  bridge.issueRelay.enabled = true;
  if (bypassRelay) bridge.issueRelay.handle = async () => false;
  bridge.issueRelay.request = async (path, body) => {
    if (path !== "chat-input") return original(path, body);
    assert.equal(typeof body.sender, "string");
    assert.ok(body.sender.length > 0);
    assert.ok(body.agent_id);
    assert.ok(body.conversation?.id);
    assert.ok(Array.isArray(body.attachment_ids));
    if (body.create_issue) return bridge.multica.createIssue({ title: body.title, description: body.content, assignee: body.agent_id, attachments: [] });
    return bridge.multica.sendChatMessage(body.session_id, body.content, body.attachment_ids);
  };
}
