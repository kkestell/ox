import solid from "@solidjs/vite-plugin";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [solid()],
  // npm run dev serves the UI with live reload on ox's usual port and passes
  // the oxd relay through to ox, which make dev runs on 8081.
  server: {
    port: 8080,
    strictPort: true,
    proxy: {
      "/events": "http://localhost:8081",
      "/rpc": "http://localhost:8081",
    },
  },
});
