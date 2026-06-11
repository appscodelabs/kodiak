// Package text provides markdown helpers, ported from bot/kodiak/text.py.
package text

import (
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	gtext "github.com/yuin/goldmark/text"
)

type span struct {
	start int
	stop  int
}

// htmlNodePositions returns the byte ranges of HTML nodes (blocks and inline
// raw HTML) within the markdown source.
func htmlNodePositions(source []byte) ([]span, error) {
	md := goldmark.New()
	doc := md.Parser().Parse(gtext.NewReader(source))

	var positions []span
	err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.HTMLBlock:
			start, stop := -1, -1
			for i := 0; i < v.Lines().Len(); i++ {
				seg := v.Lines().At(i)
				if start == -1 || seg.Start < start {
					start = seg.Start
				}
				if seg.Stop > stop {
					stop = seg.Stop
				}
			}
			if v.HasClosure() {
				seg := v.ClosureLine
				if start == -1 || seg.Start < start {
					start = seg.Start
				}
				if seg.Stop > stop {
					stop = seg.Stop
				}
			}
			if start != -1 {
				positions = append(positions, span{start, stop})
			}
		case *ast.RawHTML:
			start, stop := -1, -1
			for i := 0; i < v.Segments.Len(); i++ {
				seg := v.Segments.At(i)
				if start == -1 || seg.Start < start {
					start = seg.Start
				}
				if seg.Stop > stop {
					stop = seg.Stop
				}
			}
			if start != -1 {
				positions = append(positions, span{start, stop})
			}
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	return positions, nil
}

// commentPositions returns the byte ranges of HTML comments ("<!-- -->")
// within the given HTML snippet.
func commentPositions(html []byte) []span {
	var spans []span
	offset := 0
	for {
		rel := strings.Index(string(html[offset:]), "<!--")
		if rel < 0 {
			break
		}
		start := offset + rel
		endRel := strings.Index(string(html[start+4:]), "-->")
		if endRel < 0 {
			break
		}
		stop := start + 4 + endRel + 3
		spans = append(spans, span{start, stop})
		offset = stop
	}
	return spans
}

// StripHTMLCommentsFromMarkdown removes HTML comments from a markdown
// document:
//
//  1. parse the string into a markdown AST
//  2. find the HTML nodes
//  3. find comments in the HTML
//  4. slice the comments out of the original message
func StripHTMLCommentsFromMarkdown(rawMessage string) string {
	// Remove carriage returns so the markdown parser can process HTML
	// correctly. pulldown-cmark and remark-parse don't handle carriage
	// returns well either:
	// https://github.com/remarkjs/remark/issues/195#issuecomment-230760892
	stripped := strings.ReplaceAll(rawMessage, "\r", "")
	source := []byte(stripped)

	htmlPositions, err := htmlNodePositions(source)
	if err != nil {
		return stripped
	}

	// Collect comment spans in absolute byte offsets.
	var comments []span
	for _, hp := range htmlPositions {
		for _, cp := range commentPositions(source[hp.start:hp.stop]) {
			comments = append(comments, span{hp.start + cp.start, hp.start + cp.stop})
		}
	}

	// Remove comments from last to first so offsets remain valid.
	sort.Slice(comments, func(i, j int) bool { return comments[i].start > comments[j].start })
	result := source
	for _, c := range comments {
		result = append(result[:c.start:c.start], result[c.stop:]...)
	}
	return string(result)
}
