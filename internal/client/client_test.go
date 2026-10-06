package client

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	protocol "github.com/coder/acp-go-sdk"

	"ox/internal/acp"
	"ox/internal/agent"
	"ox/internal/catalog"
	"ox/internal/openroutertest"
	"ox/internal/server"
	"ox/internal/settings"
	"ox/internal/store"
	"ox/internal/transcript"
)

// start runs the server in process in a new workspace against a scripted
// OpenRouter that replies in order.
func start(t *testing.T, replies ...openroutertest.Reply) (*Conn, *Session) {
	t.Helper()
	sessions, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	openrouter := openroutertest.Start(t, replies...)
	a := &agent.Agent{Store: sessions, OpenRouter: openrouter.Client(), Catalog: openroutertest.ParsedCatalog()}
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	rpc := acp.NewConn(serverOut)
	srv := server.New(rpc, a, settings.Settings{Model: openroutertest.DefaultModel, Effort: catalog.EffortDefault, Mode: transcript.ModeAsk}, t.TempDir())
	done := make(chan struct{})
	go func() {
		rpc.Serve(serverIn, srv)
		srv.Shutdown()
		serverOut.Close()
		sessions.Close()
		close(done)
	}()
	conn, created, err := Connect(clientIn, clientOut, t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn.Close()
		<-done
	})
	return conn, NewSession(conn, created)
}

// next waits for the next event.
func next(t *testing.T, conn *Conn) Event {
	t.Helper()
	events := make(chan Event, 1)
	go func() { events <- conn.Next() }()
	select {
	case event := <-events:
		return event
	case <-time.After(10 * time.Second):
		t.Fatal("no event within 10 seconds")
		return nil
	}
}

// turn collects the turn's response text, answering permission requests with
// choices in order, and reports whether the turn succeeded.
func turn(t *testing.T, s *Session, choices ...int) (string, bool) {
	t.Helper()
	var text strings.Builder
	for {
		switch event := next(t, s.conn).(type) {
		case Update:
			if chunk := event.Update.AgentMessageChunk; chunk != nil && chunk.Content.Text != nil {
				text.WriteString(chunk.Content.Text.Text)
			}
		case *Permission:
			must(t, s.Ask(event))
			if len(choices) > 0 {
				if answered, err := s.Answer(choices[0]); !answered || err != nil {
					t.Fatalf("answer %d: %v", choices[0], err)
				}
				choices = choices[1:]
			}
		case Finished:
			must(t, s.Finished())
			return text.String(), event.Err == nil
		case Closed:
			t.Fatal("the server closed the connection")
		}
	}
}

// awaitPermission holds the next permission request.
func awaitPermission(t *testing.T, s *Session) {
	t.Helper()
	for s.Permission == nil {
		if permission, ok := next(t, s.conn).(*Permission); ok {
			must(t, s.Ask(permission))
		}
	}
}

