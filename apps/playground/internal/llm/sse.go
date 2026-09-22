package llm

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// stream reads complete SSE events, including multiline data and fragmented
// network writes. A per-line bound prevents an unbounded allocation.
func stream(reader io.Reader, consume func([]byte) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	var data []string
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		joined := strings.Join(data, "\n")
		data = nil
		return consume([]byte(joined))
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
		} else if value, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(value, " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}

func validateCalls(calls []Call) error {
	seen := map[string]bool{}
	for _, call := range calls {
		if call.ID == "" || call.Name == "" || seen[call.ID] {
			return errors.New("model returned an incomplete or duplicate tool call")
		}
		seen[call.ID] = true
	}
	return nil
}
