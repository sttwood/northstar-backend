package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCommunityRichTextCanonicalAndLegacy(t *testing.T) {
	raw := json.RawMessage(`{"type":"doc","content":[{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Setup","marks":[{"type":"bold"}]}]},{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"Install Go"}]}]}]},{"type":"paragraph","content":[{"type":"text","text":"Docs","marks":[{"type":"link","attrs":{"href":"https://example.test/docs","target":"_blank","rel":"noopener noreferrer nofollow","class":null}}]}]},{"type":"codeBlock","attrs":{"language":null},"content":[{"type":"text","text":"go run ./cmd/server"}]}]}`)
	text, doc, err := CommunityRichText(raw, "forged searchable content")
	if err != nil || doc == nil || text != "Setup\nInstall Go\nDocs\ngo run ./cmd/server" {
		t.Fatalf("canonical rich body: %q %v", text, err)
	}
	linked := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Docs","marks":[{"type":"link","attrs":{"href":"https://example.test","target":"_self","rel":"opener","class":"hidden","title":null}}]}]}]}`)
	_, canonical, err := CommunityRichText(linked, "")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(canonical)
	if strings.Contains(string(encoded), "hidden") || strings.Contains(string(encoded), "_self") || !strings.Contains(string(encoded), "noopener noreferrer nofollow") {
		t.Fatal("link attributes were not normalized")
	}
	text, doc, err = CommunityRichText(nil, " Legacy <script>literal</script>\nplain text ")
	if err != nil || doc != nil || text != "Legacy <script>literal</script>\nplain text" {
		t.Fatal("legacy body changed")
	}
}
func TestCommunityRichTextRejectsUnsafeOrUnboundedDocuments(t *testing.T) {
	for name, raw := range map[string]string{
		"script node":         `{"type":"doc","content":[{"type":"script","text":"alert(1)"}]}`,
		"unsafe link":         `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"click","marks":[{"type":"link","attrs":{"href":"javascript:alert(1)"}}]}]}]}`,
		"credential link":     `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"click","marks":[{"type":"link","attrs":{"href":"https://secret@example.test"}}]}]}]}`,
		"injected attributes": `{"type":"doc","content":[{"type":"paragraph","attrs":{"onclick":"alert(1)"}}]}`,
		"unknown properties":  `{"type":"doc","content":[{"type":"paragraph","html":"<img onerror=alert(1)>"}]}`,
		"invalid children":    `{"type":"doc","content":[{"type":"text","text":"outside paragraph"}]}`,
		"empty list":          `{"type":"doc","content":[{"type":"bulletList"}]}`,
		"unknown mark":        `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"x","marks":[{"type":"style"}]}]}]}`,
		"nested":              `{"type":"doc","content":[` + strings.Repeat(`{"type":"blockquote","content":[`, 20) + `{"type":"paragraph"}` + strings.Repeat(`]}`, 20) + `]}`,
		"oversize text":       `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"` + strings.Repeat("x", 12001) + `"}]}]}`,
		"oversize nodes":      `{"type":"doc","content":[` + strings.Repeat(`{"type":"paragraph"},`, 2500) + `{"type":"paragraph"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := CommunityRichText(json.RawMessage(raw), "safe-looking fallback"); err == nil {
				t.Fatal("invalid rich document accepted")
			}
		})
	}
}
