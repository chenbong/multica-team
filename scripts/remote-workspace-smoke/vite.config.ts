import {defineConfig} from 'vite';
import react from '@vitejs/plugin-react';
import {fileURLToPath} from 'node:url';
const here=fileURLToPath(new URL('.',import.meta.url));
export default defineConfig({root:here,plugins:[react()],resolve:{alias:[{find:/^@multica\/core\/api$/,replacement:here+'api.ts'},{find:/^@multica\/core\/auth$/,replacement:here+'auth.ts'},{find:/^@multica\/core\/hooks$/,replacement:here+'hooks.ts'}]},css:{postcss:here},server:{host:'127.0.0.1',port:4179,fs:{allow:[fileURLToPath(new URL('../../..',import.meta.url))]}}});
