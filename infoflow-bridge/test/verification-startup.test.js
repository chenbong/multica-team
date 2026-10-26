import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';

test('verification robot and code relay are ready before a slow work robot connects',async()=>{
 const source=readFileSync(new URL('../src/robots.js',import.meta.url),'utf8');
 const method=source.slice(source.indexOf('  async sync(config = this.config)'),source.indexOf('  status()'));
 const order=[];let release;
 const slow=new Promise(resolve=>{release=resolve;});
 const Fixture=vm.runInNewContext('(class Fixture {constructor(){this.instances=new Map();}'+method+
  'async #stop(id){this.instances.delete(id);} async #start(robot){order.push(robot.id);this.instances.set(robot.id,{fingerprint:robot.id});if(robot.id==="slow")await slow;} async #syncRelay(){order.push("relay");}})',
  {order,slow,robotIsComplete:()=>true,fingerprint:robot=>robot.id});
 const fixture=new Fixture();
 const result=fixture.sync({robots:[{id:'slow',enabled:true},{id:'verify',enabled:true,purpose:'verification'}]});
 await new Promise(resolve=>setImmediate(resolve));
 assert.deepEqual(order,['verify','relay','slow']);
 release();await result;
});

test('work-robot connection fan-out is bounded to four',async()=>{
 const source=readFileSync(new URL('../src/robots.js',import.meta.url),'utf8');
 const method=source.slice(source.indexOf('  async sync(config = this.config)'),source.indexOf('  status()'));
 const releases=[];const metrics={active:0,peak:0,started:0};
 const Fixture=vm.runInNewContext('(class Fixture {constructor(){this.instances=new Map();}'+method+
  'async #stop(id){this.instances.delete(id);} async #start(robot){this.instances.set(robot.id,{fingerprint:robot.id});metrics.started++;metrics.active++;metrics.peak=Math.max(metrics.peak,metrics.active);await new Promise(resolve=>releases.push(()=>{metrics.active--;resolve();}));} async #syncRelay(){}})',
  {releases,metrics,robotIsComplete:()=>true,fingerprint:robot=>robot.id});
 const fixture=new Fixture();const promise=fixture.sync({robots:Array.from({length:10},(_,i)=>({id:String(i),enabled:true}))});
 await new Promise(resolve=>setImmediate(resolve));
 assert.equal(metrics.started,4);assert.equal(metrics.peak,4);
 releases.splice(0).forEach(release=>release());
 await new Promise(resolve=>setImmediate(resolve));
 assert.equal(metrics.started,8);assert.equal(metrics.peak,4);
 releases.splice(0).forEach(release=>release());
 await new Promise(resolve=>setImmediate(resolve));
 assert.equal(metrics.started,10);assert.equal(metrics.peak,4);
 releases.splice(0).forEach(release=>release());await promise;
});
