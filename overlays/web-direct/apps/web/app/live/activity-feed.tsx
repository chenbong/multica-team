"use client";
import {useEffect,useLayoutEffect,useRef} from "react";
import type {LucideIcon} from "lucide-react";

export type ActivityFeedItem={key:string;icon:LucideIcon;text:string;members:string[];time:string;color:string};
type Position={node:HTMLElement;top:number;left:number;signature:string};
const signature=(item:ActivityFeedItem)=>JSON.stringify([item.text,item.members]);

export function ActivityFeed({items}:{items:ActivityFeedItem[]}){
 const list=useRef<HTMLOListElement>(null),previous=useRef(new Map<string,Position>()),initialized=useRef(false);
 const anchor=useRef<{key:string;top:number}|null>(null),animations=useRef(new Set<Animation>());
 const revision=JSON.stringify(items.map(item=>[item.key,signature(item)]));
 const cancel=()=>{animations.current.forEach(a=>a.cancel());animations.current.clear();};
 const rememberAnchor=()=>{
  const root=list.current;if(!root)return;
  if(root.scrollTop<8){anchor.current=null;return;}
  const bounds=root.getBoundingClientRect();
  const row=Array.from(root.children).find(node=>node.getBoundingClientRect().bottom>bounds.top);
  const key=row?.getAttribute("data-live-event");
  anchor.current=row&&key?{key,top:row.getBoundingClientRect().top-bounds.top+root.scrollTop}:null;
 };
 useEffect(()=>{
  const media=window.matchMedia?.("(prefers-reduced-motion: reduce)");
  const changed=()=>{if(media?.matches)cancel();};
  media?.addEventListener?.("change",changed);
  return()=>{media?.removeEventListener?.("change",changed);cancel();};
 },[]);
 useLayoutEffect(()=>{
  const root=list.current;if(!root)return;
  const bounds=root.getBoundingClientRect(),current=new Map<string,Position>();
  Array.from(root.children).forEach((child,i)=>{
   const item=items[i];if(!item||!(child instanceof HTMLElement))return;
   const rect=child.getBoundingClientRect();
   current.set(item.key,{node:child,top:rect.top-bounds.top+root.scrollTop,left:rect.left-bounds.left+root.scrollLeft,signature:signature(item)});
  });
  // Keep a reader's first visible row anchored instead of forcing the list
  // back to the latest event. Browser anchoring is disabled for this list.
  const reading=anchor.current;
  if(reading&&current.has(reading.key))root.scrollTop+=current.get(reading.key)!.top-reading.top;
  cancel();
  const animate=(node:HTMLElement,frames:Keyframe[],duration=340)=>{
   if(typeof node.animate!=="function")return;
   const animation=node.animate(frames,{duration,easing:"cubic-bezier(.2,.7,.2,1)"});
   animations.current.add(animation);
   const done=()=>animations.current.delete(animation);void animation.finished.then(done,done);
  };
  if(initialized.current&&!window.matchMedia?.("(prefers-reduced-motion: reduce)").matches){
   for(const [key,row]of current){
    const old=previous.current.get(key);
    if(!old){if(!reading)animate(row.node,[{opacity:0,transform:"translateY(-10px)"},{opacity:1,transform:"translateY(0)"}]);}
    else{
     const shift=old.top-row.top;
     const shiftX=old.left-row.left;
     if(!reading&&(Math.abs(shift)>1||Math.abs(shiftX)>1))animate(row.node,[{transform:`translate(${shiftX}px, ${shift}px)`},{transform:"translate(0, 0)"}]);
     if(old.signature!==row.signature)animate(row.node,[{backgroundColor:"rgba(50,151,105,.12)"},{backgroundColor:"transparent"}],650);
    }
   }
  }
  if(items.length)initialized.current=true;
  previous.current=current;rememberAnchor();
 // Only new events/content animate. Relative time labels and polling metadata
 // can update freely without replaying old events as fresh activity.
 // eslint-disable-next-line react-hooks/exhaustive-deps
 },[revision]);
 return <ol ref={list} onScroll={rememberAnchor} tabIndex={0} aria-label="最近协作动态" style={{overflowAnchor:"none"}}>
  {items.map(({key,icon:Icon,text,members,time,color})=><li key={key} data-live-event={key}>
   <span className={`tm-live-eventIcon tm-live-${color}`}><Icon size={17}/></span>
   <div><strong>{text}{members.length>0&&<span className="tm-live-eventMembers">：{members.map((member,index)=><span key={member}>{index>0&&" "}<code>{member}</code></span>)}</span>}</strong><small>{time}</small></div>
  </li>)}
 </ol>;
}
