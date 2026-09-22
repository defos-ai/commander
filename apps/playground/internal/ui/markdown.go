package ui

import (
	"strings"

	"github.com/charmbracelet/glamour"
	style "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func (m *Model) renderMarkdown(text string) string {
	if m.markdown == nil || m.markdownWidth != m.viewport.Width {
		// Set the style and profile explicitly: auto styles query the terminal,
		// which can interfere with Bubble Tea's input reader.
		palette := styles.DarkStyleConfig
		zero, inset, green := uint(0), uint(2), ReticleGreen
		palette.Document.Margin = &zero
		palette.Document.Color = nil
		palette.Heading.Color = &green
		palette.H1, palette.H2, palette.H3 = style.StyleBlock{}, style.StyleBlock{}, style.StyleBlock{}
		palette.H4, palette.H5, palette.H6 = style.StyleBlock{}, style.StyleBlock{}, style.StyleBlock{}
		palette.Link.Color, palette.LinkText.Color = &green, &green
		palette.Code.Color, palette.Code.BackgroundColor = &green, nil
		palette.CodeBlock = style.StyleCodeBlock{StyleBlock: style.StyleBlock{Indent: &inset}}
		profile := termenv.TrueColor
		if m.config.NoColor {
			profile = termenv.Ascii
		}
		renderer, err := glamour.NewTermRenderer(glamour.WithStyles(palette), glamour.WithColorProfile(profile), glamour.WithWordWrap(m.viewport.Width), glamour.WithTableWrap(true))
		if err != nil {
			return text
		}
		m.markdown, m.markdownWidth = renderer, m.viewport.Width
	}
	result, err := m.markdown.Render(text)
	if err != nil {
		return text
	}
	if m.config.NoColor {
		// Glamour's ASCII profile can still emit bold/underline attributes.
		return strings.Trim(clean(result), "\n")
	}
	return strings.Trim(styledText(result), "\n")
}

// Markdown entities can decode to terminal controls after input sanitization.
// Preserve only printable text, whitespace, and the renderer's SGR styles.
func styledText(text string) string {
	var result strings.Builder
	var state byte
	for len(text) > 0 {
		sequence, width, consumed, next := ansi.DecodeSequence(text, state, nil)
		text, state = text[consumed:], next
		if width > 0 || sequence == "\n" || sequence == "\t" || (strings.HasPrefix(sequence, "\x1b[") && strings.HasSuffix(sequence, "m")) {
			result.WriteString(sequence)
		}
	}
	return result.String()
}
