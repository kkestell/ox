import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

import { type SessionTranscript } from "../protocol.ts";

export function SessionInformation({ onConfigOption, transcript }: {
  onConfigOption: (configId: string, value: string) => void;
  transcript: SessionTranscript;
}) {
  if (transcript.configuration.length === 0 && !transcript.usage) return null;
  return (
    <section aria-label="Session information">
      {transcript.configuration.map((option) => (
        <div key={option.id}>
          <Label htmlFor={`config-${option.id}`}>{option.name}</Label>
          <Select onValueChange={(value) => onConfigOption(option.id, value)} value={option.currentValue}>
            <SelectTrigger aria-label={option.name} id={`config-${option.id}`}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {option.options.map((choice) => <SelectItem key={choice.value} value={choice.value}>{choice.name}</SelectItem>)}
            </SelectContent>
          </Select>
        </div>
      ))}
      {transcript.usage ? (
        <span>{`Context: ${transcript.usage.used} of ${transcript.usage.size} tokens used`}</span>
      ) : null}
    </section>
  );
}
