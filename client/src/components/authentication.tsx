import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

import { type Snapshot } from "../protocol.ts";

// Connecting and managing a credential are the same surface: the methods Ox
// advertises name their own controls, and a stored credential is worth
// attempting only while the workspace is unauthenticated.
export function Authentication({ authentication, credential, onAuthenticate, onCredential, onLogin, onLogout }: {
  authentication: Snapshot["authentication"];
  credential: string;
  onAuthenticate: (methodId: string) => void;
  onCredential: (credential: string) => void;
  onLogin: (methodId: string) => void;
  onLogout: () => void;
}) {
  const working = authentication.status === "working";
  const authenticated = authentication.status === "authenticated";
  return (
    <section aria-labelledby="authentication-heading">
      <h3 id="authentication-heading">Authentication</h3>
      {authentication.error ? <p role="alert">{authentication.error}</p> : null}
      <p>{authenticated ? "Connected" : "Not connected"}</p>
      {authentication.methods.map((method) => method.type === "agent" ? (
        authenticated ? null : (
          <Button disabled={working} key={method.id} onClick={() => onAuthenticate(method.id)} type="button">Use configured credential</Button>
        )
      ) : (
        <form key={method.id} onSubmit={(event) => { event.preventDefault(); onLogin(method.id); }}>
          <Label>{`${method.name} credential`}<Input autoComplete="off" disabled={working} onChange={(event) => onCredential(event.target.value)} required type="password" value={credential} /></Label>
          <Button disabled={working} type="submit">{method.name}</Button>
        </form>
      ))}
      {authentication.logoutAvailable ? <Button disabled={working} onClick={onLogout} type="button">Log out</Button> : null}
    </section>
  );
}
