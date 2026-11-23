import { createEffect, createSignal } from "solid-js";
import { Disconnected } from "./connection";
import { useOx } from "./ox";

// Polled is a repeated request's latest answer: its value or its error.
type Polled<T> = { kind: "value"; value: T } | { kind: "error"; message: string };

// createPoll runs request now and on each tick of the page's clock, and
// returns its latest answer, undefined until the first. Only an answer
// replaces it; a lost connection (Disconnected) keeps the last one, since the page
// reports the lost connection. One request is in flight at a time, so
// answers apply in the order sent.
export function createPoll<T>(request: () => Promise<T>) {
  const ox = useOx();
  const [polled, setPolled] = createSignal<Polled<T>>();
  let inFlight = false;
  createEffect(ox.clock, () => {
    if (inFlight) return;
    inFlight = true;
    request()
      .then(
        (value) => setPolled({ kind: "value", value }),
        (e: Error) => {
          if (!(e instanceof Disconnected)) setPolled({ kind: "error", message: e.message });
        },
      )
      .finally(() => {
        inFlight = false;
      });
  });
  return polled;
}
