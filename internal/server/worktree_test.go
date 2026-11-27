package server

import (
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/config"
	"github.com/kkestell/ox/internal/fake"
	"github.com/kkestell/ox/internal/store"
)

// runGit runs git with args and returns its trimmed output.
func runGit(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// gitRepo makes a git repository whose one commit holds sub/file.txt and a
// .gitignore that ignores *.log, with git kept from the user's own
// configuration, and returns its path.
func gitRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, variable := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(variable, "Ox")
	}
	for _, variable := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(variable, "ox@example.com")
	}
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"sub/file.txt": "committed\n", ".gitignore": "*.log\n.env*\n"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, "-C", repo, "init", "-q", "-b", "main")
	runGit(t, "-C", repo, "add", ".")
	runGit(t, "-C", repo, "commit", "-q", "-m", "init")
	return repo
}

// registered lists the worktrees git registers for repo.
func registered(t *testing.T, repo string) []string {
	t.Helper()
	var paths []string
	for line := range strings.Lines(runGit(t, "-C", repo, "worktree", "list", "--porcelain")) {
		if path, ok := strings.CutPrefix(strings.TrimSpace(line), "worktree "); ok {
			paths = append(paths, path)
		}
	}
	return paths
}

// oxBranches lists repo's branches named ox/….
func oxBranches(t *testing.T, repo string) []string {
	t.Helper()
	return strings.Fields(runGit(t, "-C", repo, "branch", "--list", "--format=%(refname:short)", "ox/*"))
}

// nothingLeft checks that repo has only its own checkout and no ox/ branch,
// and that worktrees holds nothing.
func nothingLeft(t *testing.T, repo, worktrees string) {
	t.Helper()
	if paths := registered(t, repo); len(paths) != 1 {
		t.Fatalf("git registers %v", paths)
	}
	if branches := oxBranches(t, repo); len(branches) != 0 {
		t.Fatalf("branches %v were left", branches)
	}
	if entries, err := os.ReadDir(worktrees); err == nil && len(entries) != 0 {
		t.Fatalf("the worktree directory holds %v", entries)
	}
}

