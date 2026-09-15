import { type Snapshot } from "../protocol.ts";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";

export function SupportDetails({ activeSessionId, connection, onRefresh, snapshot, workspace }: {
  activeSessionId?: string;
  connection: string;
  onRefresh: () => void;
  snapshot: Snapshot;
  workspace: NonNullable<Snapshot["workspace"]>;
}) {
  return (
    <Card asChild className="gap-4 py-4">
      <section aria-labelledby="support-heading">
        <CardHeader className="px-4">
          <CardTitle asChild>
            <h3 id="support-heading">Support details</h3>
          </CardTitle>
        </CardHeader>
        <CardContent className="min-w-0 space-y-4 px-4">
          <dl className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-sm">
            <dt className="text-muted-foreground">Browser connection</dt>
            <dd className="min-w-0 break-words font-mono [overflow-wrap:anywhere]">{connection}</dd>
            <dt className="text-muted-foreground">Host revision</dt>
            <dd className="min-w-0 break-words font-mono [overflow-wrap:anywhere]">{snapshot.revision}</dd>
            {activeSessionId ? (
              <>
                <dt className="text-muted-foreground">Session ID</dt>
                <dd className="min-w-0 break-words font-mono [overflow-wrap:anywhere]">{activeSessionId}</dd>
              </>
            ) : null}
          </dl>
          <div className="min-w-0">
            <h4 className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Ox processes</h4>
            <ul aria-label="Ox processes" className="mt-2 min-w-0 space-y-1 font-mono text-xs text-muted-foreground">
              {snapshot.workspaces.values.map((entry) => (
                <li className="break-words [overflow-wrap:anywhere]" key={entry.id}>
                  {`${entry.name} — ${entry.status}, ${entry.busy ? "working" : "idle"}${entry.awaiting ? ", waiting for an answer" : ""}`}
                </li>
              ))}
            </ul>
          </div>
          {workspace.diagnostics.length > 0 ? (
            <div className="min-w-0">
              <h4 className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Diagnostics</h4>
              <ul aria-label="Workspace diagnostics" className="mt-2 min-w-0 space-y-1.5 font-mono text-xs">
                {workspace.diagnostics.map((diagnostic, index) => (
                  <li className="break-words rounded-md bg-muted/50 px-2 py-1.5 [overflow-wrap:anywhere]" key={`${index}-${diagnostic}`}>
                    {diagnostic}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          <Button onClick={onRefresh} variant="outline">Refresh conversation history</Button>
        </CardContent>
      </section>
    </Card>
  );
}
