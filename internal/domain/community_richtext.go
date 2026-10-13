package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Rich text is a bounded document, never arbitrary HTML. Keep this schema in sync
// with the community editor and its React renderer.
type RichNode struct {
	Type    string         `json:"type"`
	Text    string         `json:"text,omitempty"`
	Attrs   map[string]any `json:"attrs,omitempty"`
	Marks   []RichMark     `json:"marks,omitempty"`
	Content []RichNode     `json:"content,omitempty"`
}
type RichMark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

func CommunityRichText(raw json.RawMessage, plain string) (string, any, error) {
	invalid := errors.New("invalid community rich text")
	if len(raw) == 0 || string(raw) == "null" {
		return strings.TrimSpace(plain), nil, nil
	}
	if len(raw) > 256*1024 {
		return "", nil, invalid
	}
	var doc RichNode
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&doc) != nil || d.Decode(new(any)) != io.EOF || doc.Type != "doc" {
		return "", nil, invalid
	}
	count, images := 0, 0
	var visit func(RichNode, string, int) (string, error)
	visit = func(n RichNode, parent string, depth int) (string, error) {
		count++
		if count > 2500 || depth > 16 {
			return "", invalid
		}
		block := Contains([]string{"paragraph", "heading", "blockquote", "bulletList", "orderedList", "codeBlock", "horizontalRule", "image"}, n.Type)
		switch parent {
		case "":
			if n.Type != "doc" {
				return "", invalid
			}
		case "doc", "blockquote", "listItem":
			if !block {
				return "", invalid
			}
		case "bulletList", "orderedList":
			if n.Type != "listItem" {
				return "", invalid
			}
		case "paragraph", "heading":
			if n.Type != "text" && n.Type != "hardBreak" {
				return "", invalid
			}
		case "codeBlock":
			if n.Type != "text" || len(n.Marks) > 0 {
				return "", invalid
			}
		default:
			return "", invalid
		}
		if n.Type != "text" && (n.Text != "" || len(n.Marks) > 0) {
			return "", invalid
		}
		for key, value := range n.Attrs {
			switch {
			case n.Type == "image" && key == "fileId":
				x, ok := value.(string)
				if !ok || len(x) < 1 || len(x) > 100 || strings.ContainsAny(x, "/\\: \r\n") {
					return "", invalid
				}
			case n.Type == "image" && key == "alt":
				x, ok := value.(string)
				if !ok || utf8.RuneCountInString(x) > 300 {
					return "", invalid
				}
			case n.Type == "heading" && key == "level":
				if value != float64(2) && value != float64(3) {
					return "", invalid
				}
			case n.Type == "orderedList" && key == "start":
				x, ok := value.(float64)
				if !ok || x < 1 || x > 10000 || x != float64(int(x)) {
					return "", invalid
				}
			case n.Type == "codeBlock" && key == "language":
				if value != nil {
					x, ok := value.(string)
					if !ok || len(x) > 40 {
						return "", invalid
					}
				}
			default:
				return "", invalid
			}
		}
		if n.Type == "image" {
			images++
			if images > 20 || n.Attrs["fileId"] == nil || len(n.Content) > 0 {
				return "", invalid
			}
			alt, _ := n.Attrs["alt"].(string)
			if strings.TrimSpace(alt) == "" {
				alt = "[Image]"
			}
			return alt, nil
		}
		if n.Type == "heading" && n.Attrs["level"] == nil {
			return "", invalid
		}
		if (n.Type == "doc" || n.Type == "blockquote" || n.Type == "listItem" || n.Type == "bulletList" || n.Type == "orderedList") && len(n.Content) == 0 {
			return "", invalid
		}
		if n.Type == "listItem" && n.Content[0].Type != "paragraph" {
			return "", invalid
		}
		if n.Type == "text" {
			if n.Text == "" || len(n.Content) > 0 {
				return "", invalid
			}
			seen := map[string]bool{}
			for i, mark := range n.Marks {
				if seen[mark.Type] {
					return "", invalid
				}
				seen[mark.Type] = true
				if !Contains([]string{"bold", "italic", "underline", "strike", "code", "link"}, mark.Type) {
					return "", invalid
				}
				if mark.Type != "link" && len(mark.Attrs) > 0 {
					return "", invalid
				}
				if mark.Type == "link" {
					href, ok := mark.Attrs["href"].(string)
					u, err := url.Parse(href)
					if !ok || err != nil || len(href) > 2048 || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || strings.ContainsAny(href, "\r\n\t") {
						return "", invalid
					}
					for k, v := range mark.Attrs {
						if !Contains([]string{"href", "target", "rel", "class", "title"}, k) {
							return "", invalid
						}
						if v != nil {
							x, ok := v.(string)
							if !ok || len(x) > 2048 {
								return "", invalid
							}
						}
					}
					n.Marks[i].Attrs = map[string]any{"href": href, "target": "_blank", "rel": "noopener noreferrer nofollow", "class": nil, "title": mark.Attrs["title"]}
				}
			}
			return n.Text, nil
		}
		if n.Type == "hardBreak" || n.Type == "horizontalRule" {
			if len(n.Content) > 0 {
				return "", invalid
			}
			return "\n", nil
		}
		parts := []string{}
		for _, child := range n.Content {
			text, err := visit(child, n.Type, depth+1)
			if err != nil {
				return "", err
			}
			parts = append(parts, text)
		}
		separator := ""
		if n.Type == "doc" || n.Type == "blockquote" || n.Type == "listItem" || n.Type == "bulletList" || n.Type == "orderedList" {
			separator = "\n"
		}
		return strings.Join(parts, separator), nil
	}
	text, err := visit(doc, "", 0)
	if err != nil {
		return "", nil, err
	}
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) > 12000 {
		return "", nil, errors.New("community content must be between 1 and 12000 characters")
	}
	return text, doc, nil
}
