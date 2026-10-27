// @vitest-environment node
import { expect, it } from "vitest";
import type { Attachment } from "../types/attachment";
import { matchAttachmentByURL, collectImageSequence } from "../attachments/image-sequence";
import { AttachmentResponseSchema, ChatMessageSchema } from "./schemas";

it("resolves historical Markdown to the current private attachment without changing its content", () => {
 const old="http://old.example.test/uploads/inbound/42/image.png";
 const file={id:"11111111-1111-4111-8111-111111111111",url:"https://private.example.test/objects/migrated.png",download_url:"/api/attachments/11111111-1111-4111-8111-111111111111/download",legacy_urls:[old],filename:"image.png",content_type:"image/png"} as Attachment;
 const content=`Uploaded image: ![image](${old})`;
 expect(matchAttachmentByURL(old,[file])).toBe(file);
 const sequence=collectImageSequence([{content,attachments:[file]}]);
 expect(sequence[0]?.attachment).toBe(file);
 expect(content).toBe(`Uploaded image: ![image](${old})`);
});
it("validates alias metadata and tolerates older responses",()=>{
 expect(AttachmentResponseSchema.safeParse({id:"1",url:"/new",download_url:"/download",filename:"a",legacy_urls:["/old"]}).success).toBe(true);
 expect(AttachmentResponseSchema.safeParse({id:"1",url:"/new",download_url:"/download",filename:"a"}).success).toBe(true);
 const message=ChatMessageSchema.parse({id:"1",chat_session_id:"2",attachments:[{id:"3",legacy_urls:17}]});
 expect(message.attachments?.[0]?.legacy_urls).toEqual([]);
});
