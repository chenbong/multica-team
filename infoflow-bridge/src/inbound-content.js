import { parseConversationBlocks, messageIdString } from "./conversation-context.js";

function blocksOf(value) {
  if (Array.isArray(value)) return value;
  if (!value || typeof value !== "object") return null;
  for (const key of ["body", "blocks", "content", "Content"]) {
    if (Array.isArray(value[key])) return value[key];
  }
  return null;
}

export function containsFile(blocks) {
  return (blocks || []).some(block => String(block?.type ?? "").toUpperCase() === "FILE");
}

export function privateContent(raw, event) {
  let value = raw.Content ?? raw.content ?? raw.Text ?? raw.text;
  if (typeof value === "string" && /image|richtext/.test(event) && /^[\[{]/.test(value.trim())) {
    try { value = JSON.parse(value); } catch { /* Retain literal content. */ }
  }
  const blocks = blocksOf(value) || blocksOf(raw.message) || blocksOf(raw);
  if (blocks) {
    const parsed = parseConversationBlocks(event === "private.image"
      ? blocks.map(v => v && typeof v === "object" && !v.type ? { ...v, type: "IMAGE" } : v) : blocks);
    return { ...parsed, unsupportedFile: event === "private.file" || containsFile(blocks) };
  }
  if (event === "private.image") {
    const images = Array.isArray(raw.Images) ? raw.Images : [value ?? raw.Image ?? raw.PicUrl];
    return { text: "", images: images.filter(v => v != null).map(v =>
      typeof v === "string" ? { type: "IMAGE", content: v, inline: true } : { type: "IMAGE", ...v }),
      unsupportedFile: false };
  }
  return { text: typeof value === "string" ? value : "", images: [], unsupportedFile: event === "private.file" };
}

export function createInboundMessageReader(client, appId, receiverType = 7) {
  const snapshots = new Map();
  return async (raw, event) => {
    if (event === "private.file") return privateContent(raw,event);
    const id = messageIdString(receiverType === 2 ? raw.message?.header?.messageid : raw.MsgId ?? raw.msgId);
    const sender = receiverType === 2 ? raw.message?.header?.fromuserid : raw.FromUserId ?? raw.fromUserId;
    const receiverId = receiverType === 2 ? raw.groupid ?? raw.groupId : appId;
    if (!id || typeof sender !== "string") return privateContent(raw,event);
    const key=JSON.stringify([receiverType,String(receiverId),sender,id]);
    if (!snapshots.has(key)) {
      const pending=(async()=>{
        try {
          let result;
          for(let attempt=0;attempt<3;attempt++) {
            try {
              result=await client.httpClient.get('/im/message/getSingleMsg?'+new URLSearchParams({
                fromId:sender,receiverId:String(receiverId),receiverType:String(receiverType),msgId:id,
              }), {disableRetry:true});
              if(result?.code!=='ok'||result?.data?.code!==200)throw Object.assign(new Error('message lookup rejected'),{
                permanent:[40001,40400,720001,720003].includes(result?.data?.code),
              });
              break;
            } catch(error) { if(error.permanent||attempt===2)throw error; }
          }
          const items=result.data.data?.items;
          if(!Array.isArray(items)||items.length!==1||String(items[0].msgId)!==id||
              (items[0].sender?.uuapId&&items[0].sender.uuapId!==sender)||items[0].deletedFlag)throw Error('message identity mismatch');
          if (items[0].msgType === 'file') return {...privateContent(raw,event),unsupportedFile:true};
          const blocks=items[0].content?.blocks;
          if(!Array.isArray(blocks))throw Error('message blocks unavailable');
          const originalBlocks=blocks.map(block=>
            ['REPLY','REPLYDATA','QUOTE'].includes(String(block?.type??'').toUpperCase())
              ? {...block,text:'',content:''}:block);
          const parsed=parseConversationBlocks(originalBlocks);
          return {...parsed,displayText:parseConversationBlocks(originalBlocks,{preserveHumanMentions:true}).text,unsupportedFile:containsFile(blocks)};
        } catch {
          const fallback=privateContent(raw,event);
          return {...fallback,images:[],mediaRejection:'消息原文暂时无法完整读取，未提交到任务，请稍后重新发送。'};
        }
      })();
      snapshots.set(key,pending);
      for(const oldest of [...snapshots.keys()].slice(0,Math.max(0,snapshots.size-200)))snapshots.delete(oldest);
    }
    return structuredClone(await snapshots.get(key));
  };
}
