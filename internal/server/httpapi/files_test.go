package httpapi_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zypher-systems/kairos/internal/server/config"
	"github.com/zypher-systems/kairos/internal/server/httpapi"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func filesRouter(t *testing.T) http.Handler {
	t.Helper()
	st := testutil.Store(t)
	api := httpapi.New(&config.Config{
		Secret:  "kairos-test-secret-not-for-production-32b",
		DataDir: t.TempDir(),
	}, nil, st, nil, nil)
	return api.Router()
}

func uploadLibrary(t *testing.T, h http.Handler, cookie, name string, body []byte) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://kairos.local/api/v1/files", &buf)
	req.Host = "kairos.local"
	req.Header.Set("Origin", "http://kairos.local")
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "kairos_session", Value: cookie})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload %s: %d %s", name, w.Code, w.Body.String())
	}
	var entry map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	return entry
}

func TestViewFileText(t *testing.T) {
	h := filesRouter(t)
	cookie := setupAdmin(t, h)
	entry := uploadLibrary(t, h, cookie, "note.txt", []byte("hello from kairos"))

	got := doJSON(t, h, http.MethodGet, "/api/v1/files/"+entry["id"].(string)+"/content", cookie, nil)
	if got.Code != http.StatusOK {
		t.Fatalf("view: %d %s", got.Code, got.Body.String())
	}
	if ct := got.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("content-type %q", ct)
	}
	if got.Body.String() != "hello from kairos" {
		t.Fatalf("body %q", got.Body.String())
	}
}

func TestViewFileRejectsBinary(t *testing.T) {
	h := filesRouter(t)
	cookie := setupAdmin(t, h)
	entry := uploadLibrary(t, h, cookie, "blob.bin", []byte{0x00, 0x01, 0x02, 'x'})

	got := doJSON(t, h, http.MethodGet, "/api/v1/files/"+entry["id"].(string)+"/content", cookie, nil)
	if got.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("binary view: %d %s", got.Code, got.Body.String())
	}
}

func TestViewFileRejectsOversize(t *testing.T) {
	h := filesRouter(t)
	cookie := setupAdmin(t, h)
	// 2 MiB + 1 — over the view cap, still under the upload cap.
	entry := uploadLibrary(t, h, cookie, "big.log", bytes.Repeat([]byte("a"), (2<<20)+1))

	got := doJSON(t, h, http.MethodGet, "/api/v1/files/"+entry["id"].(string)+"/content", cookie, nil)
	if got.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize view: %d %s", got.Code, got.Body.String())
	}
}

func TestListFilesNewestFirst(t *testing.T) {
	h := filesRouter(t)
	cookie := setupAdmin(t, h)
	first := uploadLibrary(t, h, cookie, "same.txt", []byte("older"))
	second := uploadLibrary(t, h, cookie, "same.txt", []byte("newer"))

	got := doJSON(t, h, http.MethodGet, "/api/v1/files", cookie, nil)
	if got.Code != http.StatusOK {
		t.Fatalf("list: %d %s", got.Code, got.Body.String())
	}
	var body struct {
		Files []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"files"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Files) != 2 {
		t.Fatalf("want 2 files, got %d", len(body.Files))
	}
	if body.Files[0].ID != second["id"] || body.Files[1].ID != first["id"] {
		t.Fatalf("order %+v want newest %s then %s", body.Files, second["id"], first["id"])
	}
	if body.Files[0].Name != "same.txt" || body.Files[1].Name != "same.txt" {
		t.Fatalf("names %+v", body.Files)
	}
}
