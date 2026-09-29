// @vitest-environment node
import { expect,it } from "vitest";
import type { AgentRuntime } from "@multica/core/types";
import { hasOnlineRegistration,findNewConnectedRuntime } from "./connection-result";

const row={id:"runtime",daemon_id:"new-computer",workspace_id:"ws",owner_id:"me",status:"online",runtime_mode:"local"} as AgentRuntime;
it("ignores empty, failed, refresh and malformed registration notifications",()=>{
 for(const payload of [null,{}, {runtime_id:"old"},{action:"removed"},{runtimes:[]},{runtimes:[{...row,status:"offline"}]},{runtimes:["bad"]}])expect(hasOnlineRegistration(payload)).toBe(false);
 expect(hasOnlineRegistration({runtimes:[row]})).toBe(true);
});
it("requires a new online computer belonging to this user and workspace",()=>{
 expect(findNewConnectedRuntime([row],new Set(),"ws","me")).toEqual(row);
 expect(findNewConnectedRuntime([row],new Set([row.daemon_id!]),"ws","me")).toBeUndefined();
 expect(findNewConnectedRuntime([row],new Set(),"another","me")).toBeUndefined();
 expect(findNewConnectedRuntime([row],new Set(),"ws","someone-else")).toBeUndefined();
 expect(findNewConnectedRuntime([{...row,status:"offline"}],new Set(),"ws","me")).toBeUndefined();
});
