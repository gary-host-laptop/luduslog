// Package markdown renders the small, safe Markdown subset used by reviews,
// notes, list descriptions, bios and comments.
//
// Supported: **bold**, *italic*, _italic_, __underline__, ~~strike~~,
// `code`, [text](https://url), bare https urls, \* escapes, blank-line
// paragraphs, line breaks, >! spoiler blocks (rendered as <details>), and
// ||inline spoiler||. All input is HTML-escaped before any markup is applied,
// so the output is safe by construction and needs no external sanitizer.
package markdown

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode"
)

var (
	reURL         = regexp.MustCompile(`https?://[^\s<>"')\]]+`)
	reMention     = regexp.MustCompile(`(^|[\s(])@([a-zA-Z0-9_]{2,32})\b`)
	reHashtag     = regexp.MustCompile(`(^|[\s(])#([a-zA-Z][a-zA-Z0-9_]{0,49})\b`)
	reBold        = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	reStrike      = regexp.MustCompile(`~~([^~\n]+)~~`)
	reUnder       = regexp.MustCompile(`__([^_\n]+)__`)
	reItalic      = regexp.MustCompile(`(^|[^*\w])\*([^*\n]+)\*`)
	reUnderItalic = regexp.MustCompile(`(^|[^_\w])_([^_\n]+)_`)
)

// Render converts markdown to safe HTML.
func Render(src string) string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	lines := strings.Split(src, "\n")
	var out strings.Builder
	var para []string
	flush := func() {
		if len(para) == 0 {
			return
		}
		body := strings.Join(para, "\n")
		out.WriteString("<p>")
		out.WriteString(renderInline(body))
		out.WriteString("</p>\n")
		para = para[:0]
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, ">! ") || line == ">!" {
			flush()
			var block []string
			block = append(block, strings.TrimPrefix(strings.TrimPrefix(line, ">! "), ">!"))
			for i+1 < len(lines) && (strings.HasPrefix(lines[i+1], ">! ") || lines[i+1] == ">!") {
				i++
				block = append(block, strings.TrimPrefix(strings.TrimPrefix(lines[i], ">! "), ">!"))
			}
			out.WriteString("<details class=\"spoiler\"><summary>Show spoiler</summary><div class=\"spoiler-body\">")
			out.WriteString(renderInline(strings.Join(block, "\n")))
			out.WriteString("</div></details>\n")
			continue
		}
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "#") {
			trimmed := strings.TrimSpace(strings.TrimLeft(line, "#"))
			if trimmed != "" {
				flush()
				out.WriteString("<h3 class=\"md-h\">" + renderInline(trimmed) + "</h3>\n")
				continue
			}
		}
		para = append(para, line)
	}
	flush()
	return out.String()
}

func renderInline(s string) string {
	// Protect escapes before escaping HTML.
	s = strings.ReplaceAll(s, "\\*", "\x00AST\x00")
	s = strings.ReplaceAll(s, "\\_", "\x00UND\x00")
	s = strings.ReplaceAll(s, "\\~", "\x00TIL\x00")
	s = html.EscapeString(s)

	// Links first, replacing them with placeholders so later passes don't
	// mangle the href or label.
	var placeholders []string
	hold := func(h string) string {
		placeholders = append(placeholders, h)
		return fmt.Sprintf("\x00LINK%d\x00", len(placeholders)-1)
	}
	s = reLink.ReplaceAllStringFunc(s, func(m string) string {
		parts := reLink.FindStringSubmatch(m)
		label, href := parts[1], html.UnescapeString(parts[2])
		if !safeURL(href) {
			return m
		}
		return hold(`<a href="` + html.EscapeString(href) + `" rel="nofollow ugc noopener" target="_blank">` + label + `</a>`)
	})
	// Bare urls (avoid ones already inside placeholders).
	s = reURL.ReplaceAllStringFunc(s, func(u string) string {
		href := html.UnescapeString(u)
		if !safeURL(href) {
			return u
		}
		text := u
		if len(text) > 60 {
			text = text[:57] + "…"
		}
		return hold(`<a href="` + html.EscapeString(href) + `" rel="nofollow ugc noopener" target="_blank">` + text + `</a>`)
	})

	// Code spans first: protect their content.
	var codes []string
	s = reCode.ReplaceAllStringFunc(s, func(m string) string {
		body := html.EscapeString(m[1 : len(m)-1])
		codes = append(codes, "<code>"+body+"</code>")
		return fmt.Sprintf("\x00CODE%d\x00", len(codes)-1)
	})

	s = reMention.ReplaceAllString(s, `$1<a class="mention" href="/users/$2">@$2</a>`)
	s = reHashtag.ReplaceAllString(s, `$1<a class="hashtag" href="/search?q=%23$2">#$2</a>`)
	s = reBold.ReplaceAllString(s, `<strong>$1</strong>`)
	s = reStrike.ReplaceAllString(s, `<del>$1</del>`)
	s = reUnder.ReplaceAllString(s, `<u>$1</u>`)
	s = reItalic.ReplaceAllString(s, `$1<em>$2</em>`)
	s = reUnderItalic.ReplaceAllString(s, `$1<em>$2</em>`)
	s = reSpoilerInline.ReplaceAllString(s, `<span class="spoiler-inline" tabindex="0" role="button" aria-label="Reveal spoiler">$1</span>`)

	// Restore protected content, innermost first.
	for i, c := range codes {
		s = strings.ReplaceAll(s, fmt.Sprintf("\x00CODE%d\x00", i), c)
	}
	for i, p := range placeholders {
		s = strings.ReplaceAll(s, fmt.Sprintf("\x00LINK%d\x00", i), p)
	}
	s = strings.ReplaceAll(s, "\x00AST\x00", "*")
	s = strings.ReplaceAll(s, "\x00UND\x00", "_")
	s = strings.ReplaceAll(s, "\x00TIL\x00", "~")

	// Hard line breaks within a paragraph.
	s = strings.ReplaceAll(s, "\n", "<br>\n")
	return s
}

var (
	reLink          = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^\s)]+)\)`)
	reCode          = regexp.MustCompile("`([^`\\n]+)`")
	reSpoilerInline = regexp.MustCompile(`\|\|([^|\n]+)\|\|`)
	reHeading       = regexp.MustCompile(`(?m)^#{1,6}\s*`)
)

func safeURL(u string) bool {
	u = strings.TrimSpace(strings.ToLower(u))
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

// PlainText strips markup-ish characters for previews, feed titles and meta
// descriptions. It also keeps mentions readable.
func PlainText(s string, max int) string {
	s = reLink.ReplaceAllString(s, "$1")
	s = reURL.ReplaceAllString(s, "")
	s = strings.NewReplacer("**", "", "__", "", "~~", "", "`", "", "*", "", "_", "").Replace(s)
	s = reHeading.ReplaceAllString(s, "")
	var b strings.Builder
	runes := []rune(s)
	for _, r := range runes {
		if r == '\n' || r == '\r' {
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			continue
		}
		if unicode.IsSpace(r) {
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if max > 0 && len([]rune(out)) > max {
		rr := []rune(out)
		out = string(rr[:max]) + "…"
	}
	return out
}
