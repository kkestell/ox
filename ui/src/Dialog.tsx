import { type ReactNode, useEffect, useId, useRef } from "react";

/**
 * A modal dialog, open while it is rendered, with a heading, a body, and a
 * row of action buttons. Escape calls `onClose`.
 */
export function Dialog({
  title,
  children,
  actions,
  onClose,
}: {
  title: string;
  children: ReactNode;
  actions: ReactNode;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const heading = useId();

  useEffect(() => {
    const dialog = ref.current!;
    if (!dialog.open) dialog.showModal();
  }, []);

  return (
    <dialog
      ref={ref}
      className="dialog"
      aria-labelledby={heading}
      onCancel={(event) => {
        // The parent closes the dialog by no longer rendering it.
        event.preventDefault();
        onClose();
      }}
    >
      <h2 id={heading}>{title}</h2>
      {children}
      <div className="actions">{actions}</div>
    </dialog>
  );
}
