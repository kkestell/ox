import { createSignal, createStore, createUniqueId, For, flush, Show, snapshot, untrack } from "solid-js";
import { Markdown } from "./Markdown";
import type { Approval } from "./ox";
import type { ApprovalResponse } from "./protocol";
import { planOf, type Question, questionsOf, toolInputJSON, toolLabel, toolSubject } from "./toolInput";

type Choice = "allow" | "deny" | "other";

type Props = { approval: Approval; onAnswer(response: ApprovalResponse): Promise<unknown> };

// ApprovalRequest answers one approval. An AskUserQuestion call shows its
// questions as a form; any other approval shows a card.
export function ApprovalRequest(props: Props) {
  return (
    <Show
      when={questionsOf(props.approval.request.tool_name, props.approval.request.input)}
      fallback={<PermissionCard {...props} />}
    >
      {(questions) => <QuestionForm {...props} questions={questions()} />}
    </Show>
  );
}

// PermissionCard answers with Allow, Deny, or a typed reply, which denies with
// the reply as Claude's message. Typing picks Other, and picking a choice
// clears the reply.
function PermissionCard(props: Props) {
  const id = createUniqueId();
  const [choice, setChoice] = createSignal<Choice>();
  const [reply, setReply] = createSignal("");
  const [sending, setSending] = createSignal(false);
  let field!: HTMLInputElement;
  const plan = () => planOf(props.approval.request.tool_name, props.approval.request.input);
  const subject = () =>
    (plan() ? "" : toolSubject(props.approval.request.tool_name, props.approval.request.input)) || "Input";
  const ready = () => choice() === "allow" || choice() === "deny" || (choice() === "other" && !!reply().trim());

  const pick = (picked: Choice) => {
    setChoice(picked);
    if (picked === "other") field.focus();
    else setReply("");
  };
  const submit = () => {
    if (!ready() || sending()) return;
    const response: ApprovalResponse =
      choice() === "allow"
        ? { behavior: "allow", updatedInput: snapshot(props.approval.request.input) }
        : { behavior: "deny", message: choice() === "other" ? reply().trim() : "Denied by user" };
    setSending(true);
    props.onAnswer(response).finally(() => setSending(false));
  };

  const radio = (value: Choice, label?: string) => (
    <input
      type="radio"
      id={`${id}-${value}`}
      name={id}
      checked={choice() === value}
      disabled={sending()}
      aria-label={label}
      onChange={() => pick(value)}
    />
  );

  return (
    <article class="approval" aria-labelledby={`${id}-title`}>
      <p id={`${id}-title`}>Allow {toolLabel(props.approval.request.tool_name)}?</p>
      <Show when={plan()}>{(text) => <Markdown class="plan" text={text()} />}</Show>
      {/* The subject summarizes the input; expanding shows all of it. */}
      <details class="input">
        <summary>
          <span class="subject">{subject()}</span>
          <span class="icon chevron-down" />
        </summary>
        <pre>{toolInputJSON(props.approval.request.input)}</pre>
      </details>
      <div class="choices" role="radiogroup" aria-label="Choices">
        <div class="choice">
          {radio("allow")}
          <label for={`${id}-allow`}>Allow</label>
        </div>
        <div class="choice">
          {radio("deny")}
          <label for={`${id}-deny`}>Deny</label>
        </div>
        <div class="choice">
          {radio("other", "Other")}
          <input
            ref={field}
            type="text"
            placeholder="Other"
            aria-label="Other response"
            value={reply()}
            disabled={sending()}
            onInput={(event) => {
              setReply(event.currentTarget.value);
              setChoice("other");
            }}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.isComposing) submit();
            }}
          />
        </div>
      </div>
      <div class="actions">
        <button type="button" class="primary" disabled={!ready() || sending()} onClick={submit}>
          Submit
        </button>
      </div>
    </article>
  );
}

type Answer = { labels: string[]; other: boolean; text: string };

// answerOf is the answer to a question, or "" when it is unanswered.
function answerOf(question: Question, a: Answer) {
  const text = a.other ? a.text.trim() : "";
  if (!question.multiSelect) return text || a.labels[0] || "";
  const chosen = question.options.map((o) => o.label).filter((label) => a.labels.includes(label));
  return (text ? [...chosen, text] : chosen).join(", ");
}

