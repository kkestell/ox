package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/config"
	"github.com/kkestell/ox/internal/fake"
	"github.com/kkestell/ox/internal/store"
)

// TestLifecycle runs every creation path against every creation outcome, and
// every session role against every destructive action in every state, as
// tables. A model of the sessions, built from oxd's notifications, predicts
// whether oxd accepts or refuses each request and which reports it delivers,
// and the invariants are checked after every step. A new creation path or
// session role adds a row, and runs against every column.
func TestLifecycle(t *testing.T) {
	// A parallel subtest cannot set the environment, so the parent sets it
	// for the repositories the subtests make.
	gitEnv(t)
	t.Run("create", func(t *testing.T) {
		for _, path := range creationPaths {
			for _, outcome := range []creationOutcome{succeeds, launchFails, firstMessageRefused} {
				if !path.tool && outcome != succeeds {
					continue // session/create launches nothing
				}
				t.Run(path.name+"/"+outcome.String(), func(t *testing.T) {
					t.Parallel()
					newLifecycle(t).create(path, outcome)
				})
			}
		}
	})
	t.Run("act", func(t *testing.T) {
		for _, role := range lifecycleRoles {
			for _, action := range lifecycleActions {
				for _, state := range []roleState{holding, running, idle} {
					if action.dirty && !role.worktree || action.method == "session/unarchive" && state == running {
						continue // a session without a worktree is never dirty, and an archived one runs nothing
					}
					t.Run(role.name+"/"+action.name+"/"+state.String(), func(t *testing.T) {
						t.Parallel()
						newLifecycle(t).act(role, action, state)
					})
				}
			}
		}
	})
}

// creationPath is a way a session is created: by the user with session/create,
// or by another session with an MCP tool.
type creationPath struct {
	name     string
	tool     bool
	worktree bool
	// create makes the session as caller, and returns the tool's text and
	// whether it is a refusal, or session/create's response.
	create func(l *lifecycle, caller *lifecycleSession) (string, bool)
}

var creationPaths = []creationPath{
	{name: "session/create", create: func(l *lifecycle, _ *lifecycleSession) (string, bool) {
		return l.createTop(false)
	}},
	{name: "session/create with worktree", worktree: true, create: func(l *lifecycle, _ *lifecycleSession) (string, bool) {
		return l.createTop(true)
	}},
	{name: "create_session", tool: true, create: func(l *lifecycle, caller *lifecycleSession) (string, bool) {
		return l.createChild(caller, "kid")
	}},
	{name: "create_top_level_session", tool: true, create: func(l *lifecycle, caller *lifecycleSession) (string, bool) {
		return l.createStarted(caller, false)
	}},
	{name: "create_top_level_session with worktree", tool: true, worktree: true, create: func(l *lifecycle, caller *lifecycleSession) (string, bool) {
		return l.createStarted(caller, true)
	}},
}

type creationOutcome int

const (
	succeeds creationOutcome = iota
	launchFails
	firstMessageRefused
)

func (o creationOutcome) String() string {
	return [...]string{"succeeds", "launch fails", "first message refused"}[o]
}

