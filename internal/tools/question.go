package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/agent"
)

const questionDescription = "Ask the user one non-sensitive question through the client's form UI. Use options for a single-choice answer or omit them for free text. Never request passwords, API keys, tokens, private keys, recovery codes, payment credentials, authorization, or permission to run a tool. A default only pre-fills the form; it is never an answer by itself."

const questionSchema = `{
  "type": "object",
  "properties": {
    "question": {"type": "string", "description": "The non-sensitive question to show the user."},
    "options": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "object",
        "properties": {
          "label": {"type": "string"},
          "description": {"type": "string"}
        },
        "required": ["label"],
        "additionalProperties": false
      }
    },
    "default": {"type": "string", "description": "Optional value to pre-fill. The user must still submit the form."}
  },
  "required": ["question"],
  "additionalProperties": false
}`

type questionArguments struct {
	Question *string         `json:"question"`
	Options  json.RawMessage `json:"options,omitempty"`
	Default  *string         `json:"default,omitempty"`
}

type questionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type questionResult struct {
	Outcome string `json:"outcome"`
	Answer  string `json:"answer,omitempty"`
}

func executeQuestion(ctx context.Context, invocation agent.Invocation) (string, error) {
	var arguments questionArguments
	if err := decodeArgs(invocation.Arguments, &arguments); err != nil {
		return "", err
	}
	question, err := requireString("question", arguments.Question)
	if err != nil {
		return "", err
	}
	question = strings.TrimSpace(question)
	if question == "" {
		return "", errors.New("`question` must not be blank")
	}

	property := acp.ElicitationStringProperty{Type: "string", Title: "Answer"}
	if len(arguments.Options) == 0 {
		minimum := uint32(1)
		property.MinLength = &minimum
	} else {
		if bytes.Equal(bytes.TrimSpace(arguments.Options), []byte("null")) {
			return "", errors.New("`options` must be an array")
		}
		options, err := decodeQuestionOptions(arguments.Options)
		if err != nil {
			return "", err
		}
		if len(options) == 0 {
			return "", errors.New("`options` must contain at least one choice")
		}
		seen := make(map[string]struct{}, len(options))
		property.OneOf = make([]acp.ElicitationEnumOption, len(options))
		for index, option := range options {
			label := strings.TrimSpace(option.Label)
			if label == "" {
				return "", fmt.Errorf("options item %d: label must not be blank", index+1)
			}
			if _, duplicate := seen[label]; duplicate {
				return "", fmt.Errorf("options item %d: duplicate label %q", index+1, label)
			}
			seen[label] = struct{}{}
			property.OneOf[index] = acp.ElicitationEnumOption{
				Const: label, Title: label, Description: strings.TrimSpace(option.Description),
			}
		}
	}
	if arguments.Default != nil {
		value := strings.TrimSpace(*arguments.Default)
		if value == "" {
			return "", errors.New("`default` must not be blank")
		}
		if len(property.OneOf) > 0 {
			matched := false
			for _, option := range property.OneOf {
				matched = matched || option.Const == value
			}
			if !matched {
				return "", errors.New("`default` must match an option label")
			}
		}
		property.Default = &value
	}

	request := acp.CreateElicitationRequest{
		SessionID:  invocation.SessionID,
		ToolCallID: invocation.CallID,
		Mode:       acp.ElicitationModeForm,
		Message:    question,
		RequestedSchema: acp.ElicitationSchema{
			Type:       "object",
			Properties: map[string]acp.ElicitationStringProperty{"answer": property},
			Required:   []string{"answer"},
		},
	}
	if err := request.Validate(); err != nil {
		return "", fmt.Errorf("build elicitation: %w", err)
	}
	if invocation.AskQuestion == nil {
		return "", errors.New("form elicitation is unavailable")
	}
	response, err := invocation.AskQuestion(ctx, request)
	if err != nil {
		return "", err
	}
	answer, err := response.Validate(request)
	if err != nil {
		return "", err
	}
	result := questionResult{Outcome: "accepted", Answer: answer}
	switch response.Action {
	case acp.ElicitationActionDecline:
		result = questionResult{Outcome: "declined"}
	case acp.ElicitationActionCancel:
		result = questionResult{Outcome: "cancelled"}
	}
	data, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	return string(data), nil
}

func decodeQuestionOptions(raw json.RawMessage) ([]questionOption, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var options []questionOption
	if err := decoder.Decode(&options); err != nil {
		return nil, fmt.Errorf("`options` must be an array of labeled choices: %s", trimJSONError(err.Error()))
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("`options` must be one JSON array")
	}
	return options, nil
}
