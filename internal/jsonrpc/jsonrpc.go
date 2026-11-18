// Package jsonrpc defines JSON-RPC 2.0 messages, sent one per line. It
// validates request envelopes and builds responses and notifications; the
// methods themselves belong to the server.
package jsonrpc

import (
	"encoding/json"
	"errors"
)

const version = "2.0"

// Standard error codes.
const (
	ParseError     = -32700
	InvalidRequest = -32600
	MethodNotFound = -32601
	InvalidParams  = -32602
)

type Request struct {
	ID     json.RawMessage // empty when the request has no ID
	Method string
	Params json.RawMessage
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// Parse decodes one request. An invalid request returns the error response to
// send instead.
func Parse(line []byte) (Request, *Response) {
	var envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  *string         `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			return Request{}, Failure(nil, ParseError, "Parse error")
		}
		return Request{}, Failure(nil, InvalidRequest, "Invalid Request")
	}
	if envelope.JSONRPC != version || envelope.Method == nil || !validID(envelope.ID) ||
		(len(envelope.Params) != 0 && envelope.Params[0] != '{' && envelope.Params[0] != '[') {
		return Request{}, Failure(nil, InvalidRequest, "Invalid Request")
	}
	return Request{ID: envelope.ID, Method: *envelope.Method, Params: envelope.Params}, nil
}

func validID(id json.RawMessage) bool {
	if len(id) == 0 {
		return true
	}
	// Unmarshal has already checked the JSON syntax, so only the type matters.
	switch id[0] {
	case '{', '[', 't', 'f':
		return false
	default:
		return true
	}
}

func Success(id json.RawMessage, result any) *Response {
	return &Response{JSONRPC: version, ID: id, Result: result}
}

func Failure(id json.RawMessage, code int, message string) *Response {
	return &Response{JSONRPC: version, ID: id, Error: &Error{Code: code, Message: message}}
}

func Notify(method string, params any) Notification {
	return Notification{JSONRPC: version, Method: method, Params: params}
}
