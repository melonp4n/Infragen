// infrachart is a localhost web app for drawing short-lived infrastructure as a
// chart and generating OpenTofu from it.
//
// The server is stateless. The browser owns the chart while it is being edited;
// the server renders markup fragments on request, validates imported sessions
// and generates configuration. Nothing is stored server-side — a session leaves
// as a JSON file and comes back the same way.
package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/a-h/templ"

	"infrachart/internal/catalog"
	"infrachart/internal/model"
	"infrachart/internal/tofu"
	"infrachart/internal/ui"
)

//go:embed web/static
var staticFiles embed.FS

// maxBody caps request bodies. A session is small; anything larger is a mistake
// or an attack, not a chart.
const maxBody = 4 << 20

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	seed := flag.Bool("seed", false, "start with the demonstration chart")
	flag.Parse()

	static, err := fs.Sub(staticFiles, "web/static")
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /{$}", indexHandler(*seed))
	mux.HandleFunc("GET /api/catalog", catalogHandler)
	mux.HandleFunc("POST /api/render/account", renderAccount)
	mux.HandleFunc("POST /api/render/asset", renderAsset)
	mux.HandleFunc("POST /api/render/extip", renderExtIP)
	mux.HandleFunc("POST /api/render/canvas", renderCanvas)
	mux.HandleFunc("POST /api/render/drawer", renderDrawer)
	mux.HandleFunc("POST /api/generate", generate)
	mux.HandleFunc("POST /api/validate", validate)

	log.Printf("infrachart listening on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func indexHandler(seed bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := model.Session{Version: model.SchemaVersion, Internet: model.Point{X: 1020, Y: 40}}
		if seed {
			s = model.Seed()
		}
		// Normalise before it reaches the browser: among other things this turns
		// nil slices into empty ones, which the JavaScript relies on.
		model.Normalise(&s)
		render(w, r, ui.Page(s))
	}
}

// catalogHandler serves the provider table so the browser can build type menus
// and param forms without a round-trip per interaction.
func catalogHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, catalog.All())
}

// renderAccount mints an ID for a new account and returns its card.
func renderAccount(w http.ResponseWriter, r *http.Request) {
	var acc model.Account
	if !decode(w, r, &acc) {
		return
	}
	if _, ok := catalog.Get(acc.Provider); !ok {
		http.Error(w, "unknown provider", http.StatusBadRequest)
		return
	}
	if err := checkText(acc.Name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	acc.ID = newID("acc")
	acc.Assets = nil
	render(w, r, ui.AccountCard(acc))
}

// renderAsset mints an ID for a new asset and returns its tile. Params are not
// read from the request — the browser holds them, and they are only validated
// when a whole session is imported or generated.
func renderAsset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccountID string `json:"accountId"`
		Provider  string `json:"provider"`
		Code      string `json:"code"`
		Name      string `json:"name"`
	}
	if !decode(w, r, &req) {
		return
	}
	if _, ok := catalog.Type(req.Provider, req.Code); !ok {
		http.Error(w, "unknown resource type", http.StatusBadRequest)
		return
	}
	if err := checkText(req.Name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a := model.Asset{ID: newID("asset"), Code: req.Code, Name: req.Name}
	render(w, r, ui.AssetTile(req.AccountID, req.Provider, a))
}

// renderExtIP mints an ID for a new hardcoded address and returns its node.
func renderExtIP(w http.ResponseWriter, r *http.Request) {
	var e model.ExternalIP
	if !decode(w, r, &e) {
		return
	}
	p, err := model.ParseCIDR(e.IP)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := checkText(e.Label); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	e.IP = p.String()
	e.ID = newID("extip")
	render(w, r, ui.ExtIPNode(e))
}

// renderCanvas re-renders a whole chart. This is how import works: the file is
// validated, normalised, then handed back as markup.
func renderCanvas(w http.ResponseWriter, r *http.Request) {
	var s model.Session
	if !decode(w, r, &s) {
		return
	}
	if err := model.Validate(&s); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	model.Normalise(&s)
	render(w, r, ui.Canvas(s))
}

// renderDrawer returns the edit panel for whatever is selected. The browser
// sends the whole session because the panel needs node labels and exposure
// classification, and working those out twice — once here, once in JavaScript —
// is how the two would drift apart.
func renderDrawer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Session   model.Session `json:"session"`
		Selection ui.Selection  `json:"selection"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := model.Validate(&req.Session); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	model.Normalise(&req.Session)
	render(w, r, ui.Drawer(req.Session, req.Selection))
}

// generate renders the chart as OpenTofu, with any warnings above the output.
// Warnings go into the response rather than being dropped: a refusal the user
// never sees is as bad as a silently broken rule.
func generate(w http.ResponseWriter, r *http.Request) {
	var s model.Session
	if !decode(w, r, &s) {
		return
	}
	if err := model.Validate(&s); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Normalise before classifying: it is what guarantees params match their
	// declared types, which the classifier reads.
	model.Normalise(&s)

	hcl, warnings := tofu.Generate(s)
	var body strings.Builder
	if len(warnings) > 0 {
		fmt.Fprintf(&body, "# %d thing(s) need your attention\n#\n", len(warnings))
		for _, line := range warnings {
			fmt.Fprintf(&body, "#   ! %s\n", line)
		}
		body.WriteString("\n")
	}
	body.WriteString(hcl)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if _, err := w.Write([]byte(body.String())); err != nil {
		log.Printf("generate: %v", err)
	}
}

// validate runs the real tool over the generated configuration and reports what
// it said. The configuration is regenerated here rather than taken from the
// request body: validation executes provider plugins, so what it runs on must be
// something this process produced, not whatever a browser posted.
func validate(w http.ResponseWriter, r *http.Request) {
	var s model.Session
	if !decode(w, r, &s) {
		return
	}
	if err := model.Validate(&s); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	model.Normalise(&s)

	// The first run downloads providers, which is slow but not unbounded.
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()

	hcl, _ := tofu.Generate(s)
	writeJSON(w, tofu.Validate(ctx, hcl))
}

func render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(r.Context(), w); err != nil {
		log.Printf("render: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encode: %v", err)
	}
}

// decode reads a size-capped JSON body, rejecting unknown fields so a typo in a
// hand-edited session file surfaces as an error instead of a silent default.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		http.Error(w, "malformed request: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

// checkText guards the short free-text fields that reach markup and, later, HCL.
func checkText(v string) error {
	if len(v) > 200 {
		return errTooLong
	}
	if strings.ContainsAny(v, "\x00\n\r") {
		return errControlChar
	}
	return nil
}

var (
	errTooLong     = &textError{"value is too long"}
	errControlChar = &textError{"value contains a control character"}
)

type textError struct{ msg string }

func (e *textError) Error() string { return e.msg }

// newID mints an identifier that survives export and re-import. Random rather
// than sequential so merging two sessions by hand cannot collide.
func newID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing means the process cannot be trusted
	}
	return prefix + "_" + hex.EncodeToString(b)
}
