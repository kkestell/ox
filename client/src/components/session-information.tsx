import { type SessionTranscript } from "../protocol.ts";
import { CheckIcon, ChevronRightIcon } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";

// Desktop has space for explicit controls. The phone composer follows the
// established chat pattern: one model-and-effort pill, with full choices in a
// bottom sheet rather than a crowded control row.
export function SessionInformation({ onConfigOption, transcript }: {
  onConfigOption: (configId: string, value: string) => void;
  transcript: SessionTranscript;
}) {
  return (
    <>
      <div className="min-w-0 flex-1 sm:hidden">
        <MobileSessionSettings onConfigOption={onConfigOption} transcript={transcript} />
      </div>
      <div className="hidden min-w-0 flex-1 items-center gap-2 sm:flex">
        {transcript.configuration.map((option) => (
          <div className="flex min-w-0" key={option.id}>
            <Select onValueChange={(value) => onConfigOption(option.id, value)} value={option.currentValue}>
              <SelectTrigger aria-label={option.name} className="max-w-44" id={`config-${option.id}`} size="sm">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {option.options.map((choice) => (
                  <SelectItem key={choice.value} value={choice.value}>{choice.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        ))}
        {transcript.usage ? <UsageInformation usage={transcript.usage} /> : null}
      </div>
    </>
  );
}

function UsageInformation({ usage }: { usage: NonNullable<SessionTranscript["usage"]> }) {
  const context = `${usage.used.toLocaleString()} / ${usage.size.toLocaleString()}`;
  const label = usage.cost ? `${context} tokens, ${formatCost(usage.cost)}` : `${context} tokens`;
  const progress = usage.size > 0 ? Math.min(Math.max(usage.used / usage.size, 0), 1) : 0;

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button
          aria-label={`Show usage: ${label}`}
          className="ml-auto text-muted-foreground hover:text-foreground"
          size="icon-sm"
          title="Show usage"
          type="button"
          variant="ghost"
        >
          <UsageRing progress={progress} />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-auto min-w-52 space-y-2 p-3" side="top">
        <div>
          <p className="text-xs text-muted-foreground">Context</p>
          <p className="text-sm font-medium tabular-nums">{context} tokens</p>
        </div>
        {usage.cost ? (
          <div>
            <p className="text-xs text-muted-foreground">Cost</p>
            <p className="text-sm font-medium tabular-nums">{formatCost(usage.cost)}</p>
          </div>
        ) : null}
      </PopoverContent>
    </Popover>
  );
}

function UsageRing({ progress }: { progress: number }) {
  const radius = 7;
  const circumference = 2 * Math.PI * radius;

  return (
    <svg aria-hidden="true" className="size-5 -rotate-90" viewBox="0 0 20 20">
      <circle className="stroke-muted-foreground/35" cx="10" cy="10" fill="none" r={radius} strokeWidth="3" />
      <circle
        className="stroke-current"
        cx="10"
        cy="10"
        fill="none"
        r={radius}
        strokeDasharray={circumference}
        strokeDashoffset={circumference * (1 - progress)}
        strokeLinecap="butt"
        strokeWidth="3"
      />
    </svg>
  );
}

type ConfigurationOption = SessionTranscript["configuration"][number];

function formatCost(cost: NonNullable<NonNullable<SessionTranscript["usage"]>["cost"]>): string {
  const subCent = cost.amount > 0 && cost.amount < 0.01;
  return new Intl.NumberFormat("en-US", {
    currency: cost.currency,
    style: "currency",
    ...(subCent
      ? { maximumSignificantDigits: 2, minimumSignificantDigits: 2 }
      : { maximumFractionDigits: 2, minimumFractionDigits: 2 }),
  }).format(cost.amount);
}

function MobileSessionSettings({ onConfigOption, transcript }: {
  onConfigOption: (configId: string, value: string) => void;
  transcript: SessionTranscript;
}) {
  const primary = transcript.configuration.find((option) => option.id === "model") ?? transcript.configuration[0];
  const [open, setOpen] = useState(false);
  const [view, setView] = useState(primary?.id ?? "");
  const selected = transcript.configuration.find((option) => option.id === view) ?? primary;
  if (!primary || !selected) return null;
  const primaryId = primary.id;

  function setSheetOpen(nextOpen: boolean): void {
    setOpen(nextOpen);
    if (nextOpen) setView(primaryId);
  }

  function choose(option: ConfigurationOption, value: string): void {
    onConfigOption(option.id, value);
    setOpen(false);
  }

  return (
    <Sheet onOpenChange={setSheetOpen} open={open}>
      <SheetTrigger asChild>
        <Button
          aria-label="Choose model and reasoning"
          className="max-w-full rounded-full bg-muted px-3 text-xs text-foreground hover:bg-muted/80"
          size="sm"
          type="button"
          variant="ghost"
        >
          <span className="truncate">{mobileSummary(transcript.configuration)}</span>
        </Button>
      </SheetTrigger>
      <SheetContent
        className="gap-3 rounded-t-2xl border-x px-4 pt-4 pb-[calc(1rem+env(safe-area-inset-bottom))]"
        side="bottom"
      >
        <SheetHeader className="p-0 pr-8">
          <SheetTitle>{selected.id === primary.id ? "Select model" : `Select ${selected.name.toLowerCase()}`}</SheetTitle>
        </SheetHeader>
        <div className="overflow-hidden rounded-xl border bg-card">
          {selected.options.map((choice) => (
            <button
              className="flex w-full items-start gap-3 border-b px-3 py-3 text-left last:border-b-0 hover:bg-muted/50"
              key={choice.value}
              onClick={() => choose(selected, choice.value)}
              type="button"
            >
              <span className="min-w-0 flex-1">
                <span className="block text-sm font-medium">{choice.name}</span>
                {choice.description ? <span className="mt-0.5 block text-xs text-muted-foreground">{choice.description}</span> : null}
              </span>
              {choice.value === selected.currentValue ? <CheckIcon className="mt-0.5 size-4 shrink-0 text-primary" /> : null}
            </button>
          ))}
        </div>
        {selected.id === primary.id ? (
          <div className="space-y-1">
            {transcript.configuration.filter((option) => option.id !== primary.id).map((option) => (
              <button
                className="flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left hover:bg-muted"
                key={option.id}
                onClick={() => setView(option.id)}
                type="button"
              >
                <span className="text-sm font-medium">{option.name}</span>
                <span className="ml-auto min-w-0 truncate text-sm text-muted-foreground">{currentName(option)}</span>
                <ChevronRightIcon className="size-4 shrink-0 text-muted-foreground" />
              </button>
            ))}
          </div>
        ) : (
          <button
            className="self-start px-3 py-2 text-sm text-muted-foreground hover:text-foreground"
            onClick={() => setView(primary.id)}
            type="button"
          >
            Back to model
          </button>
        )}
      </SheetContent>
    </Sheet>
  );
}

function currentName(option: ConfigurationOption): string {
  return option.options.find((choice) => choice.value === option.currentValue)?.name ?? option.currentValue;
}

function mobileSummary(options: ConfigurationOption[]): string {
  const model = options.find((option) => option.id === "model");
  const reasoning = options.find((option) => option.id === "reasoning");
  const modelName = model ? currentName(model).match(/GPT-[\d.]+(?:\s+\w+)?/)?.[0] ?? currentName(model) : "Settings";
  return reasoning ? `${modelName} ${currentName(reasoning)}` : modelName;
}