// gitGate puts a stand-in for git first on PATH that holds each worktree add
// until open is called, and returns open and waiting, which returns once n
// worktree adds are held.
func gitGate(t *testing.T) (open func(), waiting func(n int)) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"case \" $* \" in *\" worktree add \"*)\n" +
		"  touch \"" + dir + "/waiting.$$\"\n" +
		"  while [ ! -e \"" + dir + "/open\" ]; do sleep .02; done;;\n" +
		"esac\n" +
		"exec \"" + real + "\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	open = func() {
		if err := os.WriteFile(filepath.Join(dir, "open"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(open)
	waiting = func(n int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			held, _ := filepath.Glob(filepath.Join(dir, "waiting.*"))
			if len(held) >= n {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%d worktree adds held, not %d", len(held), n)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	return open, waiting
}

// worktreeConfig offers the fake harness, which also titles sessions.
var worktreeConfig = &config.Config{
	Harnesses: map[string]config.Harness{"fake": {Models: []config.Model{{ID: "m"}}, PermissionModes: []config.Option{{ID: "manual"}}}},
	Titling:   config.Titling{Harness: "fake", Model: "m"},
}

// worktreeServer serves st with the fake harness, making worktrees in
// worktrees.
func worktreeServer(t *testing.T, st *store.Store, worktrees string) *Server {
	t.Helper()
	s, err := New(st, worktreeConfig, 0, map[string]agent.Harness{"fake": {Start: fake.Start, Ask: fake.Ask}}, MCP{}, worktrees)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// addWorkspace adds path as a workspace and returns its ID.
func addWorkspace(t *testing.T, c *client, path string) string {
	t.Helper()
	var workspace struct {
		WorkspaceID string `json:"workspace_id"`
	}
	json.Unmarshal(c.call("workspace/create", map[string]string{"path": path}), &workspace)
	return workspace.WorkspaceID
}

// createParams are session/create's params for the fake harness in a
// worktree.
func createParams(workspaceID, sessionID string) map[string]any {
	params := map[string]any{"workspace_id": workspaceID, "harness": "fake", "model": "m", "permission_mode": "manual", "worktree": true}
	if sessionID != "" {
		params["session_id"] = sessionID
	}
	return params
}

// listedWorktree is a session entry from workspace/list.
type listedWorktree struct {
	SessionID string `json:"session_id"`
	Directory string `json:"directory"`
	Worktree  string `json:"worktree"`
}

// allListed lists every session workspace/list returns.
func allListed(t *testing.T, c *client) []listedWorktree {
	t.Helper()
	var listed struct {
		Workspaces []struct {
			Sessions []listedWorktree `json:"sessions"`
		} `json:"workspaces"`
	}
	json.Unmarshal(c.call("workspace/list", map[string]any{}), &listed)
	var sessions []listedWorktree
	for _, w := range listed.Workspaces {
		sessions = append(sessions, w.Sessions...)
	}
	return sessions
}

// listed returns the entry for sessionID.
func listed(t *testing.T, c *client, sessionID string) listedWorktree {
	t.Helper()
	for _, sess := range allListed(t, c) {
		if sess.SessionID == sessionID {
			return sess
		}
	}
	t.Fatalf("session %s is not listed", sessionID)
	return listedWorktree{}
}

// worktreeSession creates a session in a worktree of the workspace and
// returns its entry.
func worktreeSession(t *testing.T, c *client, workspaceID string) listedWorktree {
	t.Helper()
	var created struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(c.call("session/create", createParams(workspaceID, "")), &created)
	sess := listed(t, c, created.SessionID)
	if sess.Worktree == "" {
		t.Fatalf("session %s was created without a worktree", sess.SessionID)
	}
	return sess
}

// A worktree session runs in the worktree's copy of its workspace, on a new
// branch, holding only committed files.
func TestWorktreeSession(t *testing.T) {
	repo := gitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "sub", "untracked.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// A failing hook would undo the checkout if it ran.
	hook := filepath.Join(repo, ".git", "hooks", "post-checkout")
	marker := filepath.Join(t.TempDir(), "hook-ran")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marker+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		".env": "secret\n", ".env.local": "local\n", "build.log": "build\n", "sub/local.log": "log\n",
		".worktreeinclude": ".env*\nsub/*.log\nsub/file.txt\n",
	} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(text), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(repo, ".env.local"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktrees := t.TempDir()
	s := worktreeServer(t, openStore(t), worktrees)
	c := connect(t, s)
	sess := worktreeSession(t, c, addWorkspace(t, c, filepath.Join(repo, "sub")))

	physical, err := filepath.EvalSymlinks(worktrees)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(sess.Worktree) != physical || sess.Directory != filepath.Join(sess.Worktree, "sub") {
		t.Fatalf("the session was listed in %q with worktree %q, not in %s", sess.Directory, sess.Worktree, physical)
	}
	if branches := oxBranches(t, repo); len(branches) != 1 || branches[0] != "ox/"+filepath.Base(sess.Worktree) {
		t.Fatalf("branches %v, not the worktree's", branches)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the post-checkout hook ran")
	}
	for name, want := range map[string]string{".env": "secret\n", ".env.local": "local\n", "sub/local.log": "log\n", "sub/file.txt": "committed\n"} {
		if got, err := os.ReadFile(filepath.Join(sess.Worktree, name)); err != nil || string(got) != want {
			t.Fatalf("worktree %s is %q, %v; want %q", name, got, err, want)
		}
	}
	if info, err := os.Stat(filepath.Join(sess.Worktree, ".env")); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf(".env mode %v, %v; want 0755", info, err)
	}
	if exists(filepath.Join(sess.Worktree, "build.log")) {
		t.Fatal("build.log is not listed in .worktreeinclude and was copied")
	}
	var folder struct {
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	}
	json.Unmarshal(c.call("workspace/list_folder", map[string]string{"session_id": sess.SessionID, "path": ""}), &folder)
	if len(folder.Entries) != 2 || folder.Entries[0].Name != "file.txt" || folder.Entries[1].Name != "local.log" {
		t.Fatalf("the worktree's folder listed %+v", folder.Entries)
	}

	if response := c.request("workspace/create", map[string]string{"path": sess.Directory}); response.Error == nil ||
		!strings.Contains(response.Error.Message, "inside a session's worktree") {
		t.Fatalf("workspace/create in the worktree returned %+v", response)
	}

	t.Log("other sessions see where the worktree session works")
	params := createParams(addWorkspace(t, c, filepath.Join(repo, "sub")), "")
	delete(params, "worktree")
	var plain struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(c.call("session/create", params), &plain)
	text, refused := attach(t, s, plain.SessionID).tool("list_sessions", map[string]any{})
	var others []listedWorktree
	if err := json.Unmarshal([]byte(text), &others); refused || err != nil || len(others) != 1 || others[0] != sess {
		t.Fatalf("list_sessions listed %q, not %+v", text, sess)
	}
	c.forget(plain.SessionID)
	if text, refused := attach(t, s, sess.SessionID).tool("send_message", map[string]string{"to": plain.SessionID, "text": "hello"}); refused {
		t.Fatalf("send_message returned %q", text)
	}
	if reply := c.update(plain.SessionID, "result").Result.Text; !strings.Contains(reply, " in "+sess.Directory+":\n") {
		t.Fatalf("the message reached the model as %q, without its sender's directory %s", reply, sess.Directory)
	}
}

// A creation that cannot make its worktree fails with the reason and leaves
// nothing behind.
func TestWorktreeCreationFails(t *testing.T) {
	plain := t.TempDir()
	empty := t.TempDir()
	repo := gitRepo(t)
	runGit(t, "-C", empty, "init", "-q")
	untracked := filepath.Join(repo, "untracked-only")
	if err := os.Mkdir(untracked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(untracked, "new.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	badInclude := gitRepo(t)
	if err := os.Mkdir(filepath.Join(badInclude, ".worktreeinclude"), 0o700); err != nil {
		t.Fatal(err)
	}
	unreadable := gitRepo(t)
	secret := filepath.Join(unreadable, ".env")
	if err := os.WriteFile(secret, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unreadable, ".worktreeinclude"), []byte(".env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// HEAD has data as a symlink out of the checkout; the main checkout has
	// it as a directory holding a listed file.
	outside := t.TempDir()
	linked := gitRepo(t)
	if err := os.Symlink(outside, filepath.Join(linked, "data")); err != nil {
		t.Fatal(err)
	}
	runGit(t, "-C", linked, "add", "data")
	runGit(t, "-C", linked, "commit", "-q", "-m", "link")
	if err := os.Remove(filepath.Join(linked, "data")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(linked, "data", "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"data/x/y.log": "y", ".worktreeinclude": "data/x/y.log\n"} {
		if err := os.WriteFile(filepath.Join(linked, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	failures := []struct {
		name, workspace, worktrees, want string
		repo                             string
	}{
		{"not a repository", plain, t.TempDir(), "not a git repository", ""},
		{"empty repository", empty, t.TempDir(), "create worktree: ", empty},
		{"untracked folder", untracked, t.TempDir(), "untracked-only is not in the worktree", repo},
		{"inside the repository", filepath.Join(repo, "sub"), filepath.Join(repo, "worktrees"), "is inside the repository", repo},
		{"unreadable include file", badInclude, t.TempDir(), "worktreeinclude", badInclude},
		{"unreadable listed file", unreadable, t.TempDir(), "copy .env", unreadable},
		{"copy through a symlink", linked, t.TempDir(), "outside the worktree", linked},
	}
	for _, failure := range failures {
		t.Run(failure.name, func(t *testing.T) {
			if failure.name == "unreadable listed file" && os.Geteuid() == 0 {
				t.Skip("root reads any file")
			}
			c := connect(t, worktreeServer(t, openStore(t), failure.worktrees))
			response := c.request("session/create", createParams(addWorkspace(t, c, failure.workspace), ""))
			if response.Error == nil || !strings.Contains(response.Error.Message, failure.want) {
				t.Fatalf("session/create returned %+v, not %q", response, failure.want)
			}
			if sessions := allListed(t, c); len(sessions) != 0 {
				t.Fatalf("listed %+v", sessions)
			}
			if failure.repo != "" {
				nothingLeft(t, failure.repo, failure.worktrees)
			}
			if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
				t.Fatalf("the directory a worktree symlink leads to holds %v, %v", entries, err)
			}
			if failure.name == "inside the repository" && exists(failure.worktrees) {
				t.Fatalf("the refused worktree directory %s was made", failure.worktrees)
			}
		})
	}
}

// Two creations with one ID race through their checkouts; one publishes, and
// the other discards its worktree and branch.
func TestWorktreeCreationsWithOneID(t *testing.T) {
	repo := gitRepo(t)
	if err := os.Mkdir(filepath.Join(repo, "other"), 0o700); err != nil {
		t.Fatal(err)
	}
	open, waiting := gitGate(t)
	worktrees := t.TempDir()
	c := connect(t, worktreeServer(t, openStore(t), worktrees))
	sub := addWorkspace(t, c, filepath.Join(repo, "sub"))

	t.Log("in one workspace")
	first := c.send("session/create", createParams(sub, "1AfUXwLfh7d"))
	second := c.send("session/create", createParams(sub, "1AfUXwLfh7d"))
	waiting(2)
	open()
	existing := 0
	for _, id := range []string{first, second} {
		response := c.response(id)
		var result struct {
			Existing bool `json:"existing"`
		}
		if response.Error != nil || json.Unmarshal(response.Result, &result) != nil {
			t.Fatalf("session/create returned %+v", response)
		}
		if result.Existing {
			existing++
		}
	}
	if existing != 1 {
		t.Fatalf("%d of the two creations answered existing", existing)
	}
	if sessions := allListed(t, c); len(sessions) != 1 {
		t.Fatalf("listed %+v", sessions)
	}
	if paths, branches := registered(t, repo), oxBranches(t, repo); len(paths) != 2 || len(branches) != 1 {
		t.Fatalf("git registers %v with branches %v", paths, branches)
	}
}

// A creation whose ID another workspace publishes during its checkout is
// refused and leaves nothing.
func TestWorktreeCreationsInTwoWorkspaces(t *testing.T) {
	repo := gitRepo(t)
	open, waiting := gitGate(t)
	c := connect(t, worktreeServer(t, openStore(t), t.TempDir()))
	sub := addWorkspace(t, c, filepath.Join(repo, "sub"))
	root := addWorkspace(t, c, repo)
	first := c.send("session/create", createParams(sub, "1AfUXwLfh7d"))
	second := c.send("session/create", createParams(root, "1AfUXwLfh7d"))
	waiting(2)
	open()
	refused := 0
	for _, id := range []string{first, second} {
		if response := c.response(id); response.Error != nil {
			if response.Error.Message != "session_id belongs to another workspace" {
				t.Fatalf("session/create returned %+v", response)
			}
			refused++
		}
	}
	if refused != 1 {
		t.Fatalf("%d of the two creations were refused", refused)
	}
	if paths, branches := registered(t, repo), oxBranches(t, repo); len(paths) != 2 || len(branches) != 1 {
		t.Fatalf("git registers %v with branches %v", paths, branches)
	}
}

// A workspace removed during a checkout fails the creation, which leaves
// nothing.
func TestWorktreeWorkspaceRemovedDuringCheckout(t *testing.T) {
	repo := gitRepo(t)
	open, waiting := gitGate(t)
	worktrees := t.TempDir()
	c := connect(t, worktreeServer(t, openStore(t), worktrees))
	sub := addWorkspace(t, c, filepath.Join(repo, "sub"))
	creating := c.send("session/create", createParams(sub, ""))
	waiting(1)
	c.call("workspace/remove", map[string]string{"workspace_id": sub})
	open()
	if response := c.response(creating); response.Error == nil || response.Error.Message != "workspace not found" {
		t.Fatalf("session/create returned %+v", response)
	}
	nothingLeft(t, repo, worktrees)
}

// Stopping oxd during a checkout waits for it and discards it.
func TestWorktreeCloseDuringCheckout(t *testing.T) {
	repo := gitRepo(t)
	open, waiting := gitGate(t)
	worktrees := t.TempDir()
	s := worktreeServer(t, openStore(t), worktrees)
	c := connect(t, s)
	creating := c.send("session/create", createParams(addWorkspace(t, c, filepath.Join(repo, "sub")), ""))
	waiting(1)
	closed := make(chan struct{})
	go func() {
		s.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned during a checkout")
	case <-time.After(200 * time.Millisecond):
	}
	open()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return once the checkout ended")
	}
	if response := c.response(creating); response.Error == nil || response.Error.Message != "daemon is shutting down" {
		t.Fatalf("session/create returned %+v", response)
	}
	nothingLeft(t, repo, worktrees)
}

// Delete refuses a worktree with untracked files or on no branch, and
// otherwise removes it, with its ignored files, and keeps its branch.
func TestWorktreeDelete(t *testing.T) {
	repo := gitRepo(t)
	runGit(t, "-C", repo, "config", "status.showUntrackedFiles", "no")
	c := connect(t, worktreeServer(t, openStore(t), t.TempDir()))
	sess := worktreeSession(t, c, addWorkspace(t, c, filepath.Join(repo, "sub")))
	destroy := func() *string {
		t.Helper()
		if response := c.request("session/destroy", map[string]string{"session_id": sess.SessionID}); response.Error != nil {
			return &response.Error.Message
		}
		return nil
	}

	untracked := filepath.Join(sess.Directory, "untracked.txt")
	if err := os.WriteFile(untracked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := destroy(); err == nil || *err != "worktree has uncommitted changes" {
		t.Fatalf("deleting a worktree with an untracked file returned %v", err)
	}
	listed(t, c, sess.SessionID)
	if _, err := os.Stat(untracked); err != nil {
		t.Fatal(err)
	}
	os.Remove(untracked)

	branch := "ox/" + filepath.Base(sess.Worktree)
	runGit(t, "-C", sess.Worktree, "checkout", "-q", "--detach")
	if err := destroy(); err == nil || *err != "worktree HEAD is not on a branch" {
		t.Fatalf("deleting a detached worktree returned %v", err)
	}
	runGit(t, "-C", sess.Worktree, "checkout", "-q", branch)

	if err := os.WriteFile(filepath.Join(sess.Directory, "work.txt"), []byte("work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, "-C", sess.Worktree, "add", ".")
	runGit(t, "-C", sess.Worktree, "commit", "-q", "-m", "work")
	commit := runGit(t, "-C", sess.Worktree, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(sess.Directory, "build.log"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := destroy(); err != nil {
		t.Fatalf("deleting a clean worktree returned %s", *err)
	}
	if _, err := os.Stat(sess.Worktree); !os.IsNotExist(err) {
		t.Fatalf("the worktree folder remains: %v", err)
	}
	if paths := registered(t, repo); len(paths) != 1 {
		t.Fatalf("git registers %v", paths)
	}
	if got := runGit(t, "-C", repo, "rev-parse", branch); got != commit {
		t.Fatalf("the branch is at %s, not the worktree's commit %s", got, commit)
	}
}

// Delete clears the registration of a worktree whose folder, or whose whole
// worktree directory, was removed by hand, and a session whose folder is gone
// refuses to start.
func TestWorktreeDeleteAfterRemovedByHand(t *testing.T) {
	repo := gitRepo(t)
	worktrees := t.TempDir()
	c := connect(t, worktreeServer(t, openStore(t), worktrees))
	sub := addWorkspace(t, c, filepath.Join(repo, "sub"))

	sess := worktreeSession(t, c, sub)
	if err := os.RemoveAll(sess.Worktree); err != nil {
		t.Fatal(err)
	}
	if response := c.request("session/send", map[string]string{"session_id": sess.SessionID, "text": "hi"}); response.Error == nil ||
		!strings.HasSuffix(response.Error.Message, "no longer exists") {
		t.Fatalf("sending to a session whose folder is gone returned %+v", response)
	}
	c.call("session/destroy", map[string]string{"session_id": sess.SessionID})
	if paths := registered(t, repo); len(paths) != 1 {
		t.Fatalf("git registers %v", paths)
	}

	sess = worktreeSession(t, c, sub)
	if err := os.RemoveAll(worktrees); err != nil {
		t.Fatal(err)
	}
	c.call("session/destroy", map[string]string{"session_id": sess.SessionID})
	if paths := registered(t, repo); len(paths) != 1 {
		t.Fatalf("git registers %v", paths)
	}
}

// Delete removes the worktree of a session whose workspace folder is gone,
// and keeps its branch.
func TestWorktreeDeleteAfterWorkspaceRemoved(t *testing.T) {
	repo := gitRepo(t)
	c := connect(t, worktreeServer(t, openStore(t), t.TempDir()))
	sess := worktreeSession(t, c, addWorkspace(t, c, filepath.Join(repo, "sub")))
	if err := os.RemoveAll(filepath.Join(repo, "sub")); err != nil {
		t.Fatal(err)
	}
	c.call("session/destroy", map[string]string{"session_id": sess.SessionID})
	if _, err := os.Stat(sess.Worktree); !os.IsNotExist(err) {
		t.Fatalf("the worktree folder remains: %v", err)
	}
	if paths := registered(t, repo); len(paths) != 1 {
		t.Fatalf("git registers %v", paths)
	}
	if branches := oxBranches(t, repo); len(branches) != 1 || branches[0] != "ox/"+filepath.Base(sess.Worktree) {
		t.Fatalf("branches %v, not the worktree's", branches)
	}
}

// A delete whose row delete fails after the removal leaves the session, and
// a repeated delete finishes it.
func TestWorktreeDeleteRepeated(t *testing.T) {
	repo := gitRepo(t)
	path := filepath.Join(t.TempDir(), "ox.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c := connect(t, worktreeServer(t, st, t.TempDir()))
	sess := worktreeSession(t, c, addWorkspace(t, c, filepath.Join(repo, "sub")))
	logged := &lockedBuffer{}
	log.SetOutput(logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER refuse BEFORE DELETE ON sessions BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	if response := c.request("session/destroy", map[string]string{"session_id": sess.SessionID}); response.Error == nil {
		t.Fatal("a delete whose row delete failed succeeded")
	}
	listed(t, c, sess.SessionID)
	if got := logged.String(); !strings.Contains(got, "session "+sess.SessionID+": delete:") || !strings.Contains(got, "refused") {
		t.Errorf("a refused row delete logged %q", got)
	}
	if _, err := db.Exec(`DROP TRIGGER refuse`); err != nil {
		t.Fatal(err)
	}
	c.call("session/destroy", map[string]string{"session_id": sess.SessionID})
	if sessions := allListed(t, c); len(sessions) != 0 {
		t.Fatalf("listed %+v", sessions)
	}
	if paths := registered(t, repo); len(paths) != 1 {
		t.Fatalf("git registers %v", paths)
	}
}

// A session keeps its worktree through a restart with another worktree
// directory, and deleting it then removes the worktree.
func TestWorktreeAfterRestart(t *testing.T) {
	repo := gitRepo(t)
	st := openStore(t)
	open, waiting := gitGate(t)
	s := worktreeServer(t, st, t.TempDir())
	c := connect(t, s)
	workspace := addWorkspace(t, c, filepath.Join(repo, "sub"))
	// A local session created during the worktree session's checkout joins the
	// list first.
	creating := c.send("session/create", createParams(workspace, ""))
	waiting(1)
	local := createParams(workspace, "")
	local["worktree"] = false
	c.call("session/create", local)
	open()
	var created struct {
		SessionID string `json:"session_id"`
	}
	if response := c.response(creating); response.Error != nil || json.Unmarshal(response.Result, &created) != nil {
		t.Fatalf("session/create returned %+v", response)
	}
	sess := listed(t, c, created.SessionID)
	if sess.Worktree == "" {
		t.Fatalf("session %s was created without a worktree", sess.SessionID)
	}
	before := allListed(t, c)
	if len(before) != 2 || before[1] != sess {
		t.Fatalf("listed %+v, with the worktree session %+v not last", before, sess)
	}
	s.Close()

	c = connect(t, worktreeServer(t, st, t.TempDir()))
	if after := allListed(t, c); !slices.Equal(after, before) {
		t.Fatalf("after a restart the sessions were listed %+v, not %+v", after, before)
	}
	c.call("session/destroy", map[string]string{"session_id": sess.SessionID})
	if _, err := os.Stat(sess.Worktree); !os.IsNotExist(err) {
		t.Fatalf("the worktree folder remains: %v", err)
	}
}

// A session created without a worktree has none, and deletes without git.
func TestSessionWithoutWorktree(t *testing.T) {
	c := connect(t, worktreeServer(t, openStore(t), t.TempDir()))
	params := createParams(addWorkspace(t, c, t.TempDir()), "")
	delete(params, "worktree")
	var created struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(c.call("session/create", params), &created)
	var announced map[string]any
	json.Unmarshal(c.next(func(m message) bool { return m.Method == "session/created" }).Params, &announced)
	if _, ok := announced["worktree"]; ok {
		t.Fatalf("session/created carried a worktree: %v", announced)
	}
	if sess := listed(t, c, created.SessionID); sess.Worktree != "" {
		t.Fatalf("listed %+v", sess)
	}
	c.call("session/destroy", map[string]string{"session_id": created.SessionID})
}
