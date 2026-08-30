package tofu

import (
	"fmt"
	"strconv"
	"strings"
)

// writer builds HCL with block indentation tracked for it. Deliberately not templ:
// templ escapes for HTML, so every quote would come out as &quot;.
type writer struct {
	b     strings.Builder
	depth int
	// Arguments are buffered so a run of them can be written with their equals
	// signs aligned, which is what `tofu fmt` produces.
	pending [][2]string
}

func (w *writer) line(format string, args ...any) {
	w.flush()
	w.raw(fmt.Sprintf(format, args...))
}

func (w *writer) raw(s string) {
	w.b.WriteString(strings.Repeat("  ", w.depth))
	w.b.WriteString(s)
	w.b.WriteByte('\n')
}

func (w *writer) blank() {
	w.flush()
	w.b.WriteByte('\n')
}

// block writes `header {`, the body at one more level of indent, then `}`.
func (w *writer) block(header string, body func()) {
	w.line("%s {", header)
	w.depth++
	body()
	w.flush()
	w.depth--
	w.line("}")
}

// arg buffers one HCL argument. It is written when the run of arguments ends.
//
// A multi-line value ends the run: `tofu fmt` does not align across one, so
// padding it together with its neighbours would leave the output non-canonical.
func (w *writer) arg(key, expr string) {
	if strings.Contains(expr, "\n") {
		w.flush()
		w.raw(key + " = " + expr)
		return
	}
	w.pending = append(w.pending, [2]string{key, expr})
}

// flush writes the buffered arguments with their equals signs aligned.
func (w *writer) flush() {
	if len(w.pending) == 0 {
		return
	}
	width := 0
	for _, kv := range w.pending {
		if len(kv[0]) > width {
			width = len(kv[0])
		}
	}
	pending := w.pending
	w.pending = nil
	for _, kv := range pending {
		w.raw(fmt.Sprintf("%-*s = %s", width, kv[0], kv[1]))
	}
}

func (w *writer) String() string {
	w.flush()
	return w.b.String()
}

// quote renders a Go string as an HCL string literal.
//
// The `${` and `%{` escaping is not cosmetic: these are user-supplied names and
// notes, and an unescaped `${...}` would be evaluated as an HCL interpolation
// rather than written out as text.
func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	// A literal newline inside a quoted string is a parse error, so scripts have to
	// be escaped rather than embedded. Carriage returns go first, or a CRLF would
	// come out as \r\\n.
	s = strings.ReplaceAll(s, "\r\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\n`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "${", "$${")
	s = strings.ReplaceAll(s, "%{", "%%{")
	return `"` + s + `"`
}

// interp wraps an expression that already contains references so it reads as a
// single HCL string, e.g. "${aws_eip.x.public_ip}/32".
func interp(s string) string {
	if strings.Contains(s, "${") {
		return `"` + s + `"`
	}
	return quote(s)
}

// value renders a normalised param value. Numbers arrive from JSON as float64, so
// whole ones must not come out as 20.000000.
func value(v any) string {
	switch n := v.(type) {
	case nil:
		return `null`
	case bool:
		return strconv.FormatBool(n)
	case float64:
		return strconv.FormatFloat(n, 'f', -1, 64)
	case string:
		return quote(n)
	default:
		return quote(fmt.Sprint(n))
	}
}
