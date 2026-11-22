package ur

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

const model = "deepseek/test"

// sse writes a streamed completion of words, ending with data: [DONE].
func sse(w http.ResponseWriter, words ...string) {
	fmt.Fprint(w, ": OPENROUTER PROCESSING\n\n")
	for _, word := range words {
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", word)
	}
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"\"},\"finish_reason\":\"stop\"}]}\n\n")
	fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"total_tokens\":3}}\n\n")
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// recorded is a request the server received.
type recorded struct {
	auth string
	body map[string]any
}

// run is an agent driven from the test goroutine against a canned server.
type run struct {
	t          *testing.T
	harness    agent.Harness
	dir        string
	settings   agent.Settings // the settings the next launch starts with
	agent      agent.Agent
	updates    []agent.Update
	configured []agent.Settings
	tokens     []string // every token OnResumable received
	errors     []error  // every error OnError received
	requests   chan recorded
}

// handler answers the nth request, counting from 0.
type handler func(w http.ResponseWriter, req *http.Request, n int)

func start(t *testing.T, settings agent.Settings, providers map[string][]string, handle handler) *run {
	t.Helper()
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	r := &run{t: t, dir: filepath.Join(t.TempDir(), "ur"), settings: settings, requests: make(chan recorded, 16)}
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		data, _ := io.ReadAll(req.Body)
		var body map[string]any
		json.Unmarshal(data, &body)
		r.requests <- recorded{req.Header.Get("Authorization"), body}
		handle(w, req, int(count.Add(1))-1)
	}))
	t.Cleanup(server.Close)
	r.harness = Harness(server.URL, r.dir, providers)
	if err := r.launch(""); err != nil {
		t.Fatal(err)
	}
	return r
}

// launch starts the run's agent with the latest settings, resuming the
// conversation token names, as the server does after parking.
func (r *run) launch(token string) error {
	a, err := r.harness.Start(agent.Options{
		Resume:      token,
		Settings:    r.settings,
		OnUpdate:    func(u agent.Update) { r.updates = append(r.updates, u) },
		OnResumable: func(token string) { r.tokens = append(r.tokens, token) },
		OnConfigured: func(s agent.Settings) {
			r.configured = append(r.configured, s)
			r.settings = s
		},
		OnError: func(err error) { r.errors = append(r.errors, err) },
	})
	if err != nil {
		return err
	}
	r.agent = a
	r.t.Cleanup(func() {
		if !a.Exited() {
			a.Stop()
		}
	})
	return nil
}

// file is the conversation file of the run's only conversation.
func (r *run) file() string {
	r.t.Helper()
	if len(r.tokens) != 1 {
		r.t.Fatalf("OnResumable got %q, not one token", r.tokens)
	}
	return filepath.Join(r.dir, r.tokens[0]+".jsonl")
}

func (r *run) send(id, content string) {
	r.t.Helper()
	if err := r.agent.Send(agent.Input{MessageID: id, Text: content, Content: content}); err != nil {
		r.t.Fatal(err)
	}
}

// until handles events until an update satisfies done, and returns it.
func (r *run) until(done func(agent.Update) bool) agent.Update {
	r.t.Helper()
	timeout := time.After(5 * time.Second)
	for seen := 0; ; {
		for ; seen < len(r.updates); seen++ {
			if done(r.updates[seen]) {
				u := r.updates[seen]
				r.updates = r.updates[seen+1:]
				return u
			}
		}
		select {
		case e := <-r.agent.Events():
			r.agent.Handle(e)
		case <-timeout:
			r.t.Fatalf("no matching update; got %+v", r.updates)
		}
	}
}

func (r *run) idle() {
	r.t.Helper()
	r.until(func(u agent.Update) bool { return u.Type == agent.UpdateState && u.State == agent.StateIdle })
}

// result handles events until the turn's result, and returns it.
func (r *run) result() agent.Result {
	r.t.Helper()
	return *r.until(func(u agent.Update) bool { return u.Type == agent.UpdateResult }).Result
}

