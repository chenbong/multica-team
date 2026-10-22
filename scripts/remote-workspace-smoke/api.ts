const contents: Record<string,string> = {
 'src/auth/login.ts': '// Login flow\nexport async function login(email: string) {\n  const response = await fetch("/api/login");\n  if (!response.ok) throw new Error("Login failed");\n  return response.json();\n}\n',
 'README.md': '# Remote workspace\n\nRead-only preview with chat context.\n',
};
export const api = { chatWorkspace: async (_session: string, q: {operation:string;path:string;binding?:string}) => {
 await new Promise(r=>setTimeout(r,80));
 if(q.operation==='read') {if(!(q.path in contents))throw new Error('file_unavailable');return {binding:'smoke-binding',root_name:'teammesh',absolute_path:'/home/demo/projects/teammesh/'+q.path,content:contents[q.path],revision:'revision-1'};}
 const paths=q.path==='.'?['src/','README.md']:q.path==='src'?['src/auth/']:['src/auth/login.ts'];
 return {binding:'smoke-binding',root_name:'teammesh',absolute_path:'/home/demo/projects/teammesh',entries:paths.map(p=>({absolute_path:'/home/demo/projects/teammesh/'+p.replace(/\/$/,''),name:p.replace(/\/$/,'').split('/').pop(),path:p.replace(/\/$/,''),kind:p.endsWith('/')?'directory':'file',size:120}))};
}};
export const dispatchReasonCode=()=>'';
