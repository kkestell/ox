import { invoke } from "@tauri-apps/api/core";
import { useEffect, useRef, useState } from "react";
import type { EnumOption, Field, Item } from "./protocol";
import { showError } from "./showError";

/** What a field holds while the form is open: text for text and number
 * inputs, a value for a choice, values for a multiple choice. */
type Value = string | boolean | string[];

/** The input type for a string's `format`. */
const inputTypes: Record<string, string> = {
  email: "email",
  uri: "url",
  date: "date",
  "date-time": "datetime-local",
};

/** A `date-time` value as a `datetime-local` input shows it, in local time. */
function localDateTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
}

function initial(field: Field): Value {
  switch (field.type) {
    case "boolean":
      return field.default ?? false;
    case "array":
      return field.default ?? [];
    case "number":
    case "integer":
      return field.default?.toString() ?? "";
    case "string":
      if (field.default === undefined) return "";
      return field.format === "date-time" ? localDateTime(field.default) : field.default;
    default:
      return "";
  }
}

/** The answer a field sends, or `undefined` when it is left empty. */
function answer(field: Field, value: Value | undefined) {
  switch (field.type) {
    case "boolean":
      return value;
    case "array":
      return (value as string[]).length > 0 ? value : undefined;
    case "number":
    case "integer":
      return value === "" ? undefined : Number(value);
    case "string":
      if (value === "") return undefined;
      return field.format === "date-time" ? new Date(value as string).toISOString() : value;
    default:
      return undefined;
  }
}

/** A string or array field's choices, with plain values as their own titles. */
function choices(values: string[] | undefined, options: EnumOption[] | undefined): EnumOption[] {
  return options ?? values?.map((value) => ({ const: value, title: value })) ?? [];
}

/** Why a multiple choice's selection is invalid, or `""` when it is valid.
 * An optional field left empty is omitted, so its bounds do not apply. */
function selectionError(field: Extract<Field, { type: "array" }>, count: number) {
  if (count === 0 && !field.required) return "";
  const min = Math.max(field.minItems ?? 0, 1);
  if (count < min) return `Select at least ${min}.`;
  if (field.maxItems !== undefined && count > field.maxItems) {
    return `Select at most ${field.maxItems}.`;
  }
  return "";
}

/** Why a text field's value does not match its pattern, or `""` when it
 * does. A JSON Schema pattern is an ECMAScript regular expression that may
 * match anywhere in the value. A pattern that does not compile is not
 * checked. */
function patternError(pattern: string, value: string) {
  let regex: RegExp;
  try {
    regex = new RegExp(pattern, "u");
  } catch {
    return "";
  }
  return value === "" || regex.test(value) ? "" : `Enter a value that matches ${pattern}.`;
}

/** Why a field's value is invalid by the checks the browser does not make,
 * or `""` when it is valid. */
function customError(field: Field, value: Value | undefined) {
  if (field.type === "array") return selectionError(field, (value as string[]).length);
  if (field.type === "string" && field.pattern !== undefined) {
    return patternError(field.pattern, value as string);
  }
  return "";
}

