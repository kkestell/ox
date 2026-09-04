package workspace

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	streamHeadBytes    = InlineMaxBytes / 2
	streamTailBytes    = InlineMaxBytes - streamHeadBytes - 2048
	streamHeadLines    = InlineMaxLines / 2
	streamTailLines    = InlineMaxLines - streamHeadLines
	streamEmitInterval = 100 * time.Millisecond
	streamEmitLines    = 5
	streamLineBytes    = 32 * 1024
)

// StreamRecorder bounds a process output stream while preserving overflow in
// a spill file. It is safe for concurrent writes.
type StreamRecorder struct {
	mu     sync.Mutex
	dir    string
	label  string
	callID string
	emit   func(string)

	carry    [utf8.UTFMax - 1]byte
	carryLen int

	inline   []byte
	head     []byte
	headFull bool
	tail     []byte
	spilled  bool
	file     *os.File
	path     string
	err      error
	closed   bool

	totalBytes int
	lines      streamLineCounter

	lineCarry []byte
	pending   []string
	lastEmit  time.Time
}

// NewStreamRecorder returns a recorder that spills lazily into dir.
func NewStreamRecorder(dir, label, callID string, emit func(string)) *StreamRecorder {
	return &StreamRecorder{
		dir:    dir,
		label:  label,
		callID: callID,
		emit:   emit,
	}
}

// Write records p. Spill failures are deferred to Finish so a child cannot
// deadlock because its output pipe stopped being drained.
func (r *StreamRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record(r.sanitize(p))
	return len(p), nil
}

func (r *StreamRecorder) record(data []byte) {
	if len(data) == 0 {
		return
	}
	r.totalBytes += len(data)
	r.lines.Write(data)
	r.emitLines(data)

	if r.spilled {
		r.capturePreview(data)
		r.writeSpill(data)
		return
	}
	if len(r.inline)+len(data) <= InlineMaxBytes &&
		r.lines.Count() <= InlineMaxLines {
		r.inline = append(r.inline, data...)
		return
	}

	r.spilled = true
	prefix := r.inline
	r.inline = nil
	r.capturePreview(prefix)
	r.capturePreview(data)
	file, path, err := openSpillFile(r.dir, r.label, r.callID)
	if err != nil {
		r.err = err
		return
	}
	r.file = file
	r.path = path
	r.writeSpill(prefix)
	r.writeSpill(data)
}

func (r *StreamRecorder) writeSpill(data []byte) {
	if r.file == nil || r.err != nil || len(data) == 0 {
		return
	}
	if _, err := r.file.Write(data); err != nil {
		r.err = fmt.Errorf("failed to write spill file %s: %w", r.path, err)
	}
}

func (r *StreamRecorder) capturePreview(data []byte) {
	if !r.headFull {
		room := streamHeadBytes - len(r.head)
		r.head = append(r.head, data[:min(room, len(data))]...)
		lineOverflow := textLineCount(r.head) > streamHeadLines
		r.headFull = len(data) >= room || lineOverflow
		for len(r.head) > 0 && !utf8.Valid(r.head) {
			r.head = r.head[:len(r.head)-1]
		}
		r.head = firstLines(r.head, streamHeadLines)
	}
	r.tail = append(r.tail, data...)
	if len(r.tail) > streamTailBytes {
		r.tail = validUTF8Suffix(r.tail[len(r.tail)-streamTailBytes:])
	}
	r.tail = lastLines(r.tail, streamTailLines)
}

func (r *StreamRecorder) emitLines(data []byte) {
	if r.emit == nil {
		return
	}
	r.lineCarry = append(r.lineCarry, data...)
	start := 0
	for index := 0; index < len(r.lineCarry); index++ {
		switch r.lineCarry[index] {
		case '\n':
			r.pushLine(r.lineCarry[start:index])
			start = index + 1
		case '\r':
			if index == len(r.lineCarry)-1 {
				continue
			}
			r.pushLine(r.lineCarry[start:index])
			if r.lineCarry[index+1] == '\n' {
				index++
			}
			start = index + 1
		}
	}
	r.lineCarry = append(r.lineCarry[:0], r.lineCarry[start:]...)
	if len(r.lineCarry) > streamLineBytes {
		r.lineCarry = validUTF8Suffix(r.lineCarry[len(r.lineCarry)-streamLineBytes:])
	}
	if time.Since(r.lastEmit) >= streamEmitInterval {
		r.flush()
	}
}

func (r *StreamRecorder) pushLine(line []byte) {
	r.pending = append(r.pending, string(line))
	if len(r.pending) > streamEmitLines {
		r.pending = r.pending[len(r.pending)-streamEmitLines:]
	}
}

func (r *StreamRecorder) flush() {
	if len(r.pending) == 0 {
		return
	}
	r.emit(strings.Join(r.pending, "\n") + "\n")
	r.pending = nil
	r.lastEmit = time.Now()
}

func (r *StreamRecorder) sanitize(data []byte) []byte {
	combined := append(append([]byte(nil), r.carry[:r.carryLen]...), data...)
	r.carryLen = 0
	keep := len(combined) - incompleteUTF8Tail(combined)
	r.carryLen = copy(r.carry[:], combined[keep:])
	return []byte(strings.ToValidUTF8(string(combined[:keep]), "�"))
}

