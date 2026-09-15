import { type ReactNode, useState } from "react";

// Technical detail is disclosed beneath the activity it belongs to. The trigger
// is a button so it carries a name and an expanded state, and the content is
// absent rather than hidden so a collapsed transcript entry costs nothing.
export function Disclosure({ children, className, label }: { children: ReactNode; className?: string; label: ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <div className={className}>
      <button aria-expanded={open} onClick={() => setOpen(!open)} type="button">
        {label}
      </button>
      {open ? children : null}
    </div>
  );
}
