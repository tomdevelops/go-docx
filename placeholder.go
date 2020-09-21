package docx

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	OpenDelimiter  rune = '{'
	CloseDelimiter rune = '}'
)

var (
	OpenDelimiterRegex = regexp.MustCompile(string(OpenDelimiter))
	CloseDelimiterRegex = regexp.MustCompile(string(CloseDelimiter))
)

// PlaceholderMap is the type used to map the placeholder keys (without delimiters) to the replacement values
type PlaceholderMap map[string]interface{}

type Placeholder struct {
	Fragments []*PlaceholderFragment
}

// Text assembles the placeholder fragments using the given docBytes and returns the full placeholder literal.
func (p Placeholder) Text(docBytes []byte) string {
	str := ""
	for _, fragment := range p.Fragments {
		s := fragment.Run.Text.StartTag.End
		t := docBytes[s+fragment.Position.Start : s+fragment.Position.End]
		str += string(t)
	}
	return str
}

// StartPos returns the absolute start position of the placeholder.
func (p Placeholder) StartPos() int64 {
	return p.Fragments[0].Run.Text.StartTag.End + p.Fragments[0].Position.Start
}

// EndPos returns the absolute end position of the placeholder.
func (p Placeholder) EndPos() int64 {
	end := len(p.Fragments) -1
	return p.Fragments[end].Run.Text.StartTag.End + p.Fragments[end].Position.End
}

// ParsePlaceholders will, given the document run positions and the bytes, parse out all placeholders including
// their fragments.
func ParsePlaceholders(runs DocumentRuns, docBytes []byte) (placeholders []*Placeholder) {
	// tmp vars used to preserve state across iterations
	unclosedPlaceholder := new(Placeholder)
	hasOpenPlaceholder := false

	for _, run := range runs.WithText() {
		runText := run.GetText(docBytes)

		openDelimPositions := OpenDelimiterRegex.FindAllStringIndex(runText, -1)
		closeDelimPositions := CloseDelimiterRegex.FindAllStringIndex(runText, -1)

		// FindAllStringIndex returns a [][]int whereas the nested []int has only 2 keys (0 and 1)
		// We're only interested in the first key as that one indicates the position of the delimiter
		delimPositions := func(positions [][]int) []int {
			var pos []int
			for _, position := range positions {
				pos = append(pos, position[0])
			}
			return pos
		}

		// index all delimiters
		openPos := delimPositions(openDelimPositions)
		closePos := delimPositions(closeDelimPositions)

		// simple case: only full placeholders inside the run
		if (len(openPos) == len(closePos)) && len(openPos) != 0 {
			placeholders = append(placeholders, assembleFullPlaceholders(run, openPos, closePos)...)
			continue
		}

		// more open than closing delimiters
		// this can only mean that a placeholder is left unclosed after this run
		// For the length this must mean: (len(openPos) + 1) == len(closePos)
		// So we can be sure that the last position in openPos is the opening tag of the
		// unclosed placeholder.
		if len(openPos) > len(closePos) {
			// merge full placeholders in the run, leaving out the last openPos since
			// we know that the one is left over and must be handled separately below
			placeholders = append(placeholders, assembleFullPlaceholders(run, openPos[:len(openPos)-1], closePos)...)

			// add the unclosed part of the placeholder to a tmp placeholder var
			unclosedOpenPos := openPos[len(openPos)-1]
			fragment := &PlaceholderFragment{
				Position: Position{
					Start: int64(unclosedOpenPos),
					End:   int64(len(runText)),
				},
				Number: 0,
				Run:    run,
			}
			unclosedPlaceholder.Fragments = append(unclosedPlaceholder.Fragments, fragment)
			hasOpenPlaceholder = true
			continue
		}

		// more closing than opening delimiters
		// this can only mean that there must be an unclosed placeholder which
		// is closed in this run.
		if len(openPos) < len(closePos) {
			// merge full placeholders in the run, leaving out the last closePos since
			// we know that the one is left over and must be handled separately below
			placeholders = append(placeholders, assembleFullPlaceholders(run, openPos, closePos[:len(closePos) - 1])...)

			// there is only a closePos and no open pos
			if len(closePos) == 1 {
				fragment := &PlaceholderFragment{
					Position: Position{
						Start: 0,
						End:   int64(closePos[0])+1,
					},
					Number: len(unclosedPlaceholder.Fragments) + 1,
					Run:    run,
				}
				unclosedPlaceholder.Fragments = append(unclosedPlaceholder.Fragments, fragment)
				placeholders = append(placeholders, unclosedPlaceholder)
				unclosedPlaceholder = new(Placeholder)
				hasOpenPlaceholder = false
				continue
			}
			continue
		}

		// no placeholders at all. The run is only important if there
		// is an unclosed placeholder. That means that the full run belongs to the placeholder.
		if len(openPos) == 0 && len(closePos) == 0 {
			if hasOpenPlaceholder {
				fragment := &PlaceholderFragment{
					Position: Position{
						Start: 0,
						End:   int64(len(runText)),
					},
					Number: len(unclosedPlaceholder.Fragments) + 1,
					Run:    run,
				}
				unclosedPlaceholder.Fragments = append(unclosedPlaceholder.Fragments, fragment)
				continue
			}
		}
	}

	// in order to catch false positives, ensure that all placeholders have BOTH delimiters
	// if a placeholder only has one, remove it since it cannot be right.
	for i, placeholder := range placeholders {
		text := placeholder.Text(docBytes)
		if !strings.ContainsRune(text, OpenDelimiter) ||
			!strings.ContainsRune(text, CloseDelimiter) {
			placeholders = append(placeholders[:i], placeholders[i+1:]...)
		}
	}

	return placeholders
}

