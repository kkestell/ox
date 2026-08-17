package openrouter_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/kkestell/ox/internal/openrouter"
)

func TestContentBlockMarshalsEachVariant(t *testing.T) {
	for _, test := range []struct {
		name  string
		block openrouter.ContentBlock
		want  string
	}{
		{
			name:  "text",
			block: openrouter.ContentBlock{Type: "text", Text: "hello"},
			want:  `{"type":"text","text":"hello"}`,
		},
		{
			name:  "empty text",
			block: openrouter.ContentBlock{Type: "text"},
			want:  `{"type":"text","text":""}`,
		},
		{
			name:  "image",
			block: openrouter.ContentBlock{Type: "image_url", ImageURL: "data:image/png;base64,YQ=="},
			want:  `{"type":"image_url","image_url":{"url":"data:image/png;base64,YQ=="}}`,
		},
		{
			name: "audio",
			block: openrouter.ContentBlock{
				Type: "input_audio", AudioData: "YQ==", AudioFormat: "wav",
			},
			want: `{"type":"input_audio","input_audio":{"data":"YQ==","format":"wav"}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := json.Marshal(test.block)
			if err != nil {
				t.Fatal(err)
			}
			assertJSONEqual(t, got, []byte(test.want))
		})
	}
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode got JSON: %v", err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("decode want JSON: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON mismatch\ngot:  %s\nwant: %s", got, want)
	}
}
