import {defineConfig} from "vite";
import react from "@vitejs/plugin-react";
// diagram-frame.html is the hidden page that draws Mermaid diagrams; the
// server gives it its own Content-Security-Policy.
export default defineConfig({plugins:[react()],build:{rollupOptions:{input:{main:"index.html",diagrams:"diagram-frame.html"}}},server:{host:"127.0.0.1",proxy:{"/api":"http://127.0.0.1:8080","/health":"http://127.0.0.1:8080"}}});
