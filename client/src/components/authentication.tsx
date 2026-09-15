import { CircleCheckIcon } from "lucide-react";

import { type Snapshot } from "../protocol.ts";

import { Disclosure } from "./disclosure.tsx";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

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
    <Card asChild className="gap-4 py-4">
      <section aria-labelledby="authentication-heading">
        <CardHeader className="gap-1 px-4">
          <CardTitle asChild>
            <h3 id="authentication-heading">Authentication</h3>
          </CardTitle>
          <CardDescription>{authenticated ? "Connected" : "Not connected"}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3 px-4">
          {authentication.error ? (
            <Alert variant="destructive">
              <AlertDescription>{authentication.error}</AlertDescription>
            </Alert>
          ) : null}
          {authenticated ? (
            <>
              <div className="flex flex-wrap items-center gap-3">
                <p className="flex min-w-0 flex-1 items-center gap-2 text-sm text-emerald-700 dark:text-emerald-300">
                  <CircleCheckIcon className="size-4 shrink-0" />
                  Ox is connected and ready to start conversations.
                </p>
                {authentication.logoutAvailable ? (
                  <Button disabled={working} onClick={onLogout} size="sm" variant="outline">Log out</Button>
                ) : null}
              </div>
              {authentication.methods.some((method) => method.type === "terminal") ? (
                <Disclosure label="Change credential">
                  <AuthenticationMethods
                    authentication={authentication}
                    credential={credential}
                    onAuthenticate={onAuthenticate}
                    onCredential={onCredential}
                    onLogin={onLogin}
                    terminalOnly
                    working={working}
                  />
                </Disclosure>
              ) : null}
            </>
          ) : (
            <AuthenticationMethods
              authentication={authentication}
              credential={credential}
              onAuthenticate={onAuthenticate}
              onCredential={onCredential}
              onLogin={onLogin}
              working={working}
            />
          )}
        </CardContent>
      </section>
    </Card>
  );
}

function AuthenticationMethods({ authentication, credential, onAuthenticate, onCredential, onLogin, terminalOnly = false, working }: {
  authentication: Snapshot["authentication"];
  credential: string;
  onAuthenticate: (methodId: string) => void;
  onCredential: (credential: string) => void;
  onLogin: (methodId: string) => void;
  terminalOnly?: boolean;
  working: boolean;
}) {
  return authentication.methods.map((method) => method.type === "agent" ? (
    terminalOnly ? null : (
      <Button disabled={working} key={method.id} onClick={() => onAuthenticate(method.id)} variant="outline">
        Use configured credential
      </Button>
    )
  ) : (
    <form
      className="space-y-3"
      key={method.id}
      onSubmit={(event) => { event.preventDefault(); onLogin(method.id); }}
    >
      <div className="space-y-1.5">
        <Label htmlFor={`credential-${method.id}`}>OpenRouter API key</Label>
        <Input
          autoComplete="off"
          disabled={working}
          id={`credential-${method.id}`}
          onChange={(event) => onCredential(event.target.value)}
          placeholder="Paste API key"
          required
          type="password"
          value={credential}
        />
      </div>
      <Button aria-label={method.name} disabled={working} type="submit">Connect</Button>
    </form>
  ));
}
