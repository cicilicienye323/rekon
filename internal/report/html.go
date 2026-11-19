package report

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"strings"
	"time"

	"github.com/cicilicienye323/rekon/internal/money"
)

// The page and its stylesheet are compiled into the binary, so the produced
// file is self-contained: it can be emailed, saved, or opened six months later
// and still render.
//
//go:embed page.html
var pageTmpl string

//go:embed style.css
var styleCSS string

var tmpl = template.Must(template.New("page").Funcs(template.FuncMap{
	"n":    group,
	"amt":  func(a money.Amount) string { return thousands(a.String()) },
	"slug": slug,
	"date": func(t *time.Time) string { return t.Format("2 January") },
	"days": func(now time.Time, t *time.Time) string {
		return fmt.Sprintf("%d", int(now.Sub(*t).Hours()/24))
	},
}).Parse(pageTmpl))

// HTML writes a standalone page. Everything it needs is inlined: no stylesheet
// to fetch, no script, no font from a network the reader may not have.
func HTML(w io.Writer, v View) error {
	if _, err := fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Settlement reconciliation</title>
<style>%s</style>
</head>
<body>
`, styleCSS); err != nil {
		return err
	}
	if err := tmpl.ExecuteTemplate(w, "report", v); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n</body>\n</html>\n")
	return err
}

// slug turns a group name into a CSS class. Punctuation is dropped rather than
// hyphenated: "In flight, nothing wrong" has to become in-flight-nothing-wrong,
// not in-flight,-nothing-wrong, which no stylesheet would match.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		default:
			dash = true
		}
	}
	return b.String()
}
