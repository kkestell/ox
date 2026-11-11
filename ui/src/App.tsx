import { useEffect, useState } from "react";
import { Composer } from "./Composer";
import { Conversation } from "./Conversation";
import { SessionList } from "./SessionList";
import { useSessions } from "./useSessions";
import { Workbench } from "./Workbench";

export function App() {
  const { sessions, error, retry } = useSessions();
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
