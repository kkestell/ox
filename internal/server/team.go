package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ox/internal/agent"
	"ox/internal/catalog"
	"ox/internal/settings"
	"ox/internal/store"
	"ox/internal/tools"
	"ox/internal/transcript"
	"ox/internal/worktree"
)

type team struct {
	server     *Server
	id, parent string
	name       string
	selections settings.Settings
}

var _ tools.Team = (*team)(nil)

func (s *Server) team(id string, selections settings.Settings) (*team, error) {
	t := &team{server: s, id: id, parent: id, name: "orchestrator", selections: selections}
	member, err := s.agent.Store.Member(id)
	if err == nil {
		t.parent, t.name = member.Parent, member.Name
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	return t, nil
}

func (t *team) IsWorker() bool { return t.id != t.parent }

func (t *team) WorkerModels() string {
	t.server.mu.Lock()
	defer t.server.mu.Unlock()
	return t.server.defaults.WorkerModels
}

func (t *team) member(name string) (store.Member, error) {
	members, err := t.server.agent.Store.Members(t.parent)
	if err != nil {
		return store.Member{}, err
	}
	for _, member := range members {
		if member.Name == name {
			return member, nil
		}
	}
	return store.Member{}, fmt.Errorf("worker %s does not exist", name)
}

func (t *team) CreateAgent(ctx context.Context, args tools.CreateAgentArgs) (string, error) {
	if t.IsWorker() {
		return "", errors.New("workers cannot create workers")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s := t.server
	members, err := s.agent.Store.Members(t.parent)
	if err != nil {
		return "", err
	}
	for _, member := range members {
		if member.Name == args.Name {
			return "", fmt.Errorf("worker %s already exists", args.Name)
		}
	}
	selected := t.selections
	if args.Model != "" {
		selected.Model = args.Model
	}
	if args.Effort != "" {
		effort, ok := catalog.ParseEffort(args.Effort)
		if !ok {
			return "", fmt.Errorf("unknown effort %s", args.Effort)
		}
		selected.Effort = effort
	}
	model, err := agent.ModelSelection(s.catalog, selected.Model, selected.Effort)
	if err != nil {
		return "", err
	}
	selected.Model, selected.Mode = model.QualifiedID(), transcript.ModeAuto
	parent, err := s.agent.Store.Read(t.parent)
	if err != nil {
		return "", err
	}
	workspace, branch, root, dir, err := workerWorkspace(s.home, parent.Summary.Workspace, args)
	if err != nil {
		return "", err
	}
	created := false
	defer func() {
		if !created && branch != "" {
			if err := worktree.RemoveWorktree(root, dir, branch); err != nil {
				fmt.Fprintln(os.Stderr, "cleaning up worker worktree:", err)
			}
		}
	}()
	session, acpErr := s.newActiveSession(workspace, nil)
	if acpErr != nil {
		return "", acpErr
	}
	session.selections = selected
	summary, err := s.agent.Store.CreateWorker(t.parent, args.Name, workspace, branch, selected.Model, selected.Effort)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.active[summary.ID] = session
	s.mu.Unlock()
	member, err := s.agent.Store.Member(summary.ID)
	if err == nil {
		session.systemPrompt, err = s.workerPrompt(session.systemPrompt, member)
	}
	if err == nil {
		err = s.agent.Store.Send(summary.ID, t.name, args.Message)
	}
	if err != nil {
		s.deactivate(summary.ID)
		return "", errors.Join(err, s.agent.Store.Delete(summary.ID))
	}
	created = true
	if err := s.wake(summary.ID); err != nil {
		return "", err
	}
	s.change()
	return fmt.Sprintf("Worker %s created.\nWorkspace: %s\nBranch: %s", args.Name, workspace, branch), nil
}

func workerWorkspace(home, parent string, args tools.CreateAgentArgs) (workspace, branch, root, dir string, err error) {
	if args.Dir != "" {
		dir = args.Dir
		switch {
		case strings.HasPrefix(dir, "~/"):
			dir = filepath.Join(home, dir[2:])
		case !filepath.IsAbs(dir):
			dir = filepath.Join(parent, dir)
		}
		dir, err = filepath.EvalSymlinks(dir)
		if err != nil {
			return "", "", "", "", err
		}
		info, err := os.Stat(dir)
		if err != nil {
			return "", "", "", "", err
		}
		if !info.IsDir() {
			return "", "", "", "", fmt.Errorf("%s is not a directory", dir)
		}
		return dir, "", "", "", nil
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		return "", "", "", "", err
	}
	root, err = worktree.Root(parent)
	if err != nil {
		return "", "", "", "", fmt.Errorf("dir is required outside a Git checkout: %w", err)
	}
	branch, dir = worktree.WorkerBranch(args.Name), worktree.WorktreeDir(root, args.Name)
	workspace, err = worktree.Workspace(root, dir, parent)
	if err != nil {
		return "", "", "", "", err
	}
	if err := worktree.AddWorktree(root, dir, branch); err != nil {
		return "", "", "", "", err
	}
	return workspace, branch, root, dir, nil
}

func (t *team) SendMessage(ctx context.Context, to, text string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	id := t.parent
	if to != "orchestrator" {
		member, err := t.member(to)
		if err != nil {
			return "", err
		}
		id = member.ID
	}
	if err := t.server.send(id, t.name, text); err != nil {
		return "", err
	}
	if id != t.parent {
		if err := t.server.wake(id); err != nil {
			return "", err
		}
	}
	return "Message sent to " + to + ".", nil
}

func (s *Server) send(id, sender, text string) error {
	if err := s.agent.Store.Send(id, sender, text); err != nil {
		return err
	}
	s.change()
	return nil
}

func (s *Server) takeMessages(id string) ([]string, error) {
	messages, err := s.agent.Store.Take(id)
	if err != nil {
		return nil, err
	}
	var formatted []string
	for _, message := range messages {
		formatted = append(formatted, fmt.Sprintf("Message from %s:\n%s", message.Sender, message.Text))
	}
	return formatted, nil
}

func (s *Server) change() {
	s.mu.Lock()
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}

// selectionsFor reads the latest saved selections without activating a session.
func (s *Server) selectionsFor(id string) (settings.Settings, error) {
	if session := s.session(id); session != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		return session.selections, nil
	}
	saved, err := s.agent.Store.Read(id)
	if err != nil {
		return settings.Settings{}, err
	}
	if start := transcript.LatestTurnStart(saved.Transcript); start != nil {
		return settings.Settings{Model: start.Model, Effort: start.Effort, Mode: start.Mode}, nil
	}
	member, err := s.agent.Store.Member(id)
	if err == nil {
		return settings.Settings{Model: member.Model, Effort: member.Effort, Mode: transcript.ModeAuto}, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return settings.Settings{}, err
	}
	return s.defaultsFor(saved.Summary.Workspace)
}

func (t *team) ListAgents(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s := t.server
	parent, err := s.agent.Store.Read(t.parent)
	if err != nil {
		return "", err
	}
	members, err := s.agent.Store.Members(t.parent)
	if err != nil {
		return "", err
	}
	members = append([]store.Member{{Summary: parent.Summary, Name: "orchestrator"}}, members...)
	var lines []string
	for _, member := range members {
		selected, err := s.selectionsFor(member.ID)
		if err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("Name: %s\nModel: %s\nEffort: %s\nWorkspace: %s\nBranch: %s\nWorking: %t",
			member.Name, selected.Model, selected.Effort, member.Workspace, member.Branch, s.ops.working(member.ID)))
	}
	return strings.Join(lines, "\n\n"), nil
}

