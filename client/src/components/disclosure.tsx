import { ChevronRightIcon } from "lucide-react";
import { type ReactNode, useState } from "react";

import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { cn } from "cn";

// Technical detail is disclosed beneath the activity it belongs to. The content
// is absent rather than hidden so a collapsed transcript entry costs nothing.
export function Disclosure({ children, className, contentClassName, icon, label }: {
  children: ReactNode;
  className?: string;
  contentClassName?: string;
  icon?: ReactNode;
  label: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  return (
    <Collapsible className={className} onOpenChange={setOpen} open={open}>
      <CollapsibleTrigger asChild>
        <Button
          className="group h-auto min-h-8 min-w-0 w-full items-start justify-start gap-2 whitespace-normal px-1 py-1.5 font-normal text-muted-foreground hover:bg-transparent hover:text-foreground dark:hover:bg-transparent"
          size="sm"
          variant="ghost"
        >
          {icon ?? <ChevronRightIcon className="mt-0.5 shrink-0 transition-transform group-data-[state=open]:rotate-90" />}
          {label}
        </Button>
      </CollapsibleTrigger>
      {open ? (
        <CollapsibleContent className={cn("ml-[18px] min-w-0 space-y-3 border-l pl-4 pt-2 text-sm", contentClassName)}>
          {children}
        </CollapsibleContent>
      ) : null}
    </Collapsible>
  );
}
