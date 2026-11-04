package knowledge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// ErrInvalidText rejects Markdown that is not valid UTF-8, since code point
// offsets would be undefined.
var ErrInvalidText = errors.New("knowledge: text is not valid UTF-8")

// Prepare turns Markdown into the blocks the extractor sees, parsed with
// domain.Markdown, in document order.
//
// Policy (PreprocessVersion "prepare-v1"):
//   - Blocks are headings, paragraphs, list items (each item's own text;
//     nested items are separate blocks) and table cells (one block per
//     non-empty cell). Paragraphs in block quotes are paragraphs.
//   - Not extracted: YAML (---) and TOML (+++) frontmatter at the start,
//     fenced and indented code blocks, HTML blocks, inline code spans, inline
//     raw HTML tags, images (alt text included), autolinks, link
//     destinations and task checkboxes. Link text is extracted.
//   - Kept text is verbatim source, including backslash escapes and entity
//     references. Removed inline content leaves nothing in Text and a gap
//     between spans in the source, so quotes around it stay exact. Emphasis
//     markers are removed the same way.
//   - A soft or hard line break inside a block becomes one "\n" in Text that
//     no span covers. Leading and trailing white space is trimmed.
//   - Block IDs are "b<ordinal>-<8 hex of SHA-256 of kind and text>": equal
//     Markdown gives equal IDs.
func Prepare(markdown string) ([]Block, error) {
	if !utf8.ValidString(markdown) {
		return nil, ErrInvalidText
	}
	src := []byte(markdown)
	// Blank the frontmatter, keeping byte offsets, so it parses as blank lines.
	if end := frontmatterEnd(src); end > 0 {
		src = bytes.Clone(src)
		for i := 0; i < end; i++ {
			if src[i] != '\n' && src[i] != '\r' {
				src[i] = ' '
			}
		}
	}
	runeIndex := runeOffsets([]byte(markdown))
	doc := domain.Markdown.Parser().Parse(text.NewReader(src))
	p := preparer{src: src, runes: runeIndex}
	err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.(type) {
		case *ast.FencedCodeBlock, *ast.CodeBlock, *ast.HTMLBlock:
			return ast.WalkSkipChildren, nil
		case *ast.Heading:
			p.block(n, BlockHeading)
			return ast.WalkSkipChildren, nil
		case *east.TableCell:
			p.block(n, BlockTableCell)
			return ast.WalkSkipChildren, nil
		case *ast.Paragraph, *ast.TextBlock:
			kind := BlockParagraph
			for parent := n.Parent(); parent != nil; parent = parent.Parent() {
				if _, ok := parent.(*ast.ListItem); ok {
					kind = BlockListItem
					break
				}
			}
			p.block(n, kind)
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return p.blocks, err
}

// frontmatterEnd returns the byte offset after a closing frontmatter
// delimiter line when src starts with an opening one, or 0.
func frontmatterEnd(src []byte) int {
	var delim string
	switch {
	case bytes.HasPrefix(src, []byte("---\n")) || bytes.HasPrefix(src, []byte("---\r\n")):
		delim = "---"
	case bytes.HasPrefix(src, []byte("+++\n")) || bytes.HasPrefix(src, []byte("+++\r\n")):
		delim = "+++"
	default:
		return 0
	}
	pos := bytes.IndexByte(src, '\n') + 1
	for pos < len(src) {
		end := bytes.IndexByte(src[pos:], '\n')
		next := len(src)
		if end >= 0 {
			next = pos + end + 1
		}
		line := strings.TrimRight(string(src[pos:next]), "\r\n")
		if line == delim || (delim == "---" && line == "...") {
			return next
		}
		pos = next
	}
	return 0
}

// runeOffsets maps each byte offset of a rune start (and len(src)) to its
// code point index.
func runeOffsets(src []byte) []int {
	out := make([]int, len(src)+1)
	n := 0
	for i := 0; i < len(src); {
		out[i] = n
		_, size := utf8.DecodeRune(src[i:])
		for j := 1; j < size; j++ {
			out[i+j] = n
		}
		i += size
		n++
	}
	out[len(src)] = n
	return out
}

type preparer struct {
	src    []byte
	runes  []int
	blocks []Block

	text      []rune
	spans     []Span
	lineBreak bool
}

func (p *preparer) block(n ast.Node, kind string) {
	p.text, p.spans, p.lineBreak = p.text[:0], nil, false
	p.inline(n)
	text, spans := trimBlock(p.text, p.spans)
	if len(spans) == 0 {
		return
	}
	value := string(text)
	sum := sha256.Sum256([]byte(kind + "\x00" + value))
	p.blocks = append(p.blocks, Block{
		ID:   "b" + strconv.Itoa(len(p.blocks)+1) + "-" + hex.EncodeToString(sum[:4]),
		Kind: kind,
		Text: value,
		Map:  spans,
	})
}

func (p *preparer) inline(n ast.Node) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch c := c.(type) {
		case *ast.Text:
			p.verbatim(c.Segment.Start, c.Segment.Stop)
			if c.SoftLineBreak() || c.HardLineBreak() {
				p.lineBreak = true
			}
		case *ast.String:
			// Not produced by the parser options in use; nothing to map.
		case *ast.CodeSpan, *ast.RawHTML, *ast.Image, *ast.AutoLink, *east.TaskCheckBox:
		default:
			p.inline(c)
		}
	}
}

