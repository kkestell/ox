package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"ox/internal/textfile"
)

// Server is an ACP server executable the terminal client can start.
type Server struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// Client is the terminal client's fields of the global settings file.
type Client struct {
	// Servers is empty when the file configures none.
	Servers []Server
	// Favorites are model IDs, in the order they were added.
	Favorites []string
}

// ReadClient reads the client's fields of the global settings file at path.
// A missing file has no servers and no favorites.
func ReadClient(path string) (Client, error) {
	text, err := textfile.Read(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Client{}, nil
	}
	if err != nil {
		return Client{}, fmt.Errorf("reading %s: %w", path, err)
	}
	client, err := parseClient(text)
	if err != nil {
		return Client{}, fmt.Errorf("reading %s: %w", path, err)
	}
	return client, nil
}

func parseClient(text string) (Client, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &fields); err != nil {
		return Client{}, err
	}
	if fields == nil {
		return Client{}, errors.New("settings must be a JSON object")
	}
	var client Client
	if raw, ok := fields["servers"]; ok {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&client.Servers); err != nil {
			return Client{}, fmt.Errorf("servers: %w", err)
		}
	}
	if raw, ok := fields["favorites"]; ok {
		if err := json.Unmarshal(raw, &client.Favorites); err != nil {
			return Client{}, fmt.Errorf("favorites: %w", err)
		}
	}
	names := map[string]bool{}
	for _, server := range client.Servers {
		if strings.TrimSpace(server.Name) == "" || names[server.Name] {
			return Client{}, fmt.Errorf("server names must be nonempty and unique: %s", server.Name)
		}
		names[server.Name] = true
	}
	return client, nil
}

// SaveFavorites replaces favorites in the global settings file at path,
// keeping its other fields.
func SaveFavorites(path string, favorites []string) error {
	err := update(path, func(fields map[string]any) { fields["favorites"] = favorites })
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
