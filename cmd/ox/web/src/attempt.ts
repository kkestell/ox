import { createSignal } from "solid-js";

// createAttempt runs requests and holds an error until the next request
// starts or clear runs. A request that fails shows its error even after a
// later one has started. The promise attempt returns settles once the request
// has. fail shows an error at once, for a request refused before it is sent.
export function createAttempt() {
  const [error, setError] = createSignal<string>();
  const clear = () => setError(undefined);
  const attempt = (request: Promise<unknown>) => {
    clear();
    return request.then(
      () => {},
      (e: Error) => setError(e.message),
    );
  };
  const fail = (message: string) => setError(message);
  return [error, attempt, clear, fail] as const;
}
