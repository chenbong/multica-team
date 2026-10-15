// @vitest-environment node
import { describe, expect, it } from "vitest";
import { ChatMessageSchema } from "./schemas";

describe("Shared chat sender schema", () => {
  const message = { id: "message", chat_session_id: "chat", role: "user", content: "hello" };

  it("accepts the actual sender as additive metadata", () => {
    expect(ChatMessageSchema.parse({ ...message, sender: { user_id: "participant", name: "Participant" } }).sender)
      .toEqual({ user_id: "participant", name: "Participant" });
  });

  it("keeps older and malformed sender payloads readable without inventing an author", () => {
    expect(ChatMessageSchema.parse(message).sender).toBeUndefined();
    const parsed = ChatMessageSchema.parse({ ...message, sender: { user_id: 9, name: false } });
    expect(parsed.content).toBe("hello");
    expect(parsed.sender).toBeUndefined();
    expect(ChatMessageSchema.parse({ ...message, sender: null }).sender).toBeNull();
  });
});
