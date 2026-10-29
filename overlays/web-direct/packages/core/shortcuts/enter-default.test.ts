// @vitest-environment jsdom
import {afterEach,beforeEach,describe,expect,it,vi} from "vitest";
import {createShortcutChord} from "./definitions";
import {getShortcut,migrateShortcutState,useShortcutStore} from "./store";
beforeEach(()=>{
 const values=new Map<string,string>();
 vi.stubGlobal("localStorage",{getItem:(k:string)=>values.get(k)??null,setItem:(k:string,v:string)=>values.set(k,v),clear:()=>values.clear(),removeItem:(k:string)=>values.delete(k)});
});
afterEach(()=>{useShortcutStore.getState().resetAll();vi.unstubAllGlobals()});
describe("Enter send rollout",()=>{
 it("defaults to Enter and permits changing back to Mod+Enter",()=>{
  expect(getShortcut("send")).toEqual(createShortcutChord("Enter"));
  useShortcutStore.getState().setShortcut("send",createShortcutChord("Enter",{primary:true}));
  expect(getShortcut("send")).toEqual(createShortcutChord("Enter",{primary:true}));
 });
 it.each([0,1,2])("resets only Send in old browser version %s",version=>{
  const modern=version===2;
  const result=migrateShortcutState({overrides:{send:modern?null:"Mod+Enter",openSearch:modern?createShortcutChord("J",{primary:true}):"Mod+J",goInbox:null}},version);
  expect(result.overrides.send).toBeUndefined();expect(result.overrides.openSearch).toEqual(createShortcutChord("J",{primary:true}));expect(result.overrides.goInbox).toBeNull();
 });
 it("migrates actual browser storage once and keeps the later manual choice across reloads",async()=>{
  localStorage.setItem("multica_keyboard_shortcuts",JSON.stringify({version:2,state:{overrides:{send:null,goInbox:null}}}));
  await useShortcutStore.persist.rehydrate();
  expect(getShortcut("send")).toEqual(createShortcutChord("Enter"));expect(getShortcut("goInbox")).toBeNull();
  expect(JSON.parse(localStorage.getItem("multica_keyboard_shortcuts")!).version).toBe(3);
  useShortcutStore.getState().setShortcut("send",createShortcutChord("Enter",{primary:true}));
  await useShortcutStore.persist.rehydrate();expect(getShortcut("send")).toEqual(createShortcutChord("Enter",{primary:true}));
  expect(migrateShortcutState({overrides:{send:null}},3).overrides.send).toBeNull();
 });
});
