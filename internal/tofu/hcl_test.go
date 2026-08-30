package tofu

import (
	"strings"
	"testing"

	"infrachart/internal/catalog"
)

// Nested blocks needed no new writer machinery: block() already flushes buffered
// arguments before opening, so ordering and per-run alignment fall out. This
// records that, so nobody adds the machinery the plan originally called for.
func TestWriterNestsBlocks(t *testing.T) {
	w := &writer{}
	w.block(`resource "google_compute_instance" "asset_1"`, func() {
		w.arg("name", quote("asset-1"))
		w.arg("machine_type", quote("e2-medium"))
		w.block("boot_disk", func() {
			w.block("initialize_params", func() {
				w.arg("image", quote("debian-12"))
				w.arg("size", "10")
			})
		})
		w.arg("can_ip_forward", "false")
	})

	got := w.String()
	want := `resource "google_compute_instance" "asset_1" {
  name         = "asset-1"
  machine_type = "e2-medium"
  boot_disk {
    initialize_params {
      image = "debian-12"
      size  = 10
    }
  }
  can_ip_forward = false
}
`
	if got != want {
		t.Errorf("nested block output wrong.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// Alignment is per run of arguments, not per block: arguments before and after a
// nested block are separate runs, so a long key in one must not pad the other.
func TestAlignmentDoesNotLeakAcrossBlocks(t *testing.T) {
	w := &writer{}
	w.block("outer", func() {
		w.arg("a", "1")
		w.block("inner", func() {
			w.arg("a_very_long_key_indeed", "2")
		})
		w.arg("b", "3")
	})
	for _, line := range strings.Split(w.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "b ") && !strings.Contains(line, "b = 3") {
			t.Errorf("alignment leaked from the nested block: %q", line)
		}
	}
}

// A literal newline inside an HCL quoted string is a parse error, so a startup
// script has to be escaped rather than embedded. Nothing exercised this until
// user data, because validation rejected newlines everywhere else.
func TestQuoteEscapesNewlines(t *testing.T) {
	cases := map[string]string{
		"a\nb":      `"a\nb"`,
		"a\r\nb":    `"a\nb"`, // CRLF collapses, rather than becoming \r\n
		"a\rb":      `"a\nb"`,
		"a\n\nb":    `"a\n\nb"`,
		"#!/bin/sh": `"#!/bin/sh"`,
	}
	for in, want := range cases {
		if got := quote(in); got != want {
			t.Errorf("quote(%q) = %s, want %s", in, got, want)
		}
	}
	// The existing escapes must survive the change.
	if got := quote("${x} \"q\""); got != `"$${x} \"q\""` {
		t.Errorf("interpolation or quote escaping broke: %s", got)
	}
}

// Azure's custom_data takes base64 where the other clouds take a plain string.
func TestWrapAppliesToValues(t *testing.T) {
	plain := catalog.ParamField{Key: "user_data"}
	b64 := catalog.ParamField{Key: "custom_data", Wrap: "base64encode(%s)"}

	if got := wrapped(plain, "#!/bin/sh\necho hi"); got != `"#!/bin/sh\necho hi"` {
		t.Errorf("unwrapped value = %s", got)
	}
	if got := wrapped(b64, "#!/bin/sh\necho hi"); got != `base64encode("#!/bin/sh\necho hi")` {
		t.Errorf("wrapped value = %s", got)
	}
}