func (t *team) Wait(ctx context.Context) (string, error) {
	if t.IsWorker() {
		return "", errors.New("workers cannot wait")
	}
	s := t.server
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		s.mu.Lock()
		changed := s.changed
		s.mu.Unlock()
		messages, err := s.takeMessages(t.id)
		if err != nil {
			return "", err
		}
		if len(messages) > 0 {
			return strings.Join(messages, "\n\n"), nil
		}
		members, err := s.agent.Store.Members(t.parent)
		if err != nil {
			return "", err
		}
		working := false
		for _, member := range members {
			if s.ops.working(member.ID) {
				working = true
				break
			}
		}
		if !working {
			// A worker may have sent its report between Take and the check.
			select {
			case <-changed:
				continue
			default:
				return "No messages and no workers are working.", nil
			}
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

func (t *team) RemoveAgent(ctx context.Context, name string) (string, error) {
	if t.IsWorker() {
		return "", errors.New("workers cannot remove workers")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	member, err := t.member(name)
	if err != nil {
		return "", err
	}
	s := t.server
	s.mu.Lock()
	wait, release, ok := s.ops.beginClose(member.ID)
	if ok {
		s.removing[member.ID] = true
	}
	s.mu.Unlock()
	if !ok {
		return "", s.unavailable()
	}
	removed := false
	defer func() {
		s.mu.Lock()
		delete(s.removing, member.ID)
		s.mu.Unlock()
		release()
		if !removed {
			if err := s.wake(member.ID); err != nil {
				fmt.Fprintln(os.Stderr, "waking worker:", err)
			}
		}
		s.change()
	}()
	wait()
	if member.Branch != "" {
		parent, err := s.agent.Store.Read(t.parent)
		if err != nil {
			return "", err
		}
		root, err := worktree.Root(parent.Summary.Workspace)
		if err != nil {
			return "", err
		}
		if err := worktree.RemoveWorktree(root, worktree.WorktreeDir(root, name), member.Branch); err != nil {
			return "", err
		}
	}
	if session := s.session(member.ID); session != nil {
		session.processes.Shutdown()
		session.mcp.Close()
	}
	if err := s.agent.Store.Delete(member.ID); err != nil {
		return "", err
	}
	s.deactivate(member.ID)
	removed = true
	return "Worker " + name + " removed.", nil
}

func (s *Server) workerPrompt(prompt string, member store.Member) (string, error) {
	parent, err := s.agent.Store.Read(member.Parent)
	if err != nil {
		return "", err
	}
	members, err := s.agent.Store.Members(member.Parent)
	if err != nil {
		return "", err
	}
	members = append([]store.Member{{Summary: parent.Summary, Name: "orchestrator"}}, members...)
	var lines []string
	for _, member := range members {
		lines = append(lines, fmt.Sprintf("Name: %s\nWorkspace: %s\nBranch: %s", member.Name, member.Workspace, member.Branch))
	}
	instructions := "Follow the orchestrator's instructions about branches and commits."
	if member.Branch != "" {
		instructions = "Commit your work on your branch. The orchestrator merges it from its checkout."
	}
	return prompt + fmt.Sprintf("\n\n# Team\n\nYou are worker %s.\nWorkspace: %s\nBranch: %s\n\n%s\nYour final answer is sent to the orchestrator when your turn ends.\nUse send_message to contact team members while you work. Workers cannot create workers.\n\nTeam:\n\n%s", member.Name, member.Workspace, member.Branch, instructions, strings.Join(lines, "\n\n")), nil
}

// wake acquires the same operation as an ACP prompt before touching worker
// state. A busy worker reads its inbox; the final wake catches later arrivals.
func (s *Server) wake(id string) error {
	ctx, release, ok := s.ops.tryPrompt(id)
	if !ok {
		return nil
	}
	started := false
	defer func() {
		if !started {
			release()
		}
	}()
	has, err := s.agent.Store.HasMessages(id)
	if err != nil || !has {
		return err
	}
	member, err := s.agent.Store.Member(id)
	if err != nil {
		return err
	}
	session := s.session(id)
	if session == nil {
		selected, err := s.selectionsFor(id)
		if err != nil {
			return err
		}
		selected.Mode = transcript.ModeAuto
		if _, err := agent.ModelSelection(s.catalog, selected.Model, selected.Effort); err != nil {
			return err
		}
		loaded, sessionErr := s.newActiveSession(member.Workspace, nil)
		if sessionErr != nil {
			return sessionErr
		}
		session = loaded
		session.selections = selected
		session.systemPrompt, err = s.workerPrompt(session.systemPrompt, member)
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.active[id] = session
		s.mu.Unlock()
	}
	s.prompts.Add(1)
	started = true
	go func() {
		defer s.prompts.Done()
		result, saved, err := s.runTurn(ctx, id, transcript.TurnInput{}, session, workerClient{})
		s.mu.Lock()
		if !s.removing[id] {
			if err := s.agent.Store.Send(member.Parent, member.Name, workerReport(member.Name, result, err)); err != nil {
				fmt.Fprintln(os.Stderr, "saving worker report:", err)
			}
		}
		s.mu.Unlock()
		release()
		s.change()
		// A failed preparation leaves the same messages queued. Retry only
		// after a turn saved its input, or a new message or load wakes it.
		if saved || err == nil {
			if err := s.wake(id); err != nil {
				fmt.Fprintln(os.Stderr, "waking worker:", err)
			}
		}
	}()
	return nil
}

func workerReport(name string, result agent.Result, err error) string {
	if err != nil {
		return fmt.Sprintf("Worker %s failed:\n%s", name, err)
	}
	switch result.Stop {
	case agent.EndTurn:
		return fmt.Sprintf("Worker %s finished its turn:\n%s", name, result.Answer)
	case agent.Cancelled:
		return fmt.Sprintf("Worker %s was cancelled.", name)
	case agent.MaxTokens:
		return fmt.Sprintf("Worker %s stopped at the output limit.", name)
	case agent.Refusal:
		return fmt.Sprintf("Worker %s refused.", name)
	default:
		panic("unknown worker stop reason")
	}
}

type workerClient struct{}

func (workerClient) Send(agent.Event) error { return nil }
func (workerClient) Mode() transcript.Mode  { return transcript.ModeAuto }
func (workerClient) Approve(context.Context, transcript.ToolCall, tools.Permission) (bool, error) {
	return true, nil
}