// assembleFullPlaceholders will extract all complete placeholders inside the run given a open and close position.
// The open and close positions are the positions of the Delimiters which must already be known at this point.
// openPos and closePos are expected to be symmetrical (e.g. same length).
// Example: openPos := []int{10,20,30}; closePos := []int{13, 23, 33}
// The n-th elements inside openPos and closePos must be matching delimiter positions.
func assembleFullPlaceholders(run *Run, openPos, closePos []int) (placeholders []*Placeholder){
	for i := 0; i < len(openPos); i++ {
		start := openPos[i]
		end := closePos[i] + 1 // +1 is required to include the closing delimiter in the text
		fragment := &PlaceholderFragment{
			Position: Position{
				Start: int64(start),
				End:   int64(end),
			},
			Number: 0,
			Run:    run,
		}
		p := &Placeholder{Fragments: []*PlaceholderFragment{fragment}}
		placeholders = append(placeholders, p)
	}
	return placeholders
}

// PlaceholderFragment is a part of a placeholder within the document.xml
// If the full placeholder is e.g. '{foo-bar}', the placeholder might be ripped
// apart according to the WordprocessingML spec. So it will most likely occur, that
// the placeholders are split into multiple fragments (e.g. '{foo' and '-bar}').
type PlaceholderFragment struct {
	Position Position // Position of the actual fragment within the run text. 0 == (Run.Text.OpenTag.End + 1)
	Number   int      // numbering fragments for ease of use
	Run      *Run
}

// StartPos returns the absolute start position of the fragment.
func (p PlaceholderFragment) StartPos() int64 {
	return p.Run.Text.StartTag.End + p.Position.Start
}

// EndPos returns the absolute end position of the fragment.
func (p PlaceholderFragment) EndPos() int64 {
	return p.Run.Text.StartTag.End + p.Position.End
}

// Text returns the actual text of the fragment given the source bytes.
// If the given byte slice is not large enough for the offsets, an empty string is returned.
func (p PlaceholderFragment) Text(docBytes []byte) string {
	if int64(len(docBytes)) < p.StartPos() ||
		int64(len(docBytes)) < p.EndPos() {
		return ""
	}
	return string(docBytes[p.StartPos():p.EndPos()])
}

// TextLength returns the actual length of the fragment given a byte source.
func (p PlaceholderFragment) TextLength(docBytes []byte) int64 {
	return int64(len(p.Text(docBytes)))
}

// String spits out the most important bits and pieces of a fragment.
func (p PlaceholderFragment) String(docBytes []byte) string {
	format := "fragment in run [%d:%d] '%s' - [%d:%d] '%s'; run-text [%d:%d] '%s' - [%d:%d] '%s'; positions: [%d:%d] '%s'"
	return fmt.Sprintf(format,
		p.Run.OpenTag.Start, p.Run.OpenTag.End, docBytes[p.Run.OpenTag.Start:p.Run.OpenTag.End],
		p.Run.CloseTag.Start, p.Run.CloseTag.End, docBytes[p.Run.CloseTag.Start:p.Run.CloseTag.End],
		p.Run.Text.StartTag.Start, p.Run.Text.StartTag.End, docBytes[p.Run.Text.StartTag.Start:p.Run.Text.StartTag.End],
		p.Run.Text.EndTag.Start, p.Run.Text.EndTag.End, docBytes[p.Run.Text.EndTag.Start:p.Run.Text.EndTag.End],
		p.Position.Start, p.Position.End, docBytes[p.Run.Text.StartTag.End+p.Position.Start:p.Run.Text.StartTag.End+p.Position.End])
}


// AddPlaceholderDelimiter will wrap the given string with OpenDelimiter and CloseDelimiter.
// If the given string is already a delimited placeholder, it is returned unchanged.
func AddPlaceholderDelimiter(s string) string {
	if IsDelimitedPlaceholder(s) {
		return s
	}
	return fmt.Sprintf("%c%s%c", OpenDelimiter, s, CloseDelimiter)
}

// RemovePlaceholderDelimiter removes OpenDelimiter and CloseDelimiter from the given text.
// If the given text is not a delimited placeholder, it is returned unchanged.
func RemovePlaceholderDelimiter(s string) string {
	if !IsDelimitedPlaceholder(s) {
		return s
	}
	return strings.Trim(s, fmt.Sprintf("%s%s", string(OpenDelimiter), string(CloseDelimiter)))
}

// IsDelimitedPlaceholder returns true if the given string is a delimited placeholder.
// It checks whether the first and last rune in the string is the OpenDelimiter and CloseDelimiter respectively.
// If the string is empty, false is returned.
func IsDelimitedPlaceholder(s string) bool {
	if len(s) < 1 {
		return false
	}
	first := s[0]
	last := s[len(s)-1]
	if rune(first) == OpenDelimiter && rune(last) == CloseDelimiter {
		return true
	}
	return false
}
