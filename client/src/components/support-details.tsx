import { type Snapshot } from "../protocol.ts";

export function SupportDetails({ activeSessionId, connection, onRefresh, snapshot, workspace }: {
  activeSessionId?: string;
  connection: string;
  onRefresh: () => void;
  snapshot: Snapshot;
  workspace: NonNullable<Snapshot["workspace"]>;
}) {
  return (
    <section aria-labelledby="support-heading">
      <h3 id="support-heading">Support details</h3>
      <dl>
        <dt>Browser connection</dt><dd>{connection}</dd>
        <dt>Host revision</dt><dd>{snapshot.revision}</dd>
        {activeSessionId ? <><dt>Session ID</dt><dd>{activeSessionId}</dd></> : null}
      </dl>
      <ul aria-label="Ox processes" className="logs processes">
        {snapshot.workspaces.values.map((entry) => (
          <li key={entry.id}>
            {`${entry.name} — ${entry.status}, ${entry.busy ? "working" : "idle"}${entry.awaiting ? ", waiting for an answer" : ""}`}
          </li>
        ))}
      </ul>
      {workspace.diagnostics.length > 0 ? <ul aria-label="Workspace diagnostics" className="logs">{workspace.diagnostics.map((diagnostic, index) => <li key={`${index}-${diagnostic}`}>{diagnostic}</li>)}</ul> : null}
      <button onClick={onRefresh} type="button">Refresh conversation history</button>
    </section>
  );
}