// QuestionForm answers an AskUserQuestion call one question at a time. Next
// waits for an answer to the shown question, and Submit, on the last, allows
// with the original input plus the answers by question text; Decline denies.
function QuestionForm(props: Props & { questions: Question[] }) {
  const id = createUniqueId();
  // An approval's input never changes, so its questions are fixed for the
  // form's lifetime.
  const questions = untrack(() => props.questions);
  const [answers, setAnswers] = createStore<Answer[]>(questions.map(() => ({ labels: [], other: false, text: "" })));
  const [page, setPage] = createSignal(0);
  const [sending, setSending] = createSignal(false);
  let form!: HTMLElement;
  const paged = questions.length > 1;
  const last = () => page() === questions.length - 1;
  const current = () => answerOf(questions[page()], answers[page()]) !== "";
  const answered = () => questions.every((q, i) => answerOf(q, answers[i]) !== "");

  const send = (response: ApprovalResponse) => {
    if (sending()) return;
    setSending(true);
    props.onAnswer(response).finally(() => setSending(false));
  };
  const submit = () => {
    if (!answered()) return;
    const result: Record<string, string> = {};
    questions.forEach((q, i) => {
      result[q.question] = answerOf(q, answers[i]);
    });
    send({ behavior: "allow", updatedInput: { ...snapshot(props.approval.request.input), answers: result } });
  };
  // go shows another question and focuses its answer, or its first input, so
  // focus never rests on a hidden question or a newly disabled button.
  const go = (to: number) => {
    setPage(to);
    flush();
    const shown = form.querySelectorAll(".question")[to];
    (shown.querySelector<HTMLInputElement>("input:checked") ?? shown.querySelector("input"))?.focus();
  };
  const advance = () => {
    if (last()) submit();
    else if (current() && !sending()) go(page() + 1);
  };
  const pick = (i: number, question: Question, label: string, checked: boolean) =>
    setAnswers((draft) => {
      const a = draft[i];
      if (!question.multiSelect) {
        a.labels = [label];
        a.other = false;
      } else if (checked) a.labels.push(label);
      else a.labels = a.labels.filter((l) => l !== label);
    });

  return (
    <article ref={form} class="approval" aria-labelledby={`${id}-title`}>
      <p id={`${id}-title`}>Claude asks</p>
      <For each={questions}>
        {(question, i) => (
          <fieldset class="question" hidden={i() !== page()}>
            <legend>
              <Show when={question.header}>{(header) => <span class="header">{header()}</span>}</Show>
              {question.question}
            </legend>
            <For each={question.options}>
              {(option, j) => (
                <div class="choice">
                  <input
                    type={question.multiSelect ? "checkbox" : "radio"}
                    id={`${id}-${i()}-${j()}`}
                    name={`${id}-${i()}`}
                    checked={answers[i()].labels.includes(option.label)}
                    disabled={sending()}
                    onChange={(event) => pick(i(), question, option.label, event.currentTarget.checked)}
                  />
                  <label for={`${id}-${i()}-${j()}`}>
                    {option.label}
                    <Show when={option.description}>{(text) => <span class="description">{text()}</span>}</Show>
                  </label>
                </div>
              )}
            </For>
            <div class="choice">
              <input
                type={question.multiSelect ? "checkbox" : "radio"}
                name={`${id}-${i()}`}
                aria-label="Other"
                checked={answers[i()].other}
                disabled={sending()}
                onChange={(event) =>
                  setAnswers((draft) => {
                    draft[i()].other = event.currentTarget.checked;
                    if (!question.multiSelect) draft[i()].labels = [];
                  })
                }
              />
              <input
                type="text"
                placeholder="Other"
                aria-label="Other answer"
                value={answers[i()].text}
                disabled={sending()}
                onInput={(event) =>
                  setAnswers((draft) => {
                    draft[i()].text = event.currentTarget.value;
                    draft[i()].other = true;
                    if (!question.multiSelect) draft[i()].labels = [];
                  })
                }
                onKeyDown={(event) => {
                  if (event.key === "Enter" && !event.isComposing) advance();
                }}
              />
            </div>
          </fieldset>
        )}
      </For>
      <details class="input">
        <summary>
          <span class="subject">Input</span>
          <span class="icon chevron-down" />
        </summary>
        <pre>{toolInputJSON(props.approval.request.input)}</pre>
      </details>
      <div class="actions">
        <Show when={paged}>
          <span class="position">
            {page() + 1} of {questions.length}
          </span>
        </Show>
        <button
          type="button"
          disabled={sending()}
          onClick={() => send({ behavior: "deny", message: "User declined to answer" })}
        >
          Decline
        </button>
        <Show when={paged}>
          <button type="button" disabled={page() === 0 || sending()} onClick={() => go(page() - 1)}>
            Previous
          </button>
        </Show>
        <Show
          when={last()}
          fallback={
            <button type="button" class="primary" disabled={!current() || sending()} onClick={() => go(page() + 1)}>
              Next
            </button>
          }
        >
          <button type="button" class="primary" disabled={!answered() || sending()} onClick={submit}>
            Submit
          </button>
        </Show>
      </div>
    </article>
  );
}
