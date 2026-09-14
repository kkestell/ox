import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

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
            <Button onClick={() => setServers((current) => current.filter((_, currentIndex) => currentIndex !== index))} type="button">
              Remove server
            </Button>
            <Label>
              Name
              <Input
                maxLength={512}
                onChange={(event) => replace(index, { ...server, name: event.target.value })}
                required
                value={server.name}
              />
            </Label>
            {server.transport === "http" ? (
              <>
                <Label>
                  URL
                  <Input
                    maxLength={4096}
                    onChange={(event) => replace(index, { ...server, url: event.target.value })}
                    required
                    type="url"
                    value={server.url}
                  />
                </Label>
                <fieldset>
                  <legend>HTTP headers</legend>
                  {server.headers.map((header, headerIndex) => (
                    <div key={headerIndex}>
                      <Label>
                        Header name
                        <Input
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
                      </Label>
                      <Label>
                        Header value
                        <Input
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
                      </Label>
                      <Button
                        aria-label={`Remove header ${headerIndex + 1}`}
                        onClick={() => replace(index, { ...server, headers: server.headers.filter((_, currentIndex) => currentIndex !== headerIndex) })}
                        type="button"
                      >
                        Remove header
                      </Button>
                    </div>
                  ))}
                  <Button onClick={() => replace(index, { ...server, headers: [...server.headers, { name: "", value: "" }] })} type="button">
                    Add header
                  </Button>
                </fieldset>
              </>
            ) : (
              <>
                <Label>
                  Command
                  <Input
                    maxLength={4096}
                    onChange={(event) => replace(index, { ...server, command: event.target.value })}
                    required
                    value={server.command}
                  />
                </Label>
                <fieldset>
                  <legend>Arguments</legend>
                  {server.args.map((argument, argumentIndex) => (
                    <div key={argumentIndex}>
                      <Label>
                        Argument {argumentIndex + 1}
                        <Input
                          maxLength={16_384}
                          onChange={(event) =>
                            replace(index, {
                              ...server,
                              args: server.args.map((value, currentIndex) => (currentIndex === argumentIndex ? event.target.value : value)),
                            })
                          }
                          value={argument}
                        />
                      </Label>
                      <Button
                        aria-label={`Remove argument ${argumentIndex + 1}`}
                        onClick={() => replace(index, { ...server, args: server.args.filter((_, currentIndex) => currentIndex !== argumentIndex) })}
                        type="button"
                      >
                        Remove argument
                      </Button>
                    </div>
                  ))}
                  <Button onClick={() => replace(index, { ...server, args: [...server.args, ""] })} type="button">
                    Add argument
                  </Button>
                </fieldset>
                <fieldset>
                  <legend>Environment</legend>
                  {server.env.map((variable, variableIndex) => (
                    <div key={variableIndex}>
                      <Label>
                        Variable name
                        <Input
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
                      </Label>
                      <Label>
                        Variable value
                        <Input
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
                      </Label>
                      <Button
                        aria-label={`Remove variable ${variableIndex + 1}`}
                        onClick={() => replace(index, { ...server, env: server.env.filter((_, currentIndex) => currentIndex !== variableIndex) })}
                        type="button"
                      >
                        Remove variable
                      </Button>
                    </div>
                  ))}
                  <Button onClick={() => replace(index, { ...server, env: [...server.env, { name: "", value: "" }] })} type="button">
                    Add variable
                  </Button>
                </fieldset>
              </>
            )}
          </fieldset>
        ))}
        <Button onClick={() => setServers((current) => [...current, { transport: "http", name: "", url: "", headers: [] }])} type="button">
          Add HTTP MCP server
        </Button>
        <Button onClick={() => setServers((current) => [...current, { transport: "stdio", name: "", command: "", args: [], env: [] }])} type="button">
          Add stdio MCP server
        </Button>
        <Button type="submit">Save MCP servers</Button>
      </fieldset>
    </form>
  );
}
