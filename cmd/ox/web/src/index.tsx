import { render } from "@solidjs/web";
import App from "./App";
import "./style.css";
import { followViewport } from "./viewport";

followViewport();
render(() => <App />, document.getElementById("root")!);
