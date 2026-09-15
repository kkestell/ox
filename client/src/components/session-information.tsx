import { type SessionTranscript } from "../protocol.ts";

// The session's advertised options and its context usage, rendered inside the
// composer's control row. Each select is named by its option and shows its
// current value, so the row needs no visible labels.
export function SessionInformation({ onConfigOption, transcript }: {
  onConfigOption: (configId: string, value: string) => void;
  transcript: SessionTranscript;
}) {
  return (
    <>
      {transcript.configuration.map((option) => (
        <select
          aria-label={option.name}
          id={`config-${option.id}`}
          key={option.id}
          onChange={(event) => onConfigOption(option.id, event.target.value)}
          value={option.currentValue}
        >
          {option.options.map((choice) => <option key={choice.value} value={choice.value}>{choice.name}</option>)}
        </select>
      ))}
      {transcript.usage ? (
        <span className="usage">{`Context: ${transcript.usage.used} of ${transcript.usage.size} tokens used`}</span>
      ) : null}
    </>
  );
}
