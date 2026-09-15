import { useEffect, useRef } from "react";

export function AddWorkspaceDialog({ message, onClose, onPath, onRegister, open, path }: {
  message?: { error: boolean; text: string };
  onClose: () => void;
  onPath: (path: string) => void;
  onRegister: () => void;
  open: boolean;
  path: string;
}) {
  const dialog = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    const element = dialog.current;
    if (!element) return;
    if (open) {
      if (!element.open) element.showModal();
    } else if (element.open) {
      element.close();
    }
  }, [open]);

  return (
    <dialog aria-labelledby="add-workspace-heading" onClose={onClose} ref={dialog}>
      <h2 id="add-workspace-heading">Add workspace</h2>
      <p>Register a server-local absolute path. Ox runs one process for each workspace.</p>
      <form onSubmit={(event) => { event.preventDefault(); onRegister(); }}>
        <label>
          Workspace path
          <input autoFocus onChange={(event) => onPath(event.target.value)} required value={path} />
        </label>
        {message?.error ? <p role="alert">{message.text}</p> : null}
        <menu>
          <li><button className="outline" onClick={onClose} type="button">Cancel</button></li>
          <li><button className="solid" type="submit">Register workspace</button></li>
        </menu>
      </form>
    </dialog>
  );
}