func (r *run) request() recorded {
	r.t.Helper()
	select {
	case req := <-r.requests:
		return req
	case <-time.After(5 * time.Second):
		r.t.Fatal("no request")
		return recorded{}
	}
}

func roles(body map[string]any) []string {
	return field(body, "role")
}

func contents(body map[string]any) []string {
	return field(body, "content")
}

// field is the given field of each of a request's messages.
func field(body map[string]any, name string) []string {
	var values []string
	for _, m := range body["messages"].([]any) {
		values = append(values, m.(map[string]any)[name].(string))
	}
	return values
}

func TestTwoTurns(t *testing.T) {
	settings := agent.Settings{Model: model, Effort: "low", PermissionMode: "manual"}
	r := start(t, settings, map[string][]string{model: {"a/x", "b/y"}}, func(w http.ResponseWriter, _ *http.Request, _ int) {
		sse(w, "Hello", ", ", "world")
	})
	r.send("p1", "hi")
	var kinds []string
	for {
		u := r.until(func(agent.Update) bool { return true })
		if u.MessageID != "p1" {
			t.Errorf("%s update carries message ID %q, not p1", u.Type, u.MessageID)
		}
		kind := u.Type
		if u.State != "" {
			kind += " " + u.State
		}
		if u.Type == agent.UpdateTextDelta && u.Text == "" {
			t.Error("an empty text delta was published")
		}
		if u.Type == agent.UpdateResult {
			if u.Result.IsError || u.Result.Text != "Hello, world" {
				t.Errorf("result %+v, not the joined reply", u.Result)
			}
		}
		if len(kinds) == 0 || kinds[len(kinds)-1] != kind {
			kinds = append(kinds, kind)
		}
		if kind == "state idle" {
			break
		}
	}
	want := []string{"prompt_queued", "state running", "prompt", "text_delta", "result", "state idle"}
	if !slices.Equal(kinds, want) {
		t.Errorf("updates %q, not %q", kinds, want)
	}
	r.request()

	r.send("p2", "again")
	r.idle()
	second := r.request()
	if second.auth != "Bearer test-key" {
		t.Errorf("Authorization %q", second.auth)
	}
	if second.body["model"] != model || second.body["stream"] != true {
		t.Errorf("model or stream wrong: %v", second.body)
	}
	if effort := second.body["reasoning"].(map[string]any)["effort"]; effort != "low" {
		t.Errorf("reasoning effort %v", effort)
	}
	p := second.body["provider"].(map[string]any)
	if fmt.Sprint(p["order"]) != "[a/x b/y]" || p["allow_fallbacks"] != false {
		t.Errorf("provider %v", p)
	}
	if got := roles(second.body); !slices.Equal(got, []string{"user", "assistant", "user"}) {
		t.Errorf("messages %q", got)
	}
}

func TestUnpinnedModelWithoutEffort(t *testing.T) {
	r := start(t, agent.Settings{Model: model, PermissionMode: "manual"}, map[string][]string{"other": {"a/x"}}, func(w http.ResponseWriter, _ *http.Request, _ int) {
		sse(w, "ok")
	})
	r.send("p1", "hi")
	r.idle()
	body := r.request().body
	if _, ok := body["provider"]; ok {
		t.Errorf("a model without providers sent provider: %v", body["provider"])
	}
	if _, ok := body["reasoning"]; ok {
		t.Errorf("an empty effort sent reasoning: %v", body["reasoning"])
	}
}

// The stream's last line counts even without a trailing newline.
func TestLastLineWithoutNewline(t *testing.T) {
	for name, stream := range map[string]string{
		"[DONE]":       "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]",
		"finish chunk": "data: {\"choices\":[{\"delta\":{\"content\":\"o\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"k\"},\"finish_reason\":\"stop\"}]}",
	} {
		t.Run(name, func(t *testing.T) {
			r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, _ *http.Request, _ int) {
				fmt.Fprint(w, stream)
			})
			r.send("p1", "hi")
			if result := r.result(); result.IsError || result.Text != "ok" {
				t.Errorf("result %+v", result)
			}
		})
	}
}

