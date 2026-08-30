package tofu

import (
	"strings"
	"testing"
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
