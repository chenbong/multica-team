import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import type { ChatSession } from "@multica/core/types";
import enChat from "../../locales/en/chat.json";

const rename = vi.hoisted(() => vi.fn());
vi.mock("@multica/core/paths", () => ({ useWorkspacePaths: () => ({ agentDetail: (id: string) => `/agents/${id}` }) }));
vi.mock("@multica/core/chat/mutations", () => ({
  useUpdateChatSession: () => ({ mutate: rename }),
  useDeleteChatSession: () => ({ mutate: vi.fn() }),
  useSetChatSessionArchived: () => ({ mutate: vi.fn() }),
}));
vi.mock("@multica/core/chat", () => ({ useChatStore: (select: (s: { setActiveSession: () => void }) => unknown) => select({ setActiveSession: vi.fn() }) }));
vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: () => null }));
vi.mock("../../navigation", () => ({ AppLink: ({ children }: { children: React.ReactNode }) => <a>{children}</a> }));

import { ChatSessionHeader } from "./chat-session-header";

const session: ChatSession = {
  id: "chat", workspace_id: "workspace", agent_id: "agent", creator_id: "creator",
  title: "Shared conversation", status: "active", has_unread: false,
  created_at: "", updated_at: "",
};

describe("Shared chat destructive controls", () => {
  it.each([false, undefined])("hides owner actions with canManage=%s", (canManage) => {
    render(<I18nProvider locale="en" resources={{ en: { chat: enChat } }}>
      <ChatSessionHeader session={session} agent={null} canManage={canManage} />
    </I18nProvider>);
    expect(screen.getByRole("button", { name: session.title })).toBeDisabled();
    expect(screen.queryByRole("button", { expanded: false })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: session.title }));
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    expect(rename).not.toHaveBeenCalled();
  });

  it("retains creator rename controls", () => {
    render(<I18nProvider locale="en" resources={{ en: { chat: enChat } }}>
      <ChatSessionHeader session={session} agent={null} canManage />
    </I18nProvider>);
    fireEvent.click(screen.getByRole("button", { name: session.title }));
    expect(screen.getByRole("textbox", { name: enChat.header.rename })).toBeInTheDocument();
  });
});