// A reply that finishes without text ends the turn with an empty result.
func TestEmptyReply(t *testing.T) {
	r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, _ *http.Request, _ int) {
		sse(w)
	})
	r.send("p1", "hi")
	if result := r.result(); result.IsError || result.Text != "" {
		t.Errorf("result %+v, not an empty reply", result)
	}
}

// A failed turn ends with its error. The next request carries the reply the
// user saw, and no empty reply when there was none.
func TestStreamErrors(t *testing.T) {
	for name, c := range map[string]struct {
		handle handler
		want   string
		roles  []string // of the next request
	}{
		"mid-stream error chunk": {func(w http.ResponseWriter, _ *http.Request, _ int) {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"par\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"error\":{\"code\":502,\"message\":\"provider went away\"},\"choices\":[{\"delta\":{\"content\":\"\"},\"finish_reason\":\"error\"}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}, "OpenRouter: provider went away", []string{"user", "assistant", "user"}},
		"refused request": {func(w http.ResponseWriter, _ *http.Request, _ int) {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"code":401,"message":"No auth credentials found"}}`)
		}, "OpenRouter 401 Unauthorized: No auth credentials found", []string{"user", "user"}},
		"refused request without a message": {func(w http.ResponseWriter, _ *http.Request, _ int) {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, "upstream down")
		}, "OpenRouter 502 Bad Gateway", []string{"user", "user"}},
		"refused request with an error without a message": {func(w http.ResponseWriter, _ *http.Request, _ int) {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, `{"error":{"code":502}}`)
		}, "OpenRouter 502 Bad Gateway", []string{"user", "user"}},
		"unfinished stream": {func(w http.ResponseWriter, _ *http.Request, _ int) {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"par\"}}]}\n\n")
		}, "OpenRouter stream ended before the response finished", []string{"user", "assistant", "user"}},
		"cut connection": {func(w http.ResponseWriter, _ *http.Request, _ int) {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"par\"}}]}\n\n")
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		}, "unexpected EOF", []string{"user", "assistant", "user"}},
		"malformed chunk": {func(w http.ResponseWriter, _ *http.Request, _ int) {
			fmt.Fprint(w, "data: {\"choices\":\n\n")
		}, "malformed OpenRouter stream chunk: unexpected end of JSON input", []string{"user", "user"}},
	} {
		t.Run(name, func(t *testing.T) {
			r := start(t, agent.Settings{Model: model}, nil, c.handle)
			r.send("p1", "hi")
			if result := r.result(); !result.IsError || result.Text != c.want {
				t.Errorf("result %+v, not the error %q", result, c.want)
			}
			r.idle()
			r.request()
			r.send("p2", "again")
			r.idle()
			if got := roles(r.request().body); !slices.Equal(got, c.roles) {
				t.Errorf("the next request's messages %q, not %q", got, c.roles)
			}
		})
	}
}

func TestInterrupt(t *testing.T) {
	r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, req *http.Request, n int) {
		if n > 0 {
			sse(w, "ok")
			return
		}
		// The second delta is read with the first, and is in flight when the
		// turn is interrupted.
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\" late\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-req.Context().Done()
	})
	r.send("p1", "hi")
	r.until(func(u agent.Update) bool { return u.Type == agent.UpdateTextDelta })
	r.send("p2", "next")
	r.updates = nil
	if err := r.agent.Interrupt(); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, u := range r.updates {
		kinds = append(kinds, u.Type+" "+u.MessageID+" "+u.State)
	}
	want := []string{"prompt_cancelled p2 ", "result p1 ", "state p1 idle"}
	if !slices.Equal(kinds, want) {
		t.Fatalf("interrupt published %q, not %q", kinds, want)
	}
	if result := r.updates[1].Result; !result.IsError || result.Text != "Interrupted" {
		t.Errorf("interrupted result %+v", result)
	}
	r.updates = nil
	r.request()

	// The interrupted request's end arrives during the next turn.
	r.send("p3", "again")
	for {
		u := r.until(func(agent.Update) bool { return true })
		if u.Type == agent.UpdateTextDelta && u.Text != "ok" {
			t.Errorf("the interrupted turn's text %q reached the next turn", u.Text)
		}
		if u.Type == agent.UpdateResult && (u.Result.IsError || u.Result.Text != "ok") {
			t.Errorf("the turn after the interrupt ended with %+v", u.Result)
		}
		if u.Type == agent.UpdateState && u.State == agent.StateIdle {
			break
		}
	}
	if got := contents(r.request().body); !slices.Equal(got, []string{"hi", "partial", "again"}) {
		t.Errorf("history after the interrupt %q", got)
	}
	r.updates = nil
	if err := r.agent.Interrupt(); err != nil || len(r.updates) != 0 {
		t.Errorf("Interrupt while idle: %v, published %+v", err, r.updates)
	}
}

func TestInputDuringTurnRunsNext(t *testing.T) {
	release := make(chan struct{})
	r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, _ *http.Request, n int) {
		if n == 0 {
			<-release
		}
		sse(w, fmt.Sprint("reply ", n))
	})
	if !r.agent.Parkable() {
		t.Error("a new agent is not parkable")
	}
	r.send("p1", "first")
	r.request()
	if r.agent.Parkable() {
		t.Error("parkable while a turn runs")
	}
	r.send("p2", "second")
	if r.agent.Parkable() {
		t.Error("parkable while an input waits")
	}
	if snapshot := r.agent.Snapshot(); snapshot.State != agent.StateRunning || len(snapshot.Queued) != 1 || snapshot.Queued[0].MessageID != "p2" {
		t.Errorf("snapshot during the turn: %+v", snapshot)
	}
	close(release)
	var kinds []string
	for {
		u := r.until(func(agent.Update) bool { return true })
		if u.Type == agent.UpdateTextDelta {
			continue
		}
		kinds = append(kinds, u.Type+" "+u.MessageID+" "+u.State)
		if u.Type == agent.UpdateState && u.State == agent.StateIdle && u.MessageID == "p2" {
			break
		}
	}
	want := []string{"prompt_queued p1 ", "state p1 running", "prompt p1 ", "prompt_queued p2 ", "result p1 ", "prompt p2 ", "result p2 ", "state p2 idle"}
	if !slices.Equal(kinds, want) {
		t.Errorf("updates %q, not %q", kinds, want)
	}
	if got := roles(r.request().body); !slices.Equal(got, []string{"user", "assistant", "user"}) {
		t.Errorf("the second request's messages %q", got)
	}
	if snapshot := r.agent.Snapshot(); snapshot.State != agent.StateIdle || len(snapshot.Queued) != 0 {
		t.Errorf("snapshot after the turns: %+v", snapshot)
	}
	if !r.agent.Parkable() {
		t.Error("not parkable once idle")
	}
}

func TestConfigure(t *testing.T) {
	release := make(chan struct{})
	r := start(t, agent.Settings{Model: model, Effort: "low"}, nil, func(w http.ResponseWriter, _ *http.Request, _ int) {
		<-release
		sse(w, "ok")
	})
	r.send("p1", "hi")
	other := "other/model"
	high := "high"
	var got error
	r.agent.Configure(agent.SettingsPatch{Model: &other}, func(err error) { got = err })
	if !errors.Is(got, agent.ErrBusy) {
		t.Errorf("Configure while running: %v", got)
	}
	close(release)
	r.idle()
	r.request()

	low := "low"
	r.agent.Configure(agent.SettingsPatch{Effort: &low}, func(err error) { got = err })
	if got != nil || len(r.configured) != 0 {
		t.Errorf("Configure without a change: %v, OnConfigured got %+v", got, r.configured)
	}
	got = errors.New("not called")
	r.agent.Configure(agent.SettingsPatch{Model: &other, Effort: &high}, func(err error) { got = err })
	if got != nil {
		t.Fatalf("Configure while idle: %v", got)
	}
	want := agent.Settings{Model: other, Effort: high}
	if !slices.Equal(r.configured, []agent.Settings{want}) {
		t.Errorf("OnConfigured got %+v, not %+v", r.configured, want)
	}
	r.send("p2", "again")
	r.idle()
	body := r.request().body
	if body["model"] != other || body["reasoning"].(map[string]any)["effort"] != high {
		t.Errorf("the next request did not use the new settings: %v", body)
	}
}

func TestStartWithoutKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	_, err := Harness("http://unused", t.TempDir(), nil).Start(agent.Options{})
	if err == nil || !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Fatalf("Start without a key: %v", err)
	}
}

// Stop ends the running turn and drops waiting inputs. Its reply has already
// been saved as far as it streamed.
func TestStopDuringTurn(t *testing.T) {
	r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, req *http.Request, n int) {
		if n > 0 {
			sse(w, "ok")
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-req.Context().Done()
	})
	r.send("p1", "hi")
	r.request()
	r.until(func(u agent.Update) bool { return u.Type == agent.UpdateTextDelta })
	r.send("p2", "waiting")
	r.updates = nil
	r.agent.Stop()
	if !r.agent.Exited() {
		t.Error("Exited is false after Stop")
	}
	var got []string
	for _, u := range r.updates {
		got = append(got, u.Type+" "+u.MessageID+" "+u.Error)
	}
	want := []string{"prompt_cancelled p2 ", "exited p1 stopped"}
	if !slices.Equal(got, want) {
		t.Errorf("Stop published %q, not %q", got, want)
	}
	if err := r.launch(r.tokens[0]); err != nil {
		t.Fatal(err)
	}
	r.send("p3", "next")
	r.idle()
	if got := contents(r.request().body); !slices.Equal(got, []string{"hi", "partial", "next"}) {
		t.Errorf("the resumed request's messages %q", got)
	}
}

// A second agent sees the streamed reply while the first request is still open.
func TestResumeDuringTurn(t *testing.T) {
	r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, req *http.Request, n int) {
		if n > 0 {
			sse(w, "ok")
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"remember\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" blue\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-req.Context().Done()
	})
	r.send("p1", "hi")
	r.request()
	r.until(func(u agent.Update) bool { return u.Type == agent.UpdateTextDelta && u.Text == " blue" })

	if err := r.launch(r.tokens[0]); err != nil {
		t.Fatal(err)
	}
	r.send("p2", "next")
	r.idle()
	body := r.request().body
	if got := contents(body); !slices.Equal(got, []string{"hi", "remember blue", "next"}) {
		t.Errorf("the resumed request's messages %q", got)
	}
	if got := roles(body); !slices.Equal(got, []string{"user", "assistant", "user"}) {
		t.Errorf("the resumed request's roles %q", got)
	}
}

// A conversation survives a new agent, which sends each reply as one message.
func TestResume(t *testing.T) {
	r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, _ *http.Request, n int) {
		sse(w, [][]string{{"o", "ne"}, {"two"}, {"three"}}[n]...)
	})
	r.send("p1", "hi")
	if len(r.tokens) != 1 || r.tokens[0] == "" {
		t.Fatalf("OnResumable got %q before the first request", r.tokens)
	}
	r.idle()
	r.request()
	r.send("p2", "again")
	r.idle()
	r.request()
	r.agent.Stop()

	if err := r.launch(r.tokens[0]); err != nil {
		t.Fatal(err)
	}
	r.send("p3", "third")
	r.idle()
	body := r.request().body
	if got := contents(body); !slices.Equal(got, []string{"hi", "one", "again", "two", "third"}) {
		t.Errorf("the resumed request's messages %q", got)
	}
	if got := roles(body); !slices.Equal(got, []string{"user", "assistant", "user", "assistant", "user"}) {
		t.Errorf("the resumed request's roles %q", got)
	}
	if len(r.tokens) != 1 {
		t.Errorf("OnResumable got %q, not the one token", r.tokens)
	}
}

// A conversation whose file is gone or damaged refuses the start and clears
// the token.
func TestResumeLost(t *testing.T) {
	for name, c := range map[string]struct {
		content *string
		cause   error
	}{
		"missing file":        {nil, fs.ErrNotExist},
		"truncated last line": {new("{\"role\":\"user\",\"content\":\"hi\"}\n{\"role\":\"assis"), io.ErrUnexpectedEOF},
	} {
		t.Run(name, func(t *testing.T) {
			r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, _ *http.Request, _ int) {
				sse(w, "ok")
			})
			if c.content != nil {
				os.MkdirAll(r.dir, 0o700)
				if err := os.WriteFile(filepath.Join(r.dir, "lost.jsonl"), []byte(*c.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.launch("lost"); !errors.Is(err, errLost) {
				t.Errorf("Start with a lost conversation: %v", err)
			}
			if !slices.Equal(r.tokens, []string{""}) {
				t.Errorf("OnResumable got %q, not the empty token", r.tokens)
			}
			if len(r.errors) != 1 || !strings.HasPrefix(r.errors[0].Error(), "resume conversation: ") || !errors.Is(r.errors[0], c.cause) {
				t.Errorf("OnError got %v, not the resume failing with %v", r.errors, c.cause)
			}
		})
	}
}

// An unsaved prompt never reaches the model, and an unsaved reply delta is
// never published.
func TestSaveFailure(t *testing.T) {
	t.Run("prompt", func(t *testing.T) {
		r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, _ *http.Request, _ int) {
			sse(w, "ok")
		})
		if err := os.WriteFile(r.dir, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		r.send("p1", "hi")
		if result := r.result(); !result.IsError || !strings.HasPrefix(result.Text, "Ur could not save the prompt: ") {
			t.Errorf("result %+v", result)
		}
		r.idle()
		os.Remove(r.dir)
		r.send("p2", "again")
		r.idle()
		if got := contents(r.request().body); !slices.Equal(got, []string{"again"}) {
			t.Errorf("the first request's messages %q", got)
		}
	})

	t.Run("reply", func(t *testing.T) {
		release := make(chan struct{})
		r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, req *http.Request, n int) {
			if n > 0 {
				sse(w, "ok")
				return
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"one\"}}]}\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-release:
				sse(w, "two")
			case <-req.Context().Done():
			}
		})
		r.send("p1", "hi")
		r.request()
		r.until(func(u agent.Update) bool { return u.Type == agent.UpdateTextDelta && u.Text == "one" })
		if err := os.Chmod(r.file(), 0o400); err != nil {
			t.Fatal(err)
		}
		close(release)
		var result agent.Result
		r.until(func(u agent.Update) bool {
			if u.Type == agent.UpdateTextDelta && u.Text == "two" {
				t.Error("published an unsaved reply delta")
			}
			if u.Type == agent.UpdateResult {
				result = *u.Result
			}
			return u.Type == agent.UpdateState && u.State == agent.StateIdle
		})
		if slices.ContainsFunc(r.updates, func(u agent.Update) bool { return u.Type == agent.UpdateTextDelta }) {
			t.Error("published a reply delta after going idle")
		}
		prefix := "Ur could not save the reply: "
		if !result.IsError || !strings.HasPrefix(result.Text, prefix) || !strings.Contains(result.Text, "permission denied") {
			t.Errorf("result %+v, expected an error starting %q that names the denied write", result, prefix)
		}
		if err := os.Chmod(r.file(), 0o600); err != nil {
			t.Fatal(err)
		}
		r.send("p2", "again")
		r.idle()
		if got := contents(r.request().body); !slices.Equal(got, []string{"hi", "one", "again"}) {
			t.Errorf("the next request's messages %q", got)
		}
	})
}
