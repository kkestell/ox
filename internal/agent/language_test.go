package agent

import (
	"context"
	"reflect"
	"testing"

	"github.com/kkestell/ox/internal/lsp"
	"github.com/kkestell/ox/internal/settings"
)

func TestLanguageDefinitionsDoNotAliasSettings(t *testing.T) {
	configured := []settings.ResolvedLanguageServer{{
		Name: "gopls", Command: "gopls",
		Args: []string{"-rpc.trace"}, Extensions: []string{"go"},
	}}
	definitions := languageDefinitions(configured)
	want := []lsp.Definition{{
		Name: "gopls", Command: "gopls",
		Args: []string{"-rpc.trace"}, Extensions: []string{"go"},
	}}
	if !reflect.DeepEqual(definitions, want) {
		t.Fatalf("definitions = %#v", definitions)
	}
	configured[0].Args[0] = "mutated"
	configured[0].Extensions[0] = "mutated"
	if !reflect.DeepEqual(definitions, want) {
		t.Fatalf("definitions changed with their source = %#v", definitions)
	}
}

func TestActivationWithoutConfiguredLanguageServers(t *testing.T) {
	instance, err := New(Config{Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := instance.activateLanguages(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if manager != nil {
		t.Fatal("an unconfigured activation started a language manager")
	}
	// languagesFor must report a nil interface rather than a non-nil interface
	// holding a nil pointer, which a tool would treat as a usable server.
	if value := (&session{}).languagesFor(); value != nil {
		t.Fatalf("unconfigured session languages = %#v", value)
	}
}

func TestActivationBuildsALazyManager(t *testing.T) {
	instance, err := New(Config{
		Logger: discardLogger(),
		LanguageServers: []settings.ResolvedLanguageServer{{
			Name: "fixture", Command: "does-not-exist", Extensions: []string{"go"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := instance.activateLanguages(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if manager == nil {
		t.Fatal("a configured activation has no language manager")
	}
	value := &session{languages: newSessionLanguages(manager)}
	if value.languagesFor() == nil {
		t.Fatal("a configured session reports no language access")
	}
	// Nothing was started, so closing the activation is clean even though the
	// configured command does not exist.
	if err := value.close(); err != nil {
		t.Fatal(err)
	}
}

func TestActivationRejectsInvalidLanguageDefinitions(t *testing.T) {
	instance, err := New(Config{
		Logger: discardLogger(),
		LanguageServers: []settings.ResolvedLanguageServer{
			{Name: "first", Command: "a", Extensions: []string{"go"}},
			{Name: "second", Command: "b", Extensions: []string{"go"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := instance.activateLanguages(t.TempDir()); err == nil {
		t.Fatal("two servers claiming one extension were accepted")
	}
}

func TestSessionLanguagesAdmitsOneQueryAtATime(t *testing.T) {
	languages := newSessionLanguages(&lsp.Manager{})
	release, err := languages.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// A second query waits rather than interleaving its document updates with
	// the first, and gives up when its own caller does.
	blocked, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := languages.acquire(blocked); err == nil {
		t.Fatal("a second concurrent query was admitted")
	}

	release()
	release, err = languages.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
}
