package settings_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ox/internal/settings"
)

func TestReadClientReadsServersAndFavorites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if client, err := settings.ReadClient(path); err != nil || !reflect.DeepEqual(client, settings.Client{}) {
		t.Errorf("a missing file: %+v, %v", client, err)
	}
	for _, test := range []struct {
		text string
		want settings.Client
	}{
		{`{}`, settings.Client{}},
		{`{"favorites":["a/b","c/d"]}`, settings.Client{Favorites: []string{"a/b", "c/d"}}},
		{`{"model":"a/b","effort":"high","mode":"auto","favorites":["a/b"]}`, settings.Client{Favorites: []string{"a/b"}}},
		{`{"servers":[{"name":"Alpha","command":"alpha"}]}`, settings.Client{Servers: []settings.Server{{Name: "Alpha", Command: "alpha"}}}},
		{`{"servers":[{"name":"Ox","command":"ox","args":["acp"]}],"favorites":["a/b"]}`, settings.Client{
			Servers: []settings.Server{{Name: "Ox", Command: "ox", Args: []string{"acp"}}}, Favorites: []string{"a/b"},
		}},
	} {
		write(t, path, test.text)
		client, err := settings.ReadClient(path)
		if err != nil || !reflect.DeepEqual(client, test.want) {
			t.Errorf("%s: %+v, %v", test.text, client, err)
		}
	}
}

func TestReadClientRejectsInvalidServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	for _, text := range []string{
		`{"servers":[{"name":"Alpha","command":"a"},{"name":"Alpha","command":"b"}]}`,
		`{"servers":[{"name":" ","command":"a"}]}`,
		`{"servers":[{"name":"","command":"a"}]}`,
		`{"servers":[{"id":"old","name":"Ox","command":"ox"}]}`,
		`[]`,
	} {
		write(t, path, text)
		if _, err := settings.ReadClient(path); err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: %v", text, err)
		}
	}
}

func TestSaveFavoritesReplacesThemAndKeepsTheOtherFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ox/settings.json")
	if err := settings.SaveFavorites(path, []string{"a/b"}); err != nil {
		t.Fatal(err)
	}
	write(t, path, `{"servers":[{"name":"Alpha","command":"alpha"}],"favorites":["a/b"]}`)
	if err := settings.SaveFavorites(path, []string{"c/d", "a/b"}); err != nil {
		t.Fatal(err)
	}
	client, err := settings.ReadClient(path)
	if err != nil || client.Servers[0].Name != "Alpha" || !reflect.DeepEqual(client.Favorites, []string{"c/d", "a/b"}) {
		t.Errorf("%+v, %v", client, err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("left behind %v", entries)
	}
	if err := settings.SaveFavorites(filepath.Dir(path), nil); err == nil {
		t.Error("saved over a directory")
	}
}
