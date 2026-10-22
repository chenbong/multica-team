// Runs the real Next app, original dashboard and original useChatController.
// Only backend HTTP/WebSocket data are fixtures. Files use the live daemon.
import {chromium,expect} from '@playwright/test';
import fs from 'node:fs/promises';
const browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_EXECUTABLE_PATH?{executablePath:process.env.PLAYWRIGHT_EXECUTABLE_PATH}:{})});
try{
 const page=await browser.newPage({viewport:{width:1440,height:1000},locale:'zh-CN'});const errors=[];const requests=[];page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(r.url().includes('/api/'))requests.push(r.url())});
 await page.goto('http://127.0.0.1:4182/local-lab/chat?session=55555555-5555-4555-8555-555555555551',{timeout:120000});
 await expect(page.getByText('原版 Web 应用 · 本地接口验证',{exact:true})).toBeVisible();
 await expect(page.getByRole('link',{name:'收件箱',exact:true})).toBeVisible();
 await expect(page.locator('[data-panel][id=list]')).toBeVisible();
 await page.screenshot({path:'/tmp/teammesh-original-chat-before.png'});
 await page.getByRole('button',{name:'文件 / Files',exact:true}).click();
 await expect(page.locator('[data-panel][id=list]')).not.toBeVisible();
 await page.getByRole('button',{name:'src',exact:true}).click();await page.getByRole('button',{name:'auth',exact:true}).click();await page.getByRole('button',{name:'login.ts',exact:true}).click();
 const preview=page.getByLabel('只读文件预览 / Read-only file preview',{exact:true});await expect(preview).toContainText('LIVE-lab-chat-one');
 await page.getByRole('button',{name:'文件 / File',exact:true}).click();
 const editor=page.locator('[contenteditable=true]');await editor.fill('验证原版聊天控制器：读取文件上下文');
 const result=page.waitForResponse(r=>r.url().endsWith('/messages')&&r.request().method()==='POST');
 await page.getByRole('button',{name:'发送',exact:true}).click();const response=await result;expect(response.status()).toBe(201);
 const reply=await response.json();expect(reply.content).toContain('LIVE-lab-chat-one');
 await expect(page.getByLabel('Agent 聊天 / Agent chat',{exact:true}).getByText('文件链路验证成功（非模型回答）',{exact:true}).last()).toBeVisible({timeout:15000});
 await expect(page.getByRole('button',{name:'移除引用 / Remove src/auth/login.ts',exact:true})).toHaveCount(0);
 await page.screenshot({path:'/tmp/teammesh-original-chat-after.png'});
 await page.getByRole('button',{name:'显示会话列表',exact:true}).click();await expect(page.locator('[data-panel][id=list]')).toBeVisible();
 await page.getByText('另一会话：验证目录隔离',{exact:true}).click();await expect(page).toHaveURL(/555555555552/);
 await page.getByRole('button',{name:'文件 / Files',exact:true}).click();await page.getByRole('button',{name:'README.md',exact:true}).click();await expect(preview).toContainText('lab-chat-two');
 expect(requests.some(url=>url.includes('/api/chat/sessions/')&&url.includes('/workspace'))).toBe(true);
 expect(errors).toEqual([]);
 console.log('PASS: original Next dashboard + ChatPage + useChatController + API client + HTTP send + WS completion + session routing + real daemon files');
 await fs.writeFile('/tmp/teammesh-original-web-requests.json',JSON.stringify(requests,null,2));
}finally{await browser.close()}
