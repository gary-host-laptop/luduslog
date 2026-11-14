package markdown

import (
	"strings"
	"testing"
)

func TestRenderEscapesHTML(t *testing.T) {
	out := Render(`<script>alert("x")</script> **bold**`)
	if strings.Contains(out, "<script>") {
		t.Fatalf("script tag survived: %s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Fatalf("expected escaped script tag: %s", out)
	}
	if !strings.Contains(out, "<strong>bold</strong>") {
		t.Fatalf("expected bold markup: %s", out)
	}
}

func TestRenderRejectsJavascriptLinks(t *testing.T) {
	out := Render(`[click](javascript:alert(1))`)
	if strings.Contains(out, `<a href="javascript:`) {
		t.Fatalf("javascript URL became a link: %s", out)
	}
	if !strings.Contains(out, "javascript:") {
		t.Fatalf("unsafe link should stay visible as text: %s", out)
	}
}

func TestPlainTextStripsMarkup(t *testing.T) {
	got := PlainText("# Title\n\nSome **bold** text.", 10)
	if strings.Contains(got, "#") || strings.Contains(got, "**") {
		t.Fatalf("markup remained: %q", got)
	}
	if len([]rune(got)) > 11 {
		t.Fatalf("plain text was not truncated: %q", got)
	}
}
