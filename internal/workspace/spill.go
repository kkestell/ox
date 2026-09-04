package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	InlineMaxLines       = 200
	InlineMaxBytes       = 50 * 1024
	CollectionLimitBytes = 10 * 1024 * 1024
)

type RenderResult struct {
	Content    string
	TotalLines int
	TotalBytes int
	Truncated  bool
	Spilled    string
}

type Capped struct {
	Lines     []string
	Bytes     int
	Truncated bool
}

func (c *Capped) Push(line string) bool {
	cost := len(line)
	if len(c.Lines) > 0 {
		cost++
	}
	if c.Bytes+cost > CollectionLimitBytes {
		c.Truncated = true
		return false
	}
	c.Bytes += cost
	c.Lines = append(c.Lines, line)
	return true
}

func (c *Capped) Remaining() int {
	return CollectionLimitBytes - c.Bytes
}

func RenderLines(
	dir string,
	label string,
	callID string,
	lines []string,
	capTruncated bool,
) (RenderResult, error) {
	totalLines := len(lines)
	if totalLines == 0 {
		content := "no matches"
		if capTruncated {
			content = fmt.Sprintf(
				"[truncated: collection cap of %d bytes reached]",
				CollectionLimitBytes,
			)
		}
		return RenderResult{Content: content, Truncated: capTruncated}, nil
	}

	totalBytes := len(strings.Join(lines, "\n"))
	if totalLines <= InlineMaxLines && totalBytes <= InlineMaxBytes {
		content := strings.Join(lines, "\n")
		if capTruncated {
			content += fmt.Sprintf(
				"\n[truncated: collection cap of %d bytes reached]",
				CollectionLimitBytes,
			)
		}
		return RenderResult{
			Content:    content,
			TotalLines: totalLines,
			TotalBytes: totalBytes,
			Truncated:  capTruncated,
		}, nil
	}

	spillPath, err := writeLines(dir, label, callID, lines)
	if err != nil {
		return RenderResult{}, err
	}
	capNote := ""
	if capTruncated {
		capNote = fmt.Sprintf(", collection capped at %d bytes", CollectionLimitBytes)
	}
	return RenderResult{
		Content:    SpilledPreview(lines, totalLines, totalBytes, spillPath, capNote),
		TotalLines: totalLines,
		TotalBytes: totalBytes,
		Truncated:  capTruncated,
		Spilled:    spillPath,
	}, nil
}

func SpilledPreview(
	lines []string,
	totalLines int,
	totalBytes int,
	spillPath string,
	capNote string,
) string {
	previewCount := 0
	partialFirst := false
	var content strings.Builder
	footer := spillFooter(InlineMaxLines, totalLines, totalBytes, spillPath, capNote, false)
	partialFooter := spillFooter(0, totalLines, totalBytes, spillPath, capNote, true)
	if len(partialFooter) > len(footer) {
		footer = partialFooter
	}
	contentBudget := max(0, InlineMaxBytes-len(footer)-1)
	for _, line := range lines[:min(InlineMaxLines, len(lines))] {
		cost := len(line)
		if previewCount > 0 {
			cost++
		}
		if content.Len()+cost > contentBudget {
			if previewCount == 0 && contentBudget > 0 {
				content.WriteString(validUTF8Prefix(line, contentBudget))
				partialFirst = true
			}
			break
		}
		if previewCount > 0 {
			content.WriteByte('\n')
		}
		content.WriteString(line)
		previewCount++
	}
	content.WriteByte('\n')
	content.WriteString(spillFooter(
		previewCount,
		totalLines,
		totalBytes,
		spillPath,
		capNote,
		partialFirst,
	))
	return content.String()
}

func spillFooter(
	previewLines int,
	totalLines int,
	totalBytes int,
	spillPath string,
	capNote string,
	partialFirst bool,
) string {
	if partialFirst {
		return fmt.Sprintf(
			"[showing a prefix of the first of %d lines, %d bytes, full output at %s%s]",
			totalLines,
			totalBytes,
			spillPath,
			capNote,
		)
	}
	return fmt.Sprintf(
		"[showing first %d of %d lines, %d bytes, full output at %s%s]",
		previewLines,
		totalLines,
		totalBytes,
		spillPath,
		capNote,
	)
}

func validUTF8Prefix(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	for limit > 0 && value[limit]&0xc0 == 0x80 {
		limit--
	}
	return value[:limit]
}

func writeLines(dir, label, callID string, lines []string) (string, error) {
	file, spillPath, err := openSpillFile(dir, label, callID)
	if err != nil {
		return "", err
	}
	data := []byte(strings.Join(lines, "\n"))
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(spillPath)
		return "", fmt.Errorf("failed to write spill file %s: %w", spillPath, err)
	}
	if err := finishSpillFile(file, spillPath); err != nil {
		_ = os.Remove(spillPath)
		return "", err
	}
	return spillPath, nil
}

func openSpillFile(dir, label, callID string) (*os.File, string, error) {
	createdDir := false
	if err := os.Mkdir(dir, 0o700); err != nil {
		if !os.IsExist(err) {
			return nil, "", fmt.Errorf("failed to create spill directory %s: %w", dir, err)
		}
	} else {
		createdDir = true
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, "", fmt.Errorf("failed to secure spill directory %s: %w", dir, err)
	}
	if createdDir {
		if err := syncDir(filepath.Dir(dir)); err != nil {
			return nil, "", fmt.Errorf("failed to sync spill parent directory: %w", err)
		}
	}
	spillPath := filepath.Join(dir, label+"-"+sanitizeCallID(callID)+".out")
	file, err := os.OpenFile(spillPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create spill file %s: %w", spillPath, err)
	}
	return file, spillPath, nil
}

func finishSpillFile(file *os.File, spillPath string) error {
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("failed to sync spill file %s: %w", spillPath, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close spill file %s: %w", spillPath, err)
	}
	dir := filepath.Dir(spillPath)
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("failed to sync spill directory %s: %w", dir, err)
	}
	return nil
}

func sanitizeCallID(callID string) string {
	valid := callID != "" && len(callID) <= 64
	for _, value := range callID {
		if !isSafeNameRune(value) {
			valid = false
			break
		}
	}
	if valid {
		return callID
	}

	var prefix strings.Builder
	for _, value := range callID {
		if prefix.Len() >= 48 {
			break
		}
		if isSafeNameRune(value) {
			prefix.WriteRune(value)
		} else {
			prefix.WriteByte('_')
		}
	}
	if prefix.Len() == 0 {
		prefix.WriteString("call")
	}
	sum := sha256.Sum256([]byte(callID))
	return prefix.String() + "-" + hex.EncodeToString(sum[:6])
}

func isSafeNameRune(value rune) bool {
	return value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' ||
		value == '_' ||
		value == '-'
}
