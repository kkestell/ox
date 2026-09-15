import { type MCPServer } from "../protocol.ts";

export function MCPActivationForm({
  onSave,
  servers,
  setServers,
}: {
  onSave: (servers: MCPServer[]) => void;
  servers: MCPServer[];
  setServers: (servers: MCPServer[] | ((current: MCPServer[]) => MCPServer[])) => void;
}) {
  function replace(index: number, server: MCPServer): void {
    setServers((current) => current.map((value, currentIndex) => (currentIndex === index ? server : value)));
  }

  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        onSave(servers);
      }}
    >
      <fieldset>
        <legend>MCP servers</legend>
        <p>Saved definitions are used for later conversation activations. Secret values are never returned to the browser.</p>
        {servers.map((server, index) => (
          <fieldset key={index}>
            <legend>{server.transport === "http" ? "HTTP MCP server" : "Stdio MCP server"}</legend>
            <button onClick={() => setServers((current) => current.filter((_, currentIndex) => currentIndex !== index))} type="button">
              Remove server
            </button>
            <label>
              Name
              <input
                maxLength={512}
                onChange={(event) => replace(index, { ...server, name: event.target.value })}
                required
                value={server.name}
              />
            </label>
            {server.transport === "http" ? (
              <>
                <label>
                  URL
                  <input
                    maxLength={4096}
                    onChange={(event) => replace(index, { ...server, url: event.target.value })}
                    required
                    type="url"
                    value={server.url}
                  />
                </label>
                <fieldset>
                  <legend>HTTP headers</legend>
                  {server.headers.map((header, headerIndex) => (
                    <div key={headerIndex}>
                      <label>
                        Header name
                        <input
                          maxLength={256}
                          onChange={(event) =>
                            replace(index, {
                              ...server,
                              headers: server.headers.map((value, currentIndex) =>
                                currentIndex === headerIndex ? { ...value, name: event.target.value } : value,
                              ),
                            })
                          }
                          required
                          value={header.name}
                        />
                      </label>
                      <label>
                        Header value
                        <input
                          autoComplete="off"
                          maxLength={16_384}
                          onChange={(event) =>
                            replace(index, {
                              ...server,
                              headers: server.headers.map((value, currentIndex) =>
                                currentIndex === headerIndex ? { ...value, value: event.target.value } : value,
                              ),
                            })
                          }
                          type="password"
                          value={header.value}
                        />
                      </label>
                      <button
                        aria-label={`Remove header ${headerIndex + 1}`}
                        onClick={() => replace(index, { ...server, headers: server.headers.filter((_, currentIndex) => currentIndex !== headerIndex) })}
                        type="button"
                      >
                        Remove header
                      </button>
                    </div>
                  ))}
                  <button onClick={() => replace(index, { ...server, headers: [...server.headers, { name: "", value: "" }] })} type="button">
                    Add header
                  </button>
                </fieldset>
              </>
            ) : (
              <>
                <label>
                  Command
                  <input
                    maxLength={4096}
                    onChange={(event) => replace(index, { ...server, command: event.target.value })}
                    required
                    value={server.command}
                  />
                </label>
                <fieldset>
                  <legend>Arguments</legend>
                  {server.args.map((argument, argumentIndex) => (
                    <div key={argumentIndex}>
                      <label>
                        Argument {argumentIndex + 1}
                        <input
                          maxLength={16_384}
                          onChange={(event) =>
                            replace(index, {
                              ...server,
                              args: server.args.map((value, currentIndex) => (currentIndex === argumentIndex ? event.target.value : value)),
                            })
                          }
                          value={argument}
                        />
                      </label>
                      <button
                        aria-label={`Remove argument ${argumentIndex + 1}`}
                        onClick={() => replace(index, { ...server, args: server.args.filter((_, currentIndex) => currentIndex !== argumentIndex) })}
                        type="button"
                      >
                        Remove argument
                      </button>
                    </div>
                  ))}
                  <button onClick={() => replace(index, { ...server, args: [...server.args, ""] })} type="button">
                    Add argument
                  </button>
                </fieldset>
                <fieldset>
                  <legend>Environment</legend>
                  {server.env.map((variable, variableIndex) => (
                    <div key={variableIndex}>
                      <label>
                        Variable name
                        <input
                          maxLength={256}
                          onChange={(event) =>
                            replace(index, {
                              ...server,
                              env: server.env.map((value, currentIndex) =>
                                currentIndex === variableIndex ? { ...value, name: event.target.value } : value,
                              ),
                            })
                          }
                          required
                          value={variable.name}
                        />
                      </label>
                      <label>
                        Variable value
                        <input
                          autoComplete="off"
                          maxLength={16_384}
                          onChange={(event) =>
                            replace(index, {
                              ...server,
                              env: server.env.map((value, currentIndex) =>
                                currentIndex === variableIndex ? { ...value, value: event.target.value } : value,
                              ),
                            })
                          }
                          type="password"
                          value={variable.value}
                        />
                      </label>
                      <button
                        aria-label={`Remove variable ${variableIndex + 1}`}
                        onClick={() => replace(index, { ...server, env: server.env.filter((_, currentIndex) => currentIndex !== variableIndex) })}
                        type="button"
                      >
                        Remove variable
                      </button>
                    </div>
                  ))}
                  <button onClick={() => replace(index, { ...server, env: [...server.env, { name: "", value: "" }] })} type="button">
                    Add variable
                  </button>
                </fieldset>
              </>
            )}
          </fieldset>
        ))}
        <button onClick={() => setServers((current) => [...current, { transport: "http", name: "", url: "", headers: [] }])} type="button">
          Add HTTP MCP server
        </button>
        <button onClick={() => setServers((current) => [...current, { transport: "stdio", name: "", command: "", args: [], env: [] }])} type="button">
          Add stdio MCP server
        </button>
        <button type="submit">Save MCP servers</button>
      </fieldset>
    </form>
  );
}
