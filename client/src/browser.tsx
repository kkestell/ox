import { createRoot } from "react-dom/client";

import { App } from "./components/app.tsx";

const root = document.getElementById("root");
if (!root) {
  throw new Error("missing application root");
}
createRoot(root).render(<App />);