/** A form the agent asks the user to fill in, with its fields. */
export function Elicitation({
  item,
  sessionId,
}: {
  item: Extract<Item, { type: "elicitation" }>;
  sessionId: string;
}) {
  const [values, setValues] = useState<Record<string, Value>>(() =>
    Object.fromEntries(item.fields.map((field) => [field.name, initial(field)])),
  );
  const [pending, setPending] = useState(false);
  const form = useRef<HTMLFormElement>(null);

  const set = (name: string, value: Value) =>
    setValues((current) => ({ ...current, [name]: value }));

  // A text field's pattern and a multiple choice's bounds are checked with
  // the browser's own validation, on the field's input or the group's first
  // checkbox.
  useEffect(() => {
    for (const field of item.fields) {
      const group = form.current?.elements.namedItem(field.name);
      const first = group instanceof RadioNodeList ? group[0] : group;
      if (first instanceof HTMLInputElement) {
        first.setCustomValidity(customError(field, values[field.name]));
      }
    }
  }, [item.fields, values]);

  const respond = async (action: object) => {
    setPending(true);
    try {
      await invoke("respond_elicitation", { sessionId, requestId: item.request_id, action });
    } catch (e) {
      setPending(false);
      await showError(e);
    }
  };

  const submit = () => {
    const content: Record<string, unknown> = {};
    for (const field of item.fields) {
      const value = answer(field, values[field.name]);
      if (value !== undefined) content[field.name] = value;
    }
    respond({ action: "accept", content });
  };

  const control = (field: Field) => {
    const value = values[field.name];
    const label = field.title ?? field.name;
    switch (field.type) {
      case "string": {
        if (field.oneOf || field.enum) {
          const options = choices(field.enum, field.oneOf);
          if (options.length === 0) return null;
          return options.map((option, index) => (
            <Choice
              key={option.const}
              type="radio"
              name={field.name}
              option={option}
              required={field.required && index === 0}
              checked={value === option.const}
              onChange={() => set(field.name, option.const)}
            />
          ));
        }
        return (
          <input
            className="text-field"
            name={field.name}
            type={inputTypes[field.format ?? ""] ?? "text"}
            aria-label={label}
            required={field.required}
            minLength={field.minLength}
            maxLength={field.maxLength}
            value={value as string}
            onChange={(event) => set(field.name, event.target.value)}
          />
        );
      }
      case "number":
      case "integer":
        return (
          <input
            className="text-field"
            name={field.name}
            type="number"
            aria-label={label}
            required={field.required}
            min={field.minimum}
            max={field.maximum}
            step={field.type === "integer" ? 1 : "any"}
            value={value as string}
            onChange={(event) => set(field.name, event.target.value)}
          />
        );
      case "boolean":
        return (
          <Choice
            type="checkbox"
            name={field.name}
            option={{ const: "true", title: label, description: field.description }}
            checked={value as boolean}
            onChange={(checked) => set(field.name, checked)}
          />
        );
      case "array": {
        // Items of a type the app does not know have no choices to show.
        const options = choices(field.items.enum, field.items.anyOf);
        if (options.length === 0) return null;
        const selected = value as string[];
        return options.map((option) => (
          <Choice
            key={option.const}
            type="checkbox"
            name={field.name}
            option={option}
            checked={selected.includes(option.const)}
            // The selection keeps the options' order.
            onChange={(checked) =>
              set(
                field.name,
                options
                  .map((choice) => choice.const)
                  .filter((choice) =>
                    choice === option.const ? checked : selected.includes(choice),
                  ),
              )
            }
          />
        ));
      }
      default:
        return null;
    }
  };

  return (
    <form
      ref={form}
      className="request-panel"
      onSubmit={(event) => {
        event.preventDefault();
        submit();
      }}
    >
      <div className="scroll">
        <p className="title">{item.message}</p>
        {item.fields.map((field) => {
          const input = control(field);
          if (input === null) return null;
          // A boolean's checkbox carries its title and description.
          if (field.type === "boolean") {
            return (
              <fieldset key={field.name} disabled={pending}>
                {input}
              </fieldset>
            );
          }
          return (
            <fieldset key={field.name} disabled={pending}>
              <legend>{field.title ?? field.name}</legend>
              {field.description && <p>{field.description}</p>}
              {input}
            </fieldset>
          );
        })}
      </div>
      <div className="actions">
        <button
          type="button"
          className="text-button"
          disabled={pending}
          onClick={() => respond({ action: "decline" })}
        >
          Decline
        </button>
        <button type="submit" className="text-button" disabled={pending}>
          Submit
        </button>
      </div>
    </form>
  );
}

/** A radio or checkbox row: the input, the option's title, and its description. */
function Choice({
  type,
  name,
  option,
  required,
  checked,
  onChange,
}: {
  type: "radio" | "checkbox";
  name: string;
  option: EnumOption;
  required?: boolean;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <label className="choice">
      <input
        type={type}
        name={name}
        value={option.const}
        required={required}
        checked={checked}
        onChange={(event) => onChange(event.target.checked)}
      />
      {option.title}
      {option.description && <span className="description">{option.description}</span>}
    </label>
  );
}