// create creates a session on path, from a session the user created, with
// the harness armed for outcome. A success is listed, stored, and broadcast,
// with its creator's message first, and its worktree and branch. A failure
// is refused and leaves no row, broadcast, worktree, or branch.
func (l *lifecycle) create(path creationPath, outcome creationOutcome) {
	t := l.t
	t.Helper()
	l.createTop(false)
	caller := l.sorted()[0]
	l.check(l.settle(), l.git())
	before := l.snapshot()
	if outcome != succeeds {
		l.arm(outcome == launchFails)
	}
	l.created = nil
	text, refused := path.create(l, caller)
	l.armed.Store(nil)
	listed := l.settle()
	g := l.git()
	l.check(listed, g)
	if outcome != succeeds {
		if !refused || len(l.created) > 0 {
			t.Fatalf("a creation that %s returned %q, %v, and published %v", outcome, text, refused, l.created)
		}
		before.unchanged(t, "a failed creation", l.snapshot())
		return
	}
	var result struct {
		SessionID string `json:"session_id"`
	}
	if refused || json.Unmarshal([]byte(text), &result) != nil || !slices.Equal(l.created, []string{result.SessionID}) {
		t.Fatalf("the creation returned %q, %v, and published %v", text, refused, l.created)
	}
	id := result.SessionID
	if _, ok := listed[id]; !ok {
		t.Fatalf("session %s is not listed", id)
	}
	if _, ok := l.stored()[id]; !ok {
		t.Fatalf("session %s is not stored", id)
	}
	worktree := listed[id].Worktree
	if (worktree != "") != path.worktree {
		t.Fatalf("session %s has worktree %q", id, worktree)
	}
	if path.worktree && !slices.Contains(g.branches, worktreeBranch(worktree)) {
		t.Fatalf("git has branches %v, without the worktree's", g.branches)
	}
	if path.tool {
		var got struct {
			History []agent.Update `json:"history"`
		}
		json.Unmarshal(l.request("session/get", map[string]string{"session_id": id}).Result, &got)
		i := slices.IndexFunc(got.History, func(u agent.Update) bool { return u.Type == agent.UpdatePrompt })
		if i < 0 || got.History[i].From != caller.id {
			t.Fatalf("session %s's first prompt is not from %s: %+v", id, caller.id, got.History)
		}
	}
	l.finish()
}

// lifecycleRole is one of the sessions in the family every act case builds:
// a session the user created in a worktree, which reports to no one, and a
// child, a started session, and a started session in a worktree, which
// report to it.
type lifecycleRole struct {
	name     string
	worktree bool
	pick     func(f family) *lifecycleSession
}

type family struct{ recipient, child, started, startedWorktree *lifecycleSession }

var lifecycleRoles = []lifecycleRole{
	{name: "child", pick: func(f family) *lifecycleSession { return f.child }},
	{name: "started", pick: func(f family) *lifecycleSession { return f.started }},
	{name: "started with worktree", worktree: true, pick: func(f family) *lifecycleSession { return f.startedWorktree }},
	{name: "recipient", worktree: true, pick: func(f family) *lifecycleSession { return f.recipient }},
}

type lifecycleAction struct {
	name   string
	method string
	dirty  bool // the target's worktree holds an untracked file
}

var lifecycleActions = []lifecycleAction{
	{name: "delete clean", method: "session/destroy"},
	{name: "delete dirty", method: "session/destroy", dirty: true},
	{name: "archive", method: "session/archive"},
	{name: "unarchive", method: "session/unarchive"},
}

type roleState int

const (
	holding roleState = iota // a report waits because its recipient cannot take it
	running                  // the target runs a turn
	idle
)

func (s roleState) String() string {
	return [...]string{"report held", "turn running", "idle"}[s]
}

