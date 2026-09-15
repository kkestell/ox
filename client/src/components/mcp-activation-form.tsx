import { PlusIcon, XIcon } from "lucide-react";
import { type ReactNode } from "react";

import { type MCPServer } from "../protocol.ts";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

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
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        onSave(servers);
      }}
    >
      <p className="max-w-2xl text-sm leading-6 text-muted-foreground">
        Servers apply to new or reopened conversations. Secret values stay in the host and are never returned to the browser.
      </p>
      {servers.map((server, index) => (
        <Group key={index} legend={server.transport === "http" ? "HTTP MCP server" : "Stdio MCP server"}>
          <Field label="Name">
            <Input
              maxLength={512}
              onChange={(event) => replace(index, { ...server, name: event.target.value })}
              required
              value={server.name}
            />
          </Field>
          {server.transport === "http" ? (
            <>
              <Field label="URL">
                <Input
                  maxLength={4096}
                  onChange={(event) => replace(index, { ...server, url: event.target.value })}
                  required
                  type="url"
                  value={server.url}
                />
              </Field>
              <Group legend="HTTP headers">
                {server.headers.map((header, headerIndex) => (
                  <Row
                    key={headerIndex}
                    onRemove={() => replace(index, { ...server, headers: server.headers.filter((_, currentIndex) => currentIndex !== headerIndex) })}
                    removeLabel={`Remove header ${headerIndex + 1}`}
                  >
                    <Field label="Header name">
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
                    </Field>
                    <Field label="Header value">
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
                    </Field>
                  </Row>
                ))}
                <Button
                  onClick={() => replace(index, { ...server, headers: [...server.headers, { name: "", value: "" }] })}
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  <PlusIcon />
                  Add header
                </Button>
              </Group>
            </>
          ) : (
            <>
              <Field label="Command">
                <Input
                  maxLength={4096}
                  onChange={(event) => replace(index, { ...server, command: event.target.value })}
                  required
                  value={server.command}
                />
              </Field>
              <Group legend="Arguments">
                {server.args.map((argument, argumentIndex) => (
                  <Row
                    key={argumentIndex}
                    onRemove={() => replace(index, { ...server, args: server.args.filter((_, currentIndex) => currentIndex !== argumentIndex) })}
                    removeLabel={`Remove argument ${argumentIndex + 1}`}
                  >
                    <Field label={`Argument ${argumentIndex + 1}`}>
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
                    </Field>
                  </Row>
                ))}
                <Button
                  onClick={() => replace(index, { ...server, args: [...server.args, ""] })}
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  <PlusIcon />
                  Add argument
                </Button>
              </Group>
              <Group legend="Environment">
                {server.env.map((variable, variableIndex) => (
                  <Row
                    key={variableIndex}
                    onRemove={() => replace(index, { ...server, env: server.env.filter((_, currentIndex) => currentIndex !== variableIndex) })}
                    removeLabel={`Remove variable ${variableIndex + 1}`}
                  >
                    <Field label="Variable name">
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
                    </Field>
                    <Field label="Variable value">
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
                    </Field>
                  </Row>
                ))}
                <Button
                  onClick={() => replace(index, { ...server, env: [...server.env, { name: "", value: "" }] })}
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  <PlusIcon />
                  Add variable
                </Button>
              </Group>
            </>
          )}
          <Button
            onClick={() => setServers((current) => current.filter((_, currentIndex) => currentIndex !== index))}
            size="sm"
            type="button"
            variant="ghost"
          >
            <XIcon />
            Remove server
          </Button>
        </Group>
      ))}
      <div className="flex flex-wrap gap-2">
        <Button
          onClick={() => setServers((current) => [...current, { transport: "http", name: "", url: "", headers: [] }])}
          size="sm"
          type="button"
          variant="outline"
        >
          <PlusIcon />
          Add HTTP MCP server
        </Button>
        <Button
          onClick={() => setServers((current) => [...current, { transport: "stdio", name: "", command: "", args: [], env: [] }])}
          size="sm"
          type="button"
          variant="outline"
        >
          <PlusIcon />
          Add stdio MCP server
        </Button>
        <Button type="submit">Save MCP servers</Button>
      </div>
    </form>
  );
}

// A fieldset names the group it holds, which is what lets a server, its headers,
// and its environment stay addressable while several drafts are open.
function Group({ children, legend }: { children: ReactNode; legend: string }) {
  return (
    <fieldset className="min-w-0 space-y-3 rounded-lg border bg-muted/20 p-3">
      <legend className="rounded bg-card px-1.5 text-xs font-semibold text-muted-foreground">{legend}</legend>
      {children}
    </fieldset>
  );
}

function Field({ children, label }: { children: ReactNode; label: string }) {
  return (
    <Label className="flex-1 flex-col items-stretch gap-1.5">
      <span>{label}</span>
      {children}
    </Label>
  );
}

function Row({ children, onRemove, removeLabel }: { children: ReactNode; onRemove: () => void; removeLabel: string }) {
  return (
    <div className="flex min-w-0 flex-wrap items-end gap-2">
      {children}
      <Button
        aria-label={removeLabel}
        className="hover:bg-destructive/10 hover:text-destructive"
        onClick={onRemove}
        size="icon"
        title={removeLabel}
        type="button"
        variant="ghost"
      >
        <XIcon />
      </Button>
    </div>
  );
}
