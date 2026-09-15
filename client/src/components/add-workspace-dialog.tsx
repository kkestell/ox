import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

export function AddWorkspaceDialog({ message, onClose, onPath, onRegister, open, path }: {
  message?: { error: boolean; text: string };
  onClose: () => void;
  onPath: (path: string) => void;
  onRegister: () => void;
  open: boolean;
  path: string;
}) {
  return (
    <Dialog onOpenChange={(next) => { if (!next) onClose(); }} open={open}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add workspace</DialogTitle>
          <DialogDescription>
            Enter the full path to a project folder on the server running Ox.
          </DialogDescription>
        </DialogHeader>
        <form className="space-y-4" onSubmit={(event) => { event.preventDefault(); onRegister(); }}>
          <div className="space-y-1.5">
            <Label htmlFor="workspace-path">Workspace path</Label>
            <Input
              autoFocus
              id="workspace-path"
              onChange={(event) => onPath(event.target.value)}
              placeholder="/Users/name/project"
              required
              value={path}
            />
          </div>
          {message?.error ? (
            <Alert variant="destructive">
              <AlertDescription>{message.text}</AlertDescription>
            </Alert>
          ) : null}
          <DialogFooter>
            <Button onClick={onClose} type="button" variant="outline">Cancel</Button>
            <Button type="submit">Register workspace</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
