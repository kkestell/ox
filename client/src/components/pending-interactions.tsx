import { type FormValue, type PendingInteraction } from "../protocol.ts";

export function PendingInteractions({ interactions, onElicitation, onPermission }: {
  interactions: PendingInteraction[];
  onElicitation: (interactionId: string, action: "accept" | "decline" | "cancel", content?: Record<string, FormValue>) => void;
  onPermission: (interactionId: string, optionId: string) => void;
}) {
  if (interactions.length === 0) return null;
  return (
    <aside aria-label="Pending interactions" className="pending-interactions">
      {interactions.map((interaction) => interaction.kind === "permission" ? (
        <PermissionInteraction interaction={interaction} key={interaction.id} onPermission={onPermission} />
      ) : (
        <ElicitationForm interaction={interaction} key={interaction.id} onSubmit={onElicitation} />
      ))}
    </aside>
  );
}

export function PermissionInteraction({ interaction, onPermission }: {
  interaction: Extract<PendingInteraction, { kind: "permission" }>;
  onPermission: (interactionId: string, optionId: string) => void;
}) {
  return (
    <article aria-label={`Permission for ${interaction.tool.title}`} className="pending-interaction permission-interaction">
      <header><h3>{interaction.tool.title}</h3></header>
      <div className="interaction-body">
        {interaction.tool.name ? <p>{interaction.tool.name}</p> : null}
        {interaction.tool.toolKind ? <p>{interaction.tool.toolKind}</p> : null}
        <div className="interaction-actions">
          {interaction.options.map((option) => (
            <button key={option.id} onClick={() => onPermission(interaction.id, option.id)} type="button">
              {option.name}
            </button>
          ))}
        </div>
      </div>
    </article>
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
    <article aria-label={interaction.title ?? interaction.message} className="pending-interaction elicitation-interaction">
      <header><h3>{interaction.title ?? "Question"}</h3></header>
      <div className="interaction-body">
      <p>{interaction.message}</p>
      {interaction.description ? <p>{interaction.description}</p> : null}
      <form className="interaction-form" onSubmit={(event) => {
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
          <label key={field.name}>
            {field.label}
            {field.description ? <span>{field.description}</span> : null}
            {field.type === "boolean" ? (
              <input defaultChecked={field.default} name={field.name} type="checkbox" />
            ) : field.type === "multi-select" ? (
              <select defaultValue={field.default} multiple name={field.name} required={field.required}>
                {field.choices.map((choice) => <option key={choice.value} value={choice.value}>{choice.label}</option>)}
              </select>
            ) : field.type === "string" && field.choices ? (
              <select defaultValue={field.default ?? ""} name={field.name} required={field.required}>
                {field.default === undefined ? <option disabled value="">Select an option</option> : null}
                {field.choices.map((choice) => <option key={choice.value} value={choice.value}>{choice.label}</option>)}
              </select>
            ) : (
              <input defaultValue={field.default} max={field.type === "string" ? field.maxLength : field.maximum} min={field.type === "string" ? field.minLength : field.minimum} name={field.name} pattern={field.type === "string" ? field.pattern : undefined} required={field.required} step={field.type === "integer" ? 1 : undefined} type={field.type === "string" ? stringInputType(field.format) : "number"} />
            )}
          </label>
        ))}
        <div className="interaction-actions">
          <button type="submit">Submit answer</button>
          <button onClick={() => onSubmit(interaction.id, "decline")} type="button">Decline</button>
          <button onClick={() => onSubmit(interaction.id, "cancel")} type="button">Cancel question</button>
        </div>
      </form>
      </div>
    </article>
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
