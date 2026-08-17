package openrouter

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

const maxSSEEventSize = 16 * 1024 * 1024

var errStreamEnded = errors.New("OpenRouter stream ended before [DONE]")

// readSSE calls onEvent with the payload of each server-sent event in reader.
// A payload spread over several data lines is joined with newlines, comment
// lines are skipped, and the stream stops at [DONE].
func readSSE(reader io.Reader, onEvent func([]byte) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxSSEEventSize)

	var data []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			done, err := emitSSEEvent(data, onEvent)
			data = data[:0]
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if line == "data" {
			data = append(data, "")
			continue
		}
		if strings.HasPrefix(line, "data:") {
			value := strings.TrimPrefix(line, "data:")
			value = strings.TrimPrefix(value, " ")
			data = append(data, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read OpenRouter stream: %w", err)
	}
	if len(data) > 0 {
		done, err := emitSSEEvent(data, onEvent)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
	return errStreamEnded
}

func emitSSEEvent(data []string, onEvent func([]byte) error) (bool, error) {
	if len(data) == 0 {
		return false, nil
	}
	joined := strings.Join(data, "\n")
	if joined == "[DONE]" {
		return true, nil
	}
	if err := onEvent([]byte(joined)); err != nil {
		return false, err
	}
	return false, nil
}
