"use client";
import {useState} from "react";

const format=new Intl.NumberFormat("zh-CN");

/** Animate only an observed number-to-number change, never a loading value. */
export function AnimatedNumber({value}:{value:number|null}){
 const [sample,setSample]=useState<{value:number|null;previous:number|null;revision:number}>({value,previous:null,revision:0});
 // Adjusting derived state here avoids painting the new number before an
 // effect can capture its predecessor. No timers or animation loops survive.
 if(sample.value!==value){setSample({value,previous:sample.value,revision:sample.revision+1});}
 const changing=sample.previous!==null&&value!==null&&sample.previous!==value;
 const direction=value!==null&&sample.previous!==null&&value<sample.previous?"down":"up";
 return <span className="tm-live-number" data-value={value??"unknown"}>
  <span key={sample.revision} className={`tm-live-numberStack${changing?" tm-live-numberChanging":""}`} data-direction={direction}>
   {changing&&sample.previous!==null&&<span className="tm-live-numberOld" aria-hidden="true">{format.format(sample.previous)}</span>}
   <span className="tm-live-numberNew">{value===null?"—":format.format(value)}</span>
  </span>
 </span>;
}