// verbatim appends source bytes [start, stop) as one span, after a pending
// line break, and merges it with the previous span when both are contiguous.
func (p *preparer) verbatim(start, stop int) {
	if start >= stop {
		return
	}
	if p.lineBreak && len(p.text) > 0 {
		p.text = append(p.text, '\n')
	}
	p.lineBreak = false
	piece := []rune(string(p.src[start:stop]))
	s := Span{Start: len(p.text), End: len(p.text) + len(piece), SrcStart: p.runes[start], SrcEnd: p.runes[stop]}
	p.text = append(p.text, piece...)
	if last := len(p.spans) - 1; last >= 0 && p.spans[last].End == s.Start && p.spans[last].SrcEnd == s.SrcStart {
		p.spans[last].End, p.spans[last].SrcEnd = s.End, s.SrcEnd
		return
	}
	p.spans = append(p.spans, s)
}

// trimBlock removes leading and trailing white space and clips the spans.
func trimBlock(text []rune, spans []Span) ([]rune, []Span) {
	lo, hi := 0, len(text)
	for lo < hi && unicode.IsSpace(text[lo]) {
		lo++
	}
	for hi > lo && unicode.IsSpace(text[hi-1]) {
		hi--
	}
	out := make([]Span, 0, len(spans))
	for _, s := range spans {
		start, end := max(s.Start, lo), min(s.End, hi)
		if start >= end {
			continue
		}
		out = append(out, Span{
			Start: start - lo, End: end - lo,
			SrcStart: s.SrcStart + start - s.Start, SrcEnd: s.SrcEnd - (s.End - end),
		})
	}
	return text[lo:hi], out
}

// SourceRange maps the block range [start, end) to the Markdown. exact
// reports that the source range holds exactly the same code points. When the
// range crosses removed markup, inline code or an inserted line break, the
// result encloses the quote and exact is false. Invalid or empty ranges and
// ranges with no source text return (-1, -1, false).
func SourceRange(b Block, start, end int) (srcStart, srcEnd int, exact bool) {
	if start < 0 || end <= start || end > utf8.RuneCountInString(b.Text) {
		return -1, -1, false
	}
	first, last := -1, -1
	for i, s := range b.Map {
		if s.End > start && s.Start < end {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return -1, -1, false
	}
	f, l := b.Map[first], b.Map[last]
	srcStart, srcEnd = f.SrcStart+max(start-f.Start, 0), l.SrcEnd-max(l.End-end, 0)
	return srcStart, srcEnd, first == last && f.Start <= start && end <= f.End
}
