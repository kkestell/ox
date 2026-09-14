import type * as acp from "@agentclientprotocol/sdk";

// Loading replays updates before its response. This small owner exists so the
// supervisor can attach the route first; transcript projection belongs elsewhere.
export class SessionController {
  #updates: acp.SessionUpdate[] = [];

  constructor(readonly id: string) {}

  accept(update: acp.SessionUpdate): void {
    this.#updates.push(update);
  }

  get updates(): readonly acp.SessionUpdate[] {
    return this.#updates;
  }
}
