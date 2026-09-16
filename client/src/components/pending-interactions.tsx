import { type FormValue, type PendingInteraction } from "../protocol.ts";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

export function PendingInteractions({ interactions, onElicitation, onPermission }: {
  interactions: PendingInteraction[];
  onElicitation: (interactionId: string, action: "accept" | "decline" | "cancel", content?: Record<string, FormValue>) => void;
  onPermission: (interactionId: string, optionId: string) => void;
}) {
  if (interactions.length === 0) return null;
  return (
    <aside aria-label="Pending interactions" className="max-h-[35%] shrink-0 overflow-x-hidden overflow-y-auto bg-background py-3">
      <div className="mx-auto w-full max-w-3xl space-y-2 px-4">
        {interactions.map((interaction) => interaction.kind === "permission" ? (
          <PermissionInteraction interaction={interaction} key={interaction.id} onPermission={onPermission} />
        ) : (
          <ElicitationForm interaction={interaction} key={interaction.id} onSubmit={onElicitation} />
        ))}
      </div>
    </aside>
  );
}

export function PermissionInteraction({ interaction, onPermission }: {
  interaction: Extract<PendingInteraction, { kind: "permission" }>;
  onPermission: (interactionId: string, optionId: string) => void;
}) {
  const label = interaction.tool.title;
  return (
    <Card asChild className="w-full py-0 shadow-sm">
      <article aria-label={`Permission for ${label}`}>
        <CardContent className="flex flex-wrap items-center gap-3 px-3 py-3">
          <CardTitle asChild>
            <h3 className="min-w-0 flex-1 break-words text-sm [overflow-wrap:anywhere]">
              <span className="block font-medium">{label}</span>
              {interaction.tool.arguments ? <code className="mt-0.5 block break-words font-mono text-xs text-muted-foreground [overflow-wrap:anywhere]">{interaction.tool.arguments}</code> : null}
            </h3>
          </CardTitle>
          <div className="flex flex-wrap gap-2">
            {interaction.options.map((option, index) => (
              <Button
                key={option.id}
                onClick={() => onPermission(interaction.id, option.id)}
                size="sm"
                variant={index === 0 ? "default" : "outline"}
              >
                {permissionLabel(option.kind, option.name)}
              </Button>
            ))}
          </div>
        </CardContent>
      </article>
    </Card>
  );
}

export function ElicitationForm({
  interaction,
  onSubmit,
}: {
  interaction: Extract<PendingInteraction, { kind: "form" }>;
  onSubmit: (interactionId: string, action: "accept" | "decline" | "cancel", content?: Record<string, FormValue>) => void;
}) {
  return (
    <Card asChild className="gap-0 overflow-hidden py-0 shadow-sm">
      <article aria-label={interaction.title ?? interaction.message}>
        <CardHeader className="border-b bg-muted/40 px-4 py-3">
          <CardTitle asChild>
            <h3 className="text-sm">{interaction.title ?? "Question"}</h3>
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-2 px-4 py-3">
          <p className="text-sm text-muted-foreground">{interaction.message}</p>
          {interaction.description ? <p className="text-sm text-muted-foreground">{interaction.description}</p> : null}
          <form className="space-y-3 pt-1" onSubmit={(event) => {
            event.preventDefault();
            const form = new FormData(event.currentTarget);
            const content = Object.create(null) as Record<string, FormValue>;
            for (const field of interaction.fields) {
              switch (field.type) {
                case "string": {
                  const value = form.get(field.name);
                  if (value !== null && (field.required || value !== "")) content[field.name] = String(value);
                  break;
                }
                case "number":
                case "integer": {
                  const value = form.get(field.name);
                  if (value !== null && value !== "") content[field.name] = Number(value);
                  break;
                }
                case "boolean": content[field.name] = form.has(field.name); break;
                case "multi-select": content[field.name] = form.getAll(field.name).map(String); break;
              }
            }
            onSubmit(interaction.id, "accept", content);
          }}>
            {interaction.fields.map((field) => (
              <Field field={field} key={field.name} />
            ))}
            <div className="flex flex-wrap gap-2">
              <Button size="sm" type="submit">Submit answer</Button>
              <Button onClick={() => onSubmit(interaction.id, "decline")} size="sm" type="button" variant="outline">
                Decline
              </Button>
              <Button onClick={() => onSubmit(interaction.id, "cancel")} size="sm" type="button" variant="ghost">
                Cancel question
              </Button>
            </div>
          </form>
        </CardContent>
      </article>
    </Card>
  );
}

function permissionLabel(kind: Extract<PendingInteraction, { kind: "permission" }>["options"][number]["kind"], fallback: string): string {
  switch (kind) {
    case "allow_once": return "Allow once";
    case "allow_always": return "Always allow";
    case "reject_once": return "Reject";
    case "reject_always": return "Always reject";
    default: return fallback;
  }
}

function Field({ field }: { field: Extract<PendingInteraction, { kind: "form" }>["fields"][number] }) {
  const id = `field-${field.name}`;
  const description = field.description ? (
    <span className="text-xs font-normal text-muted-foreground">{field.description}</span>
  ) : null;

  if (field.type === "boolean") {
    return (
      <div className="flex items-center gap-2">
        <Checkbox defaultChecked={field.default} id={id} name={field.name} />
        <Label htmlFor={id}>{field.label}{description}</Label>
      </div>
    );
  }

  // Radix has no multiple-selection listbox, so a multi-select field keeps the
  // platform's own control.
  if (field.type === "multi-select") {
    return (
      <div className="space-y-1.5">
        <Label htmlFor={id}>{field.label}{description}</Label>
        <select
          className="w-full rounded-md border border-input bg-transparent px-3 py-1.5 text-sm shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
          defaultValue={field.default}
          id={id}
          multiple
          name={field.name}
          required={field.required}
        >
          {field.choices.map((choice) => <option key={choice.value} value={choice.value}>{choice.label}</option>)}
        </select>
      </div>
    );
  }

  if (field.type === "string" && field.choices) {
    return (
      <div className="space-y-1.5">
        <Label htmlFor={id}>{field.label}{description}</Label>
        <Select defaultValue={field.default} name={field.name} required={field.required}>
          <SelectTrigger aria-label={field.label} className="w-full" id={id}>
            <SelectValue placeholder="Select an option" />
          </SelectTrigger>
          <SelectContent>
            {field.choices.map((choice) => <SelectItem key={choice.value} value={choice.value}>{choice.label}</SelectItem>)}
          </SelectContent>
        </Select>
      </div>
    );
  }

  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{field.label}{description}</Label>
      <Input
        defaultValue={field.default}
        id={id}
        max={field.type === "string" ? field.maxLength : field.maximum}
        min={field.type === "string" ? field.minLength : field.minimum}
        name={field.name}
        pattern={field.type === "string" ? field.pattern : undefined}
        required={field.required}
        step={field.type === "integer" ? 1 : undefined}
        type={field.type === "string" ? stringInputType(field.format) : "number"}
      />
    </div>
  );
}

function stringInputType(format: "date" | "date-time" | "email" | "uri" | undefined): "date" | "datetime-local" | "email" | "text" | "url" {
  switch (format) {
    case "email": return "email";
    case "uri": return "url";
    case "date": return "date";
    case "date-time": return "datetime-local";
    default: return "text";
  }
}