// act builds the family, puts the role's session in state, and has oxd take
// action on it. The model predicts whether oxd refuses it; a refusal changes
// nothing. Then every running turn is interrupted, and every report must
// have been delivered once, unless its sender or recipient was deleted.
//
// In holding, a reporter's report waits for its recipient's running turn,
// and the recipient waits for the started session's report the same way, or,
// when it is unarchived, because it is archived.
func (l *lifecycle) act(role lifecycleRole, action lifecycleAction, state roleState) {
	t := l.t
	t.Helper()
	f := l.family()
	target := role.pick(f)
	unarchive := action.method == "session/unarchive"
	switch {
	case state == running:
		l.send(target, "wait 60000")
		target.long = true
	case state == holding && target == f.recipient && unarchive:
		l.predicted("session/archive", target)
		l.send(f.started, "held")
	case state == holding && target == f.recipient:
		l.send(target, "wait 60000")
		target.long = true
		l.send(f.started, "held")
	case state == holding:
		l.send(f.recipient, "wait 60000")
		f.recipient.long = true
		l.send(target, "held")
	}
	l.check(l.settle(), l.git())
	if state == holding && l.pendingReports() == 0 {
		t.Fatalf("no report is held: %s", l.describe())
	}
	if unarchive && !target.archived {
		l.predicted("session/archive", target)
		l.check(l.settle(), l.git())
	}
	if action.dirty {
		if err := os.WriteFile(filepath.Join(target.directory, "untracked.txt"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		target.dirty = true
	}
	l.predicted(action.method, target)
	l.check(l.settle(), l.git())
	for _, sess := range l.sorted() {
		if sess.long {
			if response := l.request("session/interrupt", map[string]string{"session_id": sess.id}); response.Error != nil {
				t.Fatalf("session/interrupt: %+v", response.Error)
			}
		}
	}
	l.check(l.settle(), l.git())
	if n := l.pendingReports(); n > 0 && !slices.ContainsFunc(l.sorted(), func(sess *lifecycleSession) bool { return sess.archived }) {
		t.Fatalf("%d reports were never delivered: %s", n, l.describe())
	}
	l.finish()
}

// family builds the act cases' sessions: a session the user created in a
// worktree, its child, and a session it started with and without a worktree,
// each settled with its first report delivered.
func (l *lifecycle) family() family {
	t := l.t
	t.Helper()
	l.createTop(true)
	recipient := l.sorted()[0]
	l.settle()
	if text, refused := l.createChild(recipient, "kid"); refused {
		t.Fatalf("create_session returned %q", text)
	}
	l.settle()
	if text, refused := l.createStarted(recipient, false); refused {
		t.Fatalf("create_top_level_session returned %q", text)
	}
	l.settle()
	if text, refused := l.createStarted(recipient, true); refused {
		t.Fatalf("create_top_level_session returned %q", text)
	}
	l.check(l.settle(), l.git())
	sessions := l.sorted()
	return family{sessions[0], sessions[1], sessions[2], sessions[3]}
}

// armedFailure is the creation failure a case has armed: the start of a
// session whose ID known does not hold fails, or its agent refuses its first
// Send.
type armedFailure struct {
	known  map[string]bool
	launch bool
}

// lifecycleSession is what the model knows of a live session.
type lifecycleSession struct {
	id        string
	order     int // how many sessions the model saw created before it
	directory string
	worktree  string
	parent    string // a child's parent, or ""
	recipient string // the session it reports to, or ""
	archived  bool
	state     string
	open      int  // inputs accepted whose turn has not ended
	long      bool // a long turn the case left running
	dirty     bool // an untracked file in its worktree
	deleting  bool // its delete request is in flight
}

type lifecycle struct {
	t         *testing.T
	repo      string
	worktrees string
	st        *store.Store
	armed     atomic.Pointer[armedFailure]

	s         *Server
	c         *client
	workspace string

	sessions map[string]*lifecycleSession
	pending  map[string]int // reports made and not yet delivered, by sender
	creator  string         // the caller of the create_top_level_session in flight
	created  []string       // the sessions published since the case last cleared it
	branches []string       // the branches of every worktree session published
	order    int            // how many sessions the model has seen created
}

var lifecycleConfig = &config.Config{
	Harnesses:     map[string]config.Harness{"fake": {Models: []config.Model{{ID: "m"}}}},
	Titling:       config.Titling{Harness: "fake", Model: "m"},
	MaxChildDepth: 2,
}

func newLifecycle(t *testing.T) *lifecycle {
	t.Helper()
	l := &lifecycle{
		t: t, repo: newGitRepo(t), worktrees: t.TempDir(), st: openStore(t),
		sessions: map[string]*lifecycleSession{}, pending: map[string]int{},
	}
	start := func(options agent.Options) (agent.Agent, error) {
		f := l.armed.Load()
		if f == nil || f.known[options.SessionID] {
			return fake.Start(options)
		}
		if f.launch {
			return nil, errors.New("harness cannot be launched")
		}
		a, _ := fake.Start(options)
		refused := false
		return sendGateAgent{a, func(agent.Input) error {
			if refused {
				return nil
			}
			refused = true
			return errors.New("first message refused")
		}}, nil
	}
	s, err := New(l.st, lifecycleConfig, 0, 0, map[string]agent.Harness{"fake": {Start: start, Ask: fake.Ask}}, MCP{}, l.worktrees)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	l.s, l.c = s, connect(t, s)
	l.workspace = addWorkspace(t, l.c, l.repo)
	return l
}

// arm has the harness fail to launch, or refuse the first message of, every
// session the model does not know yet.
func (l *lifecycle) arm(launch bool) {
	known := map[string]bool{}
	for id := range l.sessions {
		known[id] = true
	}
	l.armed.Store(&armedFailure{known: known, launch: launch})
}

// finish closes the server and applies every notification it sent, so a
// late report delivery fails the case.
func (l *lifecycle) finish() {
	l.t.Helper()
	l.s.Close()
	l.list()
}

// apply applies a message to the model, failing on a report delivery the
// model does not expect.
func (l *lifecycle) apply(m message) {
	t := l.t
	t.Helper()
	switch m.Method {
	case "session/created":
		var params struct {
			sessionInfo
		}
		json.Unmarshal(m.Params, &params)
		sess := &lifecycleSession{id: params.SessionID, order: l.order, directory: params.Directory, worktree: params.Worktree,
			archived: params.Archived, state: params.State}
		if params.Child != nil {
			sess.parent, sess.recipient = params.Child.ParentID, params.Child.ParentID
		} else {
			sess.recipient = l.creator
		}
		if params.State == agent.StateRunning {
			sess.open = 1
		}
		if sess.worktree != "" {
			l.branches = append(l.branches, worktreeBranch(sess.worktree))
		}
		l.order++
		l.sessions[sess.id] = sess
		l.created = append(l.created, sess.id)
	case "session/archived", "session/unarchived":
		if sess := l.sessions[m.sessionID()]; sess != nil {
			sess.archived = m.Method == "session/archived"
		}
	case "session/destroyed":
		id := m.sessionID()
		delete(l.sessions, id)
		delete(l.pending, id)
		for sender := range l.pending {
			if sess := l.sessions[sender]; sess == nil || sess.recipient == id {
				delete(l.pending, sender)
			}
		}
	case "session/update":
		sess := l.sessions[m.sessionID()]
		if sess == nil {
			t.Fatalf("an update for session %s, which is not listed: %s", m.sessionID(), m.Params)
		}
		var u agent.Update
		json.Unmarshal(m.Params, &u)
		switch u.Type {
		case agent.UpdateState:
			sess.state = u.State
		case agent.UpdateParked:
			sess.state = agent.StateParked
		case agent.UpdatePromptQueued:
			sess.open++
			for _, r := range u.Reports {
				if l.pending[r.From] == 0 {
					t.Fatalf("session %s took a report from %s that was not pending: %+v", sess.id, r.From, u.Reports)
				}
				if sender := l.sessions[r.From]; sender == nil || sender.recipient != sess.id {
					t.Fatalf("session %s took a report from %s, which does not report to it", sess.id, r.From)
				}
				l.pending[r.From]--
			}
		case agent.UpdatePromptCancelled:
			sess.open--
		case agent.UpdateResult, agent.UpdateExited:
			sess.open--
			sess.long = false
			if l.sessions[sess.recipient] != nil && (u.Type != agent.UpdateExited || !sess.deleting) {
				l.pending[sess.id]++
			}
		}
	}
}

// next reads the client's next message within its timeout.
func (l *lifecycle) next(waiting string) message {
	l.t.Helper()
	select {
	case m, ok := <-l.c.messages:
		if !ok {
			l.t.Fatal("connection closed")
		}
		return m
	case <-time.After(l.c.timeout):
		l.t.Fatalf("waited %v for %s", l.c.timeout, waiting)
		return message{}
	}
}

// request sends a request and applies every notification until its response.
func (l *lifecycle) request(method string, params any) message {
	l.t.Helper()
	id := l.c.send(method, params)
	for {
		m := l.next("the response to " + method)
		if string(m.ID) == id {
			return m
		}
		l.apply(m)
	}
}

// tool calls an MCP tool as caller, applying notifications meanwhile, and
// returns its text and whether it is a refusal.
func (l *lifecycle) tool(caller, name string, arguments any) (string, bool) {
	l.t.Helper()
	type answer struct {
		text    string
		refused bool
		err     error
	}
	done := make(chan answer, 1)
	go func() {
		text, refused, err := callTool(l.s, caller, name, arguments)
		done <- answer{text, refused, err}
	}()
	timeout := time.After(l.c.timeout)
	for {
		select {
		case a := <-done:
			if a.err != nil {
				l.t.Fatalf("%s: %v", name, a.err)
			}
			return a.text, a.refused
		case m, ok := <-l.c.messages:
			if !ok {
				l.t.Fatal("connection closed")
			}
			l.apply(m)
		case <-timeout:
			l.t.Fatalf("waited %v for %s", l.c.timeout, name)
		}
	}
}

// callTool attaches as sessionID's MCP forwarder and calls a tool. It reads
// each reply by its ID, past the broadcasts the connection receives until
// mcp/attach takes it off the broadcast list.
func callTool(s *Server, sessionID, name string, arguments any) (string, bool, error) {
	serverSide, clientSide := net.Pipe()
	go s.serveConn(serverSide)
	defer clientSide.Close()
	encoder, decoder := json.NewEncoder(clientSide), json.NewDecoder(clientSide)
	type reply struct {
		ID     int `json:"id"`
		Error  any `json:"error"`
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	var result reply
	for id, request := range []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "mcp/attach", "params": map[string]string{"session_id": sessionID}},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": name, "arguments": arguments}},
	} {
		if err := encoder.Encode(request); err != nil {
			return "", false, err
		}
		for result.ID != id+1 {
			result = reply{}
			if err := decoder.Decode(&result); err != nil {
				return "", false, err
			}
		}
		if result.Error != nil {
			return "", false, fmt.Errorf("request %d failed: %v", id+1, result.Error)
		}
	}
	if len(result.Result.Content) != 1 {
		return "", false, fmt.Errorf("returned %+v", result.Result)
	}
	return result.Result.Content[0].Text, result.Result.IsError, nil
}

// createTop creates a session as the user, and returns session/create's
// result and whether it failed.
func (l *lifecycle) createTop(worktree bool) (string, bool) {
	l.t.Helper()
	params := createParams(l.workspace, "")
	params["worktree"] = worktree
	response := l.request("session/create", params)
	if response.Error != nil {
		return response.Error.Message, true
	}
	return string(response.Result), false
}

func (l *lifecycle) createChild(caller *lifecycleSession, name string) (string, bool) {
	l.t.Helper()
	return l.tool(caller.id, "create_session", map[string]string{"name": name, "title": "Child", "message": "first"})
}

// createStarted applies the notifications oxd sent before the tool's answer
// while the caller is the creator the model gives a new top-level session.
func (l *lifecycle) createStarted(caller *lifecycleSession, worktree bool) (string, bool) {
	l.t.Helper()
	l.creator = caller.id
	text, refused := l.tool(caller.id, "create_top_level_session", map[string]any{"title": "Started", "message": "first", "worktree": worktree})
	l.list()
	l.creator = ""
	return text, refused
}

func (l *lifecycle) send(target *lifecycleSession, text string) {
	l.t.Helper()
	if response := l.request("session/send", map[string]string{"session_id": target.id, "text": text}); response.Error != nil {
		l.t.Fatalf("session/send: %+v", response.Error)
	}
}

// refusal is why oxd refuses method for target, or "" when it accepts it.
func (l *lifecycle) refusal(method string, target *lifecycleSession) string {
	switch method {
	case "session/destroy":
		if slices.ContainsFunc(l.sorted(), func(other *lifecycleSession) bool { return other.parent == target.id }) {
			return "session has child sessions"
		}
		if target.dirty {
			return "worktree has uncommitted changes"
		}
	case "session/archive":
		if l.busy(target) {
			return "session is busy"
		}
	case "session/unarchive":
		if parent := l.sessions[target.parent]; parent != nil && parent.archived {
			return "parent session is archived"
		}
	}
	return ""
}

// busy is whether sess or a session beneath it runs a long turn.
func (l *lifecycle) busy(sess *lifecycleSession) bool {
	return sess.long || slices.ContainsFunc(l.sorted(), func(other *lifecycleSession) bool { return other.parent == sess.id && l.busy(other) })
}

// predicted sends a request the model predicts oxd accepts or refuses, and
// checks the answer, and that a refusal changed nothing.
func (l *lifecycle) predicted(method string, target *lifecycleSession) {
	t := l.t
	t.Helper()
	before := l.snapshot()
	want := l.refusal(method, target)
	target.deleting = method == "session/destroy"
	response := l.request(method, map[string]string{"session_id": target.id})
	target.deleting = false
	got := ""
	if response.Error != nil {
		got = response.Error.Message
	}
	if got != want {
		t.Fatalf("%s of %s returned %q, not %q: %s", method, target.id, got, want, l.describe())
	}
	if got != "" {
		before.unchanged(t, "a refused "+method, l.snapshot())
	}
}

// lifecycleSnapshot is what a refused request or a failed creation leaves as
// it was: the list, the store, git, the worktree directory, and the reports
// waiting.
type lifecycleSnapshot struct {
	listed    map[string]bool // archived, by session ID
	stored    map[string]bool
	git       registrations
	worktrees int
	pending   map[string]int
}

func (l *lifecycle) snapshot() lifecycleSnapshot {
	l.t.Helper()
	listed := map[string]bool{}
	for id, sess := range l.list() {
		listed[id] = sess.Archived
	}
	entries, err := os.ReadDir(l.worktrees)
	if err != nil && !os.IsNotExist(err) {
		l.t.Fatal(err)
	}
	return lifecycleSnapshot{listed, l.stored(), l.git(), len(entries), maps.Clone(l.pending)}
}

func (before lifecycleSnapshot) unchanged(t *testing.T, what string, after lifecycleSnapshot) {
	t.Helper()
	switch {
	case !maps.Equal(before.listed, after.listed):
		t.Fatalf("%s changed the list from %v to %v", what, before.listed, after.listed)
	case !maps.Equal(before.stored, after.stored):
		t.Fatalf("%s changed the store from %v to %v", what, before.stored, after.stored)
	case !slices.Equal(before.git.worktrees, after.git.worktrees) || !slices.Equal(before.git.branches, after.git.branches):
		t.Fatalf("%s changed git from %+v to %+v", what, before.git, after.git)
	case before.worktrees != after.worktrees:
		t.Fatalf("%s left %d entries in the worktree directory, not %d", what, after.worktrees, before.worktrees)
	case !maps.Equal(before.pending, after.pending):
		t.Fatalf("%s changed the reports waiting from %v to %v", what, before.pending, after.pending)
	}
}

// stored is the store's sessions and whether each is archived.
func (l *lifecycle) stored() map[string]bool {
	l.t.Helper()
	workspaces, err := l.st.Workspaces()
	if err != nil {
		l.t.Fatal(err)
	}
	stored := map[string]bool{}
	for _, w := range workspaces {
		for _, sess := range w.Sessions {
			stored[sess.ID] = sess.Archived
		}
	}
	return stored
}

func (l *lifecycle) pendingReports() int {
	n := 0
	for _, reports := range l.pending {
		n += reports
	}
	return n
}

// settledLocally is whether every live session has no open input but the
// long turn the case left running, and every report waiting for a recipient
// that can take it has been delivered.
func (l *lifecycle) settledLocally() bool {
	for _, sess := range l.sessions {
		open := sess.open
		if sess.long {
			open--
		}
		if open < 0 {
			l.t.Fatalf("session %s ended more turns than it began", sess.id)
		}
		if open > 0 {
			return false
		}
	}
	for sender, n := range l.pending {
		recipient := l.sessions[l.sessions[sender].recipient]
		if n > 0 && recipient != nil && !recipient.archived &&
			slices.Contains([]string{agent.StateIdle, agent.StateBackground, agent.StateParked}, recipient.state) {
			return false
		}
	}
	return true
}

// listing is workspace/list's sessions, by ID.
type listing map[string]sessionInfo

// settle applies notifications until the model is settled, confirming it
// with a listing that follows every notification already sent, and returns
// that listing.
func (l *lifecycle) settle() listing {
	l.t.Helper()
	deadline := time.Now().Add(l.c.timeout)
	for {
		for !l.settledLocally() {
			if time.Now().After(deadline) {
				l.t.Fatalf("not settled within %v: %s", l.c.timeout, l.describe())
			}
			l.apply(l.next("the sessions to settle: " + l.describe()))
		}
		listed := l.list()
		if l.settledLocally() {
			return listed
		}
	}
}

func (l *lifecycle) list() listing {
	l.t.Helper()
	var listed struct {
		Workspaces []struct {
			Sessions []sessionInfo `json:"sessions"`
		} `json:"workspaces"`
	}
	json.Unmarshal(l.request("workspace/list", map[string]any{}).Result, &listed)
	sessions := listing{}
	for _, w := range listed.Workspaces {
		for _, sess := range w.Sessions {
			sessions[sess.SessionID] = sess
		}
	}
	return sessions
}

// describe is the model's sessions and pending reports, for a failure.
func (l *lifecycle) describe() string {
	var s string
	for _, sess := range l.sorted() {
		s += fmt.Sprintf("\n  %s state=%s open=%d long=%v archived=%v recipient=%s pending=%d",
			sess.id, sess.state, sess.open, sess.long, sess.archived, sess.recipient, l.pending[sess.id])
	}
	return s
}

// registrations is the worktrees git registers, apart from the repository's
// own checkout, and the ox/ branches, both sorted.
type registrations struct{ worktrees, branches []string }

func (l *lifecycle) git() registrations {
	l.t.Helper()
	var worktrees []string
	for _, path := range registered(l.t, l.repo)[1:] {
		worktrees = append(worktrees, resolved(path))
	}
	slices.Sort(worktrees)
	return registrations{worktrees, oxBranches(l.t, l.repo)}
}

// resolved is path with its symlinks resolved, or path when it is gone.
func resolved(path string) string {
	if physical, err := filepath.EvalSymlinks(path); err == nil {
		return physical
	}
	return path
}

// check checks the invariants every settled step keeps: the list holds the
// model's sessions, a child of an archived session is archived, an archived
// session is parked, git registers the sessions' worktrees, and every worktree
// session published keeps its branch.
func (l *lifecycle) check(listed listing, g registrations) {
	t := l.t
	t.Helper()
	if !slices.Equal(slices.Sorted(maps.Keys(listed)), slices.Sorted(maps.Keys(l.sessions))) {
		t.Fatalf("workspace/list has %v, the model %s", slices.Sorted(maps.Keys(listed)), l.describe())
	}
	var worktrees []string
	for id, sess := range l.sessions {
		got := listed[id]
		if got.Archived != sess.archived || got.Worktree != sess.worktree {
			t.Fatalf("workspace/list has %+v, the model %+v", got, *sess)
		}
		if parent := l.sessions[sess.parent]; parent != nil && parent.archived && !sess.archived {
			t.Fatalf("session %s is not archived, and its parent %s is", id, parent.id)
		}
		if sess.archived && sess.state != agent.StateParked {
			t.Fatalf("archived session %s is %s, not parked", id, sess.state)
		}
		if sess.worktree != "" {
			worktrees = append(worktrees, resolved(sess.worktree))
		}
	}
	slices.Sort(worktrees)
	if !slices.Equal(g.worktrees, worktrees) {
		t.Fatalf("git registers %v, the sessions have %v", g.worktrees, worktrees)
	}
	if branches := slices.Sorted(slices.Values(l.branches)); !slices.Equal(g.branches, branches) {
		t.Fatalf("git has branches %v, the worktree sessions made %v", g.branches, branches)
	}
}

// sorted is the model's sessions, oldest first.
func (l *lifecycle) sorted() []*lifecycleSession {
	sessions := slices.Collect(maps.Values(l.sessions))
	slices.SortFunc(sessions, func(a, b *lifecycleSession) int { return a.order - b.order })
	return sessions
}
