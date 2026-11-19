import solid from "@solidjs/vite-plugin";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [solid()],
  // npm run dev serves the UI with live reload and passes the oxd relay
  // through to ox.
  server: {
    proxy: {
      "/events": "http://localhost:8080",
      "/rpc": "http://localhost:8080",
    },
  },
});
