import { useEffect, useState } from "react";
import { Composer } from "./Composer";
import { Conversation } from "./Conversation";
import { SessionList } from "./SessionList";
import { useWorkspaces } from "./useWorkspaces";
import { Workbench } from "./Workbench";

export function App() {
  const { workspaces, sessions, error, retry } = useWorkspaces();
  // The viewed session survives a webview reload.
  const [viewed, setViewed] = useState(() => sessionStorage.getItem("viewed"));
  useEffect(() => {
    if (viewed === null) sessionStorage.removeItem("viewed");
    else sessionStorage.setItem("viewed", viewed);
  }, [viewed]);
  const session = sessions.find((session) => session.id === viewed);

  return (
    <Workbench
      left={
        <SessionList
          workspaces={workspaces}
          sessions={sessions}
          selected={viewed}
          onSelect={setViewed}
          error={error}
          onRetry={retry}
        />
      }
    >
      <Conversation sessionId={viewed} />
      {session && <Composer key={session.id} session={session} />}
    </Workbench>
  );
}
