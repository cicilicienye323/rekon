// Package web serves the local page finance uses.
//
// It is a server only in the mechanical sense. It binds to the loopback
// address, so nothing on the network can reach it, keeps no state between
// requests, writes nothing to disk, and stops when the process does. What it
// provides that a plain CLI cannot is a file picker and an error a person can
// act on without asking an engineer.
package web

import (
	"bytes"
	_ "embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/cicilicienye323/rekon/internal/ledger"
	"github.com/cicilicienye323/rekon/internal/match"
	"github.com/cicilicienye323/rekon/internal/report"
	"github.com/cicilicienye323/rekon/internal/xendit"
)

//go:embed picker.html
var picker []byte

// maxUpload caps a single file. A day of transactions for the merchants this
// targets is a few megabytes; the cap exists so a mistaken selection fails
// quickly rather than exhausting memory.
//
// ponytail: whole files are held in memory. Streaming would lift the cap, and
// is the same change the matcher needs past a few million rows.
const maxUpload = 64 << 20

// Serve starts the page and opens a browser at it. It returns when the process
// is killed.
//
// port 0 asks the operating system for any free port, which is right for a
// person double-clicking the binary. A fixed port exists for the browser tests,
// which need to know the address in advance.
func Serve(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("cannot open a local port: %w", err)
	}
	url := "http://" + ln.Addr().String() + "/"

	mux := http.NewServeMux()
	mux.HandleFunc("/", handlePage)
	mux.HandleFunc("/inspect", handleInspect)
	mux.HandleFunc("/reconcile", handleReconcile)

	fmt.Println("rekon is open at", url)
	fmt.Println("close this window when you are done.")
	openBrowser(url)

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	return srv.Serve(ln)
}

func handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(picker)
}

// inspection is what the page shows next to a slot the moment a file is chosen.
type inspection struct {
	OK      bool   `json:"ok"`
	Name    string `json:"name"`
	Kind    string `json:"kind,omitempty"`
	Summary string `json:"summary,omitempty"`
	Error   string `json:"error,omitempty"`
	Why     string `json:"why,omitempty"`
}

func handleInspect(w http.ResponseWriter, r *http.Request) {
	name, data, err := oneFile(r, "file")
	res := inspection{Name: name}
	if err != nil {
		res.Error = err.Error()
		writeJSON(w, res)
		return
	}
	writeJSON(w, inspect(name, data))
}

func inspect(name string, data []byte) inspection {
	res := inspection{Name: name}

	head, err := csv.NewReader(bytes.NewReader(data)).Read()
	if err != nil {
		res.Error = "this file has no readable header row"
		res.Why = "The file may be empty, or saved in a format other than CSV. Export it again as CSV."
		return res
	}

	switch {
	case xendit.Recognise(head):
		txns, meta, err := xendit.Read(bytes.NewReader(data))
		if err != nil {
			res.Error = err.Error()
			res.Why = plainWhy(err)
			return res
		}
		res.OK, res.Kind = true, "provider"
		res.Summary = fmt.Sprintf("%d payments · times written in %s", len(txns), meta.Timezone)
		if n := skipped(meta); n > 0 {
			res.Summary += fmt.Sprintf(" · %d rows that are not payments", n)
		}
	case ledger.Recognise(head):
		orders, err := ledger.Read(bytes.NewReader(data))
		if err != nil {
			res.Error = err.Error()
			res.Why = plainWhy(err)
			return res
		}
		res.OK, res.Kind = true, "internal"
		res.Summary = fmt.Sprintf("%d orders", len(orders))
	default:
		res.Error = "this is not a current Xendit transaction report, and not an order export either"
		res.Why = "The Xendit report format that retired in July 2026 can no longer be read, " +
			"because it does not say which timezone its times are in. Download a fresh report " +
			"from the dashboard and it will carry that column."
	}
	return res
}

func handleReconcile(w http.ResponseWriter, r *http.Request) {
	nameA, a, errA := oneFile(r, "a")
	nameB, b, errB := oneFile(r, "b")
	if errA != nil || errB != nil {
		httpError(w, "both files are needed")
		return
	}

	ia, ib := inspect(nameA, a), inspect(nameB, b)
	if !ia.OK || !ib.OK || ia.Kind == ib.Kind {
		httpError(w, "the two files could not be read as one provider report and one order export")
		return
	}

	provider, orderData := a, b
	if ia.Kind == "internal" {
		provider, orderData = b, a
	}

	orders, err := ledger.Read(bytes.NewReader(orderData))
	if err != nil {
		httpError(w, err.Error())
		return
	}
	txns, meta, err := xendit.Read(bytes.NewReader(provider))
	if err != nil {
		httpError(w, err.Error())
		return
	}

	now := time.Now().UTC()
	rep := match.Run(orders, txns, now)
	rep.Adapter, rep.Timezone, rep.SkippedByType = meta.Adapter, meta.Timezone, meta.SkippedByType
	view := report.NewView(rep, len(orders), len(txns)+skipped(meta), now)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := report.HTML(w, view); err != nil {
		httpError(w, err.Error())
	}
}

// oneFile reads a single uploaded part fully. Nothing is written to disk: the
// bytes live for the length of the request and are then gone.
func oneFile(r *http.Request, field string) (string, []byte, error) {
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		return "", nil, fmt.Errorf("could not read the upload: %w", err)
	}
	f, hdr, err := r.FormFile(field)
	if err != nil {
		return "", nil, fmt.Errorf("no file was sent")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxUpload))
	if err != nil {
		return hdr.Filename, nil, err
	}
	return hdr.Filename, data, nil
}

// plainWhy turns a parser error into the sentence a non-engineer needs: what to
// do next, not what went wrong internally.
func plainWhy(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "Timezone"):
		return "Without that column the times could be read as either Jakarta time or UTC. " +
			"Guessing wrong moves transactions to the wrong day, so the report would be wrong " +
			"in a way nobody would notice. Download the report again from the dashboard."
	case strings.Contains(s, "column is missing"):
		return "The report is missing a column this needs. It is probably an older export. " +
			"Download a fresh one from the dashboard."
	case strings.Contains(s, "ambiguous separator"):
		return "An amount in this file uses a separator that could mean either thousands or " +
			"decimals. Export the file again without formatting applied to the numbers."
	}
	return "Check that this is the file you meant to choose, then try again."
}

func skipped(m xendit.Meta) int {
	n := 0
	for _, c := range m.SkippedByType {
		n += c
	}
	return n
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8">
<style>body{font:15px/1.6 "Segoe UI",system-ui,sans-serif;margin:60px auto;max-width:560px;color:#16191d}
a{color:#1f4d6b}</style>
<h1 style="font-size:18px">That did not work</h1><p>%s</p><p><a href="/">Start over</a></p>`,
		htmlEscape(msg))
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// openBrowser is best effort. If it fails the URL is already printed, and the
// operator can paste it.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