// Finish closes the recorder and returns either the complete inline stream or
// a bounded head-and-tail preview pointing to the faithful spill file.
func (r *StreamRecorder) Finish() (RenderResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.carryLen > 0 {
		r.carryLen = 0
		r.record([]byte("�"))
	}
	if len(r.lineCarry) > 0 && r.lineCarry[len(r.lineCarry)-1] == '\r' {
		r.pushLine(r.lineCarry[:len(r.lineCarry)-1])
		r.lineCarry = nil
	}
	r.flush()
	r.lines.Finish()
	if r.file != nil && !r.closed {
		r.closed = true
		if err := finishSpillFile(r.file, r.path); err != nil && r.err == nil {
			r.err = err
		}
	}
	if r.err != nil {
		return RenderResult{}, r.err
	}
	if !r.spilled {
		return RenderResult{
			Content:    string(r.inline),
			TotalLines: r.lines.Count(),
			TotalBytes: r.totalBytes,
		}, nil
	}

	tail := r.tail
	tailOffset := r.totalBytes - len(tail)
	if overlap := len(r.head) - tailOffset; overlap > 0 {
		tail = validUTF8Suffix(tail[min(overlap, len(tail)):])
	}
	elidedBytes := max(0, r.totalBytes-len(r.head)-len(tail))
	elidedLines := max(
		0,
		r.lines.Count()-textLineCount(r.head)-textLineCount(tail),
	)
	var content strings.Builder
	content.Write(r.head)
	if len(r.head) > 0 && r.head[len(r.head)-1] != '\n' {
		content.WriteByte('\n')
	}
	fmt.Fprintf(
		&content,
		"[... elided %d lines, %d bytes ...]\n",
		elidedLines,
		elidedBytes,
	)
	content.Write(tail)
	if len(tail) > 0 && tail[len(tail)-1] != '\n' {
		content.WriteByte('\n')
	}
	fmt.Fprintf(
		&content,
		"[showing head and tail of %d lines, %d bytes, full output at %s]",
		r.lines.Count(),
		r.totalBytes,
		r.path,
	)
	return RenderResult{
		Content:    content.String(),
		TotalLines: r.lines.Count(),
		TotalBytes: r.totalBytes,
		Spilled:    r.path,
	}, nil
}

// Close releases a recorder whose result will not be rendered.
func (r *StreamRecorder) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file != nil && !r.closed {
		r.closed = true
		if err := r.file.Close(); err != nil && r.err == nil {
			r.err = fmt.Errorf("failed to close spill file %s: %w", r.path, err)
		}
	}
}

type streamLineCounter struct {
	count     int
	open      bool
	pendingCR bool
}

func (c *streamLineCounter) Write(data []byte) {
	for _, value := range data {
		if c.pendingCR {
			c.count++
			c.pendingCR = false
			c.open = false
			if value == '\n' {
				continue
			}
		}
		switch value {
		case '\r':
			c.pendingCR = true
		case '\n':
			c.count++
			c.open = false
		default:
			c.open = true
		}
	}
}

func (c *streamLineCounter) Finish() {
	if c.pendingCR {
		c.count++
		c.pendingCR = false
		c.open = false
	}
}

func (c *streamLineCounter) Count() int {
	if c.open || c.pendingCR {
		return c.count + 1
	}
	return c.count
}

func textLineCount(data []byte) int {
	var counter streamLineCounter
	counter.Write(data)
	counter.Finish()
	return counter.Count()
}

func firstLines(data []byte, limit int) []byte {
	if textLineCount(data) <= limit {
		return data
	}
	return data[:lineBoundary(data, limit)]
}

func lastLines(data []byte, limit int) []byte {
	if textLineCount(data) <= limit {
		return data
	}
	boundaries := lineBoundaries(data)
	start := boundaries[len(boundaries)-limit-1]
	return validUTF8Suffix(data[start:])
}

func lineBoundary(data []byte, lines int) int {
	boundaries := lineBoundaries(data)
	if len(boundaries) <= lines {
		return len(data)
	}
	return boundaries[lines]
}

func lineBoundaries(data []byte) []int {
	boundaries := []int{0}
	for index := 0; index < len(data); index++ {
		switch data[index] {
		case '\n':
			boundaries = append(boundaries, index+1)
		case '\r':
			if index+1 < len(data) && data[index+1] == '\n' {
				index++
			}
			boundaries = append(boundaries, index+1)
		}
	}
	if boundaries[len(boundaries)-1] != len(data) {
		boundaries = append(boundaries, len(data))
	}
	return boundaries
}

func validUTF8Suffix(data []byte) []byte {
	for len(data) > 0 && !utf8.Valid(data) {
		data = data[1:]
	}
	return data
}

func incompleteUTF8Tail(data []byte) int {
	start := max(0, len(data)-(utf8.UTFMax-1))
	for index := start; index < len(data); index++ {
		if utf8.RuneStart(data[index]) && !utf8.FullRune(data[index:]) {
			return len(data) - index
		}
	}
	return 0
}
