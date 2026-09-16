package agent

import (
	"reflect"
	"testing"

	"github.com/kkestell/ox/internal/lsp"
)

func TestAgentOwnsLanguageConfiguration(t *testing.T) {
	definitions := []lsp.Definition{{Name: "gopls", Command: "gopls", Args: []string{"-rpc.trace"}, Extensions: []string{"go"}}}
	instance, err := New(Config{LanguageServers: definitions})
	if err != nil {
		t.Fatal(err)
	}
	definitions[0].Args[0] = "changed"
	definitions[0].Extensions[0] = "changed"
	if !reflect.DeepEqual(instance.languageServers[0].Args, []string{"-rpc.trace"}) ||
		!reflect.DeepEqual(instance.languageServers[0].Extensions, []string{"go"}) {
		t.Fatal("activation configuration changed with caller input")
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
		LanguageServers: []lsp.Definition{{
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
	value := &session{languages: manager}
	if value.languagesFor() == nil {
		t.Fatal("a configured session reports no language access")
	}
	// Nothing was started, so closing the activation is clean even though the
	// configured command does not exist.
	if err := value.close(); err != nil {
		t.Fatal(err)
	}
}