// awaitMessage waits for the first response chunk.
func awaitMessage(t *testing.T, conn *Conn) {
	t.Helper()
	for {
		if update, ok := next(t, conn).(Update); ok && update.Update.AgentMessageChunk != nil {
			return
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func hang(text string) openroutertest.Reply {
	data, _ := json.Marshal(openroutertest.Delta(map[string]any{"role": "assistant", "content": text}, ""))
	return openroutertest.Hang("data: " + string(data) + "\n\n")
}

func TestPromptsShareOneSessionAndStreamInOrder(t *testing.T) {
	streamed := openroutertest.Chunks(
		openroutertest.Delta(map[string]any{"role": "assistant", "content": "stream "}, ""),
		openroutertest.Delta(map[string]any{"content": "arrives "}, ""),
		openroutertest.Delta(map[string]any{"content": "in order\n"}, "stop"),
	)
	_, s := start(t, streamed, openroutertest.Echo())
	id := s.ID
	s.Prompt("stream")
	if text, ok := turn(t, s); text != "stream arrives in order\n" || !ok {
		t.Errorf("%q, %v", text, ok)
	}
	s.Prompt("second")
	if text, _ := turn(t, s); !strings.Contains(text, "second") {
		t.Errorf("%q", text)
	}
	if s.ID != id {
		t.Errorf("the session changed from %s to %s", id, s.ID)
	}
}

func TestResumeListsAndLoadsADifferentSessionAfterClose(t *testing.T) {
	conn, s := start(t, openroutertest.Text("saved"), openroutertest.Echo())
	if !conn.CanResume || len(s.Commands) != 0 {
		t.Fatalf("can resume %v, commands %v", conn.CanResume, s.Commands)
	}
	update, _ := next(t, conn).(Update)
	if update.SessionID != s.ID || update.Update.AvailableCommandsUpdate == nil {
		t.Errorf("first event %+v", update)
	}
	s.Update(update.Update)
	if len(s.Commands) != 0 {
		t.Errorf("commands %v", s.Commands)
	}
	old := s.ID
	s.Prompt("First session")
	turn(t, s)
	newer, err := conn.NewSession()
	must(t, err)
	if update, _ := next(t, conn).(Update); update.SessionID != newer.SessionId || update.Update.AvailableCommandsUpdate == nil {
		t.Errorf("event after a new session %+v", update)
	}
	listed, err := conn.ListSessions()
	must(t, err)
	if len(listed) != 2 {
		t.Fatalf("listed %+v", listed)
	}
	for _, item := range listed {
		if item.UpdatedAt == nil {
			t.Errorf("%s has no activity time", item.SessionId)
		}
		if item.SessionId == old && (item.Title == nil || *item.Title != "First session") {
			t.Errorf("the first session's title is %v", item.Title)
		}
	}
	must(t, conn.CloseSession(s.ID))
	must(t, s.Closed())
	if s.Active() || s.ConfigOptions != nil || s.Commands != nil || s.Usage != nil {
		t.Errorf("closed state %+v", s)
	}
	loaded, err := conn.LoadSession(newer.SessionId)
	must(t, err)
	s.Opened(newer.SessionId, loaded.ConfigOptions)
	update, _ = next(t, conn).(Update)
	if update.SessionID != newer.SessionId || update.Update.AvailableCommandsUpdate == nil {
		t.Errorf("event after a load %+v", update)
	}
	s.Update(update.Update)
	if !s.Active() || s.ID != newer.SessionId || len(s.ConfigOptions) != 3 {
		t.Fatalf("loaded state %+v", s)
	}
	for _, option := range s.ConfigOptions {
		if option.Select != nil && option.Select.Id == "mode" && option.Select.CurrentValue != "ask" {
			t.Errorf("mode %s", option.Select.CurrentValue)
		}
	}
	s.Prompt("after")
	if text, _ := turn(t, s); text != "you said: after" {
		t.Errorf("%q", text)
	}
}

func TestANewSessionReplacesTheClosedOne(t *testing.T) {
	conn, s := start(t)
	old := s.ID
	must(t, conn.CloseSession(s.ID))
	must(t, s.Closed())
	created, err := conn.NewSession()
	must(t, err)
	s.Opened(created.SessionId, created.ConfigOptions)
	if !s.Active() || s.ID == old || len(s.ConfigOptions) == 0 {
		t.Errorf("%+v", s)
	}
}

func TestAFailedLoadCanBeFollowedByAnotherSelection(t *testing.T) {
	conn, s := start(t)
	other, err := conn.NewSession()
	must(t, err)
	must(t, conn.CloseSession(s.ID))
	must(t, s.Closed())
	if _, err := conn.LoadSession("missing"); err == nil {
		t.Fatal("loaded a missing session")
	}
	if s.Active() {
		t.Fatal("active after a failed load")
	}
	loaded, err := conn.LoadSession(other.SessionId)
	must(t, err)
	s.Opened(other.SessionId, loaded.ConfigOptions)
	if !s.Active() {
		t.Error("inactive after a load")
	}
}

// fakeAgent answers initialization with its response and creates sessions.
type fakeAgent struct{ initialized protocol.InitializeResponse }

func (f fakeAgent) HandleRequest(r *acp.Request) {
	switch r.Method {
	case acp.MethodInitialize:
		r.Respond(f.initialized)
	case acp.MethodNewSession:
		r.Respond(protocol.NewSessionResponse{SessionId: "session"})
	default:
		r.Fail(&acp.Error{Code: -32601, Message: "Method not found"})
	}
}

func (fakeAgent) HandleNotification(string, json.RawMessage) {}
func (fakeAgent) Shutdown()                                  {}

func connectFake(agent fakeAgent) (*Conn, protocol.NewSessionResponse, error) {
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	go acp.NewConn(agentOut).Serve(agentIn, agent)
	return Connect(clientIn, clientOut, "/", "test")
}

func TestResumeRequiresAdvertisedListLoadAndCloseCapabilities(t *testing.T) {
	conn, created, err := connectFake(fakeAgent{protocol.InitializeResponse{ProtocolVersion: acp.ProtocolVersion}})
	must(t, err)
	defer conn.Close()
	if conn.CanResume || !NewSession(conn, created).Active() {
		t.Errorf("can resume %v", conn.CanResume)
	}
}

func TestRejectsAnUnsupportedProtocolVersion(t *testing.T) {
	_, _, err := connectFake(fakeAgent{protocol.InitializeResponse{ProtocolVersion: 2}})
	if err == nil || !strings.Contains(err.Error(), "ACP version 2") {
		t.Errorf("%v", err)
	}
}

func TestCancellationAnswersPendingPermissionsBeforeTheNextPrompt(t *testing.T) {
	_, s := start(t, openroutertest.Shell("touch cancelled"), openroutertest.Echo())
	s.Prompt("run a command")
	awaitPermission(t, s)
	must(t, s.Cancel())
	if s.Permission != nil {
		t.Fatal("the permission request is still pending")
	}
	if _, ok := turn(t, s); !ok {
		t.Error("the cancelled turn failed")
	}
	s.Prompt("after")
	if _, ok := turn(t, s); !ok {
		t.Error("the next turn failed")
	}
}

func TestARunningTurnAcceptsCancelWithoutAPermissionRequest(t *testing.T) {
	conn, s := start(t, hang("cancelled"))
	s.Prompt("running")
	awaitMessage(t, conn)
	must(t, s.Cancel())
	if text, ok := turn(t, s); text != "" || !ok {
		t.Errorf("%q, %v", text, ok)
	}
}

func TestClosingACancelledSessionHoldsTheNextPermissionRequest(t *testing.T) {
	conn, s := start(t, hang("cancelled"), openroutertest.Shell("touch next"))
	s.Prompt("running")
	awaitMessage(t, conn)
	must(t, s.Cancel())
	must(t, conn.CloseSession(s.ID))
	must(t, s.Closed())
	created, err := conn.NewSession()
	must(t, err)
	s.Opened(created.SessionId, created.ConfigOptions)
	s.Prompt("run a command")
	for s.Permission == nil {
		if permission, ok := next(t, conn).(*Permission); ok && s.Accepts(permission.SessionID) {
			must(t, s.Ask(permission))
		}
	}
}

func TestRejectedAndFailedTurnsAllowAnotherPrompt(t *testing.T) {
	failed := openroutertest.Chunks(openroutertest.Delta(map[string]any{"role": "assistant", "content": "failing"}, ""))
	_, s := start(t, openroutertest.Status(400, "{}"), failed, openroutertest.Echo())
	for _, text := range []string{"reject", "fail"} {
		s.Prompt(text)
		if _, ok := turn(t, s); ok {
			t.Errorf("%s succeeded", text)
		}
	}
	s.Prompt("after")
	if _, ok := turn(t, s); !ok {
		t.Error("the turn after the failures failed")
	}
}

// TestAgentProcess is the server of the Start tests when OX_TEST_AGENT is
// set. It writes one line to standard error, serves fakeAgent until its input
// ends, and exits when a prompt arrives.
func TestAgentProcess(t *testing.T) {
	if os.Getenv("OX_TEST_AGENT") == "" {
		return
	}
	fmt.Fprintln(os.Stderr, "agent started")
	acp.NewConn(os.Stdout).Serve(os.Stdin, exitingAgent{fakeAgent{protocol.InitializeResponse{ProtocolVersion: acp.ProtocolVersion}}})
	os.Exit(0)
}

// exitingAgent is fakeAgent that exits when prompted.
type exitingAgent struct{ fakeAgent }

func (a exitingAgent) HandleRequest(r *acp.Request) {
	if r.Method == acp.MethodPrompt {
		os.Exit(1)
	}
	a.fakeAgent.HandleRequest(r)
}

// startProcess starts the test binary as the server in TestAgentProcess.
func startProcess(t *testing.T) (*Conn, *Session) {
	t.Helper()
	executable, err := os.Executable()
	must(t, err)
	t.Setenv("OX_TEST_AGENT", "1")
	conn, created, err := Start(settings.Server{Command: executable, Args: []string{"-test.run=^TestAgentProcess$"}}, t.TempDir(), "test")
	must(t, err)
	t.Cleanup(conn.Close)
	return conn, NewSession(conn, created)
}

func TestAServerExitFinishesThePromptAndClosesTheConnection(t *testing.T) {
	conn, s := startProcess(t)
	s.Prompt("exit")
	// The connection can report Closed before it fails the prompt.
	var finished *Finished
	for {
		switch event := next(t, conn).(type) {
		case Finished:
			finished = &event
		case Closed:
			if finished != nil {
				if finished.Err == nil {
					t.Error("the prompt succeeded")
				}
				return
			}
		}
	}
}

func TestDiagnosticsArriveFromStandardError(t *testing.T) {
	conn, _ := startProcess(t)
	if event := next(t, conn); event != Diagnostic("agent started") {
		t.Errorf("%#v", event)
	}
}
