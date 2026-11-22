import solid from "@solidjs/vite-plugin";
import { defineConfig } from "vite";

// npm run dev serves the UI with live reload on ox's usual port and passes
// the oxd relay through to ox, which make dev runs on 8081. OX_VITE_PORT and
// OX_RELAY_PORT move both, for a second stack on other ports.
const relay = `http://localhost:${process.env.OX_RELAY_PORT ?? "8081"}`;

export default defineConfig({
  plugins: [solid()],
  server: {
    port: Number(process.env.OX_VITE_PORT ?? "8080"),
    strictPort: true,
    proxy: { "/events": relay, "/rpc": relay },
  },
});
