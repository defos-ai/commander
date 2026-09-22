package llm

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

const (
	maxStreamEventBytes    = 4 << 20
	maxStreamResponseBytes = 16 << 20
)

// Limits include ignored SSE fields and comments, not just model output.
func stream(reader io.Reader, consume func([]byte) error) error {
	limited := &io.LimitedReader{R: reader, N: maxStreamResponseBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), maxStreamEventBytes)
	var data []string
	eventBytes := 0
	eventLimit := errors.New("model stream event exceeds 4 MiB; no tool calls were executed")
	responseLimit := errors.New("model stream response exceeds 16 MiB; no tool calls were executed")
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		advance, token, err := bufio.ScanLines(data, atEOF)
		// Count consumed wire bytes, including CRLF delimiters ScanLines removes.
		eventBytes += advance
		if eventBytes > maxStreamEventBytes {
			return 0, nil, eventLimit
		}
		return advance, token, err
	})
	flush := func() error {
		eventBytes = 0
		if len(data) == 0 {
			return nil
		}
		joined := strings.Join(data, "\n")
		data = nil
		return consume([]byte(joined))
	}
	for scanner.Scan() {
		if limited.N == 0 {
			return responseLimit
		}
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
		} else if value, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(value, " "))
		}
	}
	if limited.N == 0 {
		return responseLimit
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return eventLimit
		}
		return err
	}
	return flush()
}
