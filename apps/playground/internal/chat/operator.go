package chat

import (
	"fmt"

	bridge "commander/apps/playground/internal/mcp"
)

func questionEvent(question bridge.Question) Event {
	text := question.Text
	for i, option := range question.Options {
		text += fmt.Sprintf("\n  [%d] %s", i+1, option)
	}
	text += "\n/answer " + question.ID + " <answer or option number>"
	return Event{Kind: "question", ID: question.ID, Text: text}
}
