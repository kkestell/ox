import { useEffect, useState } from "react";
import { Composer } from "./Composer";
import { Conversation } from "./Conversation";
import { localMerge } from "./localMerge";
import { MergeButton } from "./MergeButton";
import type { Item } from "./protocol";
import { SessionList } from "./SessionList";
import { useConversation } from "./useConversation";
import { useSessionActions } from "./useSessionActions";
import { useWorkspaces } from "./useWorkspaces";
import { Workbench } from "./Workbench";

export function App() {
  const { workspaces, sessions, connection } = useWorkspaces();
  // The viewed session survives a webview reload.
  const [viewed, setViewed] = useState(() => sessionStorage.getItem("viewed"));
  useEffect(() => {
    if (viewed === null) sessionStorage.removeItem("viewed");
    else sessionStorage.setItem("viewed", viewed);
  }, [viewed]);
  const session = sessions.find((session) => session.id === viewed);
  const merge = session && localMerge(session);
  const actions = useSessionActions();
  const items = useConversation(viewed, connection);
  const request = items.find(
    (item): item is Extract<Item, { type: "permission" | "elicitation" }> =>
      (item.type === "permission" || item.type === "elicitation") && !item.answered,
  );

  return (
    <>
      <Workbench
        left={
          <SessionList
            workspaces={workspaces}
            sessions={sessions}
            selected={viewed}
            onSelect={setViewed}
            actions={actions}
          />
        }
        actions={
          session &&
          merge && (
            <MergeButton
              key={session.id}
              session={session}
              compare={merge.compare}
              comparison={merge.comparison}
              deleteSession={actions.deleteSession}
            />
          )
        }
      >
        <Conversation sessionId={viewed} items={items} />
        {session && <Composer key={session.id} session={session} request={request} />}
      </Workbench>
      {actions.dialogs}
    </>
  );
}
