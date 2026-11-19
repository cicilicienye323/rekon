// Go tests for the handlers. The browser tests in e2e/ cover the happy path a
// person walks; these cover the paths a person hits by accident, where the
// message matters more than the layout.
package web

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	providerHeader = "Status,Type,Channel,Currency,Amount," +
		"Transaction Fee Amount,Transaction VAT Amount,Xendit Withholding Tax," +
		"Net Amount,Reference,Transaction ID," +
		"Payment Date,Timezone,Settlement Status,Settlement Date,Estimated Settlement Date\n"
	providerRow = "SUCCESSFUL,PAYMENT,MANDIRI,IDR,150000.00,9000.00,990.00,0.00,140010.00," +
		"REF-1,txn_1,2026-09-01T10:00:00Z,UTC,SETTLED,2026-09-02T10:00:00Z,2026-09-07T10:00:00Z\n"
	ledgerHeader = "order_id,reference,gross,currency,paid_at,status\n"
	ledgerRow    = "ORD-1,REF-1,150000.00,IDR,2026-09-01T10:00:00Z,PAID\n"
	retired      = "Status,Type,Currency,Amount,Transaction Fee Amount," +
		"Transaction VAT Amount,Withholding Tax Amount,Net Amount,Reference," +
		"Payment Date,Settlement Status\n"
)

func upload(t *testing.T, url string, files map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for field, content := range files {
		part, err := w.CreateFormFile(field, field+".csv")
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(part, content)
	}
	w.Close()

	req := httptest.NewRequest(http.MethodPost, url, &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()

	switch url {
	case "/inspect":
		handleInspect(rec, req)
	case "/reconcile":
		handleReconcile(rec, req)
	}
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) inspection {
	t.Helper()
	var got inspection
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("the inspect response does not parse: %v\n%s", err, rec.Body.String())
	}
	return got
}

func TestInspectRecognisesBothKinds(t *testing.T) {
	got := decode(t, upload(t, "/inspect", map[string]string{"file": providerHeader + providerRow}))
	if !got.OK || got.Kind != "provider" {
		t.Errorf("provider file: ok=%v kind=%q err=%q", got.OK, got.Kind, got.Error)
	}
	if !strings.Contains(got.Summary, "UTC") {
		t.Errorf("the summary does not show the timezone: %q", got.Summary)
	}

	got = decode(t, upload(t, "/inspect", map[string]string{"file": ledgerHeader + ledgerRow}))
	if !got.OK || got.Kind != "internal" {
		t.Errorf("order file: ok=%v kind=%q err=%q", got.OK, got.Kind, got.Error)
	}
}

// Each rejection has to say what to do next. An operator who cannot act on the
// message calls an engineer, and the tool has failed at its one job.
func TestInspectExplainsEveryRejection(t *testing.T) {
	cases := []struct {
		name, body, wants string
	}{
		{"an empty file", "", "may be empty"},
		{"the retired format", retired, "July 2026"},
		{"something that is not a report at all", "a,b,c\n1,2,3\n", "July 2026"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decode(t, upload(t, "/inspect", map[string]string{"file": c.body}))
			if got.OK {
				t.Fatal("the file was accepted")
			}
			if got.Error == "" {
				t.Error("no error was reported")
			}
			if got.Why == "" {
				t.Error("no explanation was given, so the operator has nothing to act on")
			}
			if !strings.Contains(got.Why, c.wants) {
				t.Errorf("the explanation does not mention %q: %q", c.wants, got.Why)
			}
		})
	}
}

// A file whose header is right but whose rows are not still has to fail with a
// sentence, not a stack trace.
func TestInspectExplainsABadRow(t *testing.T) {
	bad := providerHeader + strings.Replace(providerRow, ",UTC,", ",,", 1)
	got := decode(t, upload(t, "/inspect", map[string]string{"file": bad}))

	if got.OK {
		t.Fatal("a row with an empty Timezone was accepted")
	}
	if !strings.Contains(got.Why, "Jakarta time or UTC") {
		t.Errorf("the explanation does not say why guessing is unsafe: %q", got.Why)
	}
}

func TestInspectHandlesNoFileAtAll(t *testing.T) {
	got := decode(t, upload(t, "/inspect", map[string]string{}))
	if got.OK {
		t.Error("an empty upload was accepted")
	}
}

func TestReconcileRendersTheReport(t *testing.T) {
	rec := upload(t, "/reconcile", map[string]string{
		"a": providerHeader + providerRow,
		"b": ledgerHeader + ledgerRow,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"Settlement reconciliation", "Matched and clean", "<style>"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("the report does not contain %q", want)
		}
	}
}

// The same kind twice cannot be reconciled, and swapping the two fields must
// make no difference.
func TestReconcileRefusesAnImpossiblePair(t *testing.T) {
	for _, files := range []map[string]string{
		{"a": providerHeader, "b": providerHeader},
		{"a": ledgerHeader, "b": ledgerHeader},
		{"a": providerHeader, "b": retired},
	} {
		rec := upload(t, "/reconcile", files)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "Start over") {
			t.Error("the error page offers no way back")
		}
	}
}

func TestReconcileIsIndifferentToFieldOrder(t *testing.T) {
	swapped := upload(t, "/reconcile", map[string]string{
		"a": ledgerHeader + ledgerRow,
		"b": providerHeader + providerRow,
	})
	if swapped.Code != http.StatusOK {
		t.Fatalf("status = %d with the files swapped, want 200", swapped.Code)
	}
}

// The picker is served from the binary, so it can never be missing at runtime.
func TestPageIsEmbeddedAndSelfContained(t *testing.T) {
	rec := httptest.NewRecorder()
	handlePage(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"<form", `name="a"`, `name="b"`, "stay on this computer"} {
		if !strings.Contains(body, want) {
			t.Errorf("the picker does not contain %q", want)
		}
	}
	for _, forbidden := range []string{"<link", "http://", "https://"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("the picker reaches outside itself: found %q", forbidden)
		}
	}

	rec = httptest.NewRecorder()
	handlePage(rec, httptest.NewRequest(http.MethodGet, "/anything-else", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("an unknown path returned %d, want 404", rec.Code)
	}
}

// Error text goes into HTML, so a parser message carrying a quote or an angle
// bracket must not be able to break the page open.
func TestErrorTextIsEscaped(t *testing.T) {
	rec := httptest.NewRecorder()
	httpError(rec, `<script>alert("x")</script>`)

	body := rec.Body.String()
	if strings.Contains(body, "<script>alert") {
		t.Error("the message was written into the page unescaped")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("the message was not escaped as expected: %s", body)
	}
}
