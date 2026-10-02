import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

// The management console is built from webui/ and emitted to webui/dist, which
// the Go binary embeds. Keeping the output inside webui/ means `go build` stays
// the only deployment step once the artifacts are committed.
export default defineConfig({
  root: "webui",
  base: "/",
  plugins: [vue()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
    target: "es2020",
    cssCodeSplit: false,
    // 管理端 CSP 禁止内联脚本，Vite 的 modulepreload polyfill 会注入内联 <script>，
    // 因此关闭它；现代浏览器原生支持 modulepreload。
    modulePreload: { polyfill: false },
    chunkSizeWarningLimit: 1500,
  },
});
