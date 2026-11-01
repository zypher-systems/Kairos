package storage

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeS3 is the part of S3 the backend's writes need: PUT, the three
// multipart calls, and If-None-Match on whichever request makes an object
// visible. It is a test double, so it answers for the protocol only.
type fakeS3 struct {
	mu       sync.Mutex
	objects  map[string][]byte
	uploads  map[string]map[int][]byte
	nextID   int
	aborted  int
	noCondit bool // answer conditional writes with 501, as some stores do
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/bucket/")
	q := r.URL.Query()
	f.mu.Lock()
	defer f.mu.Unlock()

	// The final write: PUT of a whole object, or completing an upload.
	final := func(body []byte) {
		if r.Header.Get("If-None-Match") == "*" {
			if f.noCondit {
				s3Error(w, http.StatusNotImplemented, "NotImplemented")
				return
			}
			if _, ok := f.objects[key]; ok {
				s3Error(w, http.StatusPreconditionFailed, "PreconditionFailed")
				return
			}
		}
		f.objects[key] = body
		w.Header().Set("ETag", `"etag"`)
		fmt.Fprintf(w, `<CompleteMultipartUploadResult><Bucket>bucket</Bucket><Key>%s</Key><ETag>"etag"</ETag></CompleteMultipartUploadResult>`, key)
	}

	switch {
	case r.Method == http.MethodPost && q.Has("uploads"):
		f.nextID++
		id := fmt.Sprint(f.nextID)
		f.uploads[id] = map[int][]byte{}
		fmt.Fprintf(w, `<InitiateMultipartUploadResult><UploadId>%s</UploadId></InitiateMultipartUploadResult>`, id)
	case r.Method == http.MethodPut && q.Has("partNumber"):
		var num int
		fmt.Sscan(q.Get("partNumber"), &num)
		f.mu.Unlock()
		body := readS3Body(r)
		f.mu.Lock()
		f.uploads[q.Get("uploadId")][num] = body
		w.Header().Set("ETag", fmt.Sprintf(`"part-%d"`, num))
	case r.Method == http.MethodPost && q.Has("uploadId"):
		var req struct {
			Parts []struct {
				PartNumber int
			} `xml:"Part"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = xml.Unmarshal(data, &req)
		var nums []int
		for _, p := range req.Parts {
			nums = append(nums, p.PartNumber)
		}
		sort.Ints(nums)
		var body []byte
		for _, n := range nums {
			body = append(body, f.uploads[q.Get("uploadId")][n]...)
		}
		final(body)
	case r.Method == http.MethodDelete && q.Has("uploadId"):
		f.aborted++
		delete(f.uploads, q.Get("uploadId"))
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPut:
		f.mu.Unlock()
		body := readS3Body(r)
		f.mu.Lock()
		final(body)
	case r.Method == http.MethodHead:
		if _, ok := f.objects[key]; !ok {
			w.WriteHeader(http.StatusNotFound)
		}
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func s3Error(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<Error><Code>%s</Code><Message>%s</Message></Error>`, code, code)
}

// readS3Body removes aws-chunked framing from a streaming-signed body.
func readS3Body(r *http.Request) []byte {
	data, _ := io.ReadAll(r.Body)
	if !strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") {
		return data
	}
	var out []byte
	for len(data) > 0 {
		head, rest, ok := strings.Cut(string(data), "\r\n")
		if !ok {
			break
		}
		var size int
		fmt.Sscanf(head, "%x", &size)
		if size == 0 {
			break
		}
		out = append(out, rest[:size]...)
		data = []byte(rest[size+2:])
	}
	return out
}

func openFakeS3(t *testing.T) (*S3, *fakeS3) {
	t.Helper()
	f := &fakeS3{objects: map[string][]byte{}, uploads: map[string]map[int][]byte{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	b, err := OpenS3(context.Background(), Spec{
		Kind: KindS3, Endpoint: strings.TrimPrefix(srv.URL, "http://"), Bucket: "bucket",
		AccessKey: "k", SecretKey: "s", Region: "us-east-1", PathStyle: true, Insecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b, f
}

func (f *fakeS3) object(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.objects[key])
}

func (f *fakeS3) set(key, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = []byte(body)
}

// A key that appears after Create's check must survive with overwrite off.
func TestS3OverwriteOffHoldsAtCommit(t *testing.T) {
	ctx := context.Background()
	b, f := openFakeS3(t)
	w, err := b.Create(ctx, "out.csv", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "late"); err != nil {
		t.Fatal(err)
	}
	f.set("out.csv", "first")
	if err := w.Commit(zeroTime); !errors.Is(err, ErrExists) {
		t.Fatalf("commit over a new key must fail with ErrExists: %v", err)
	}
	if got := f.object("out.csv"); got != "first" {
		t.Fatalf("object replaced with overwrite off: %q", got)
	}
}

func TestS3OverwriteOnReplaces(t *testing.T) {
	ctx := context.Background()
	b, f := openFakeS3(t)
	f.set("out.csv", "first")
	w, err := b.Create(ctx, "out.csv", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "second"); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(zeroTime); err != nil {
		t.Fatal(err)
	}
	if got := f.object("out.csv"); got != "second" {
		t.Fatalf("landed %q", got)
	}
}

// The condition must reach the request that makes the object visible, both
// for a single PUT and for the completion of a multipart upload, where the
// data is already in the store and only that request decides.
func TestPutIfAbsent(t *testing.T) {
	body := strings.Repeat("0123456789", 10)
	sizes := map[string]int64{"single put": 1 << 20, "multipart": 16, "exact parts": 25}
	cases := []struct {
		name    string
		prepare func(*fakeS3)
		want    error
		object  string
	}{
		{"free key lands", func(*fakeS3) {}, nil, body},
		{"taken key is refused", func(f *fakeS3) { f.objects["out.csv"] = []byte("first") }, ErrExists, "first"},
		{"store without conditions falls back", func(f *fakeS3) { f.noCondit = true }, nil, body},
	}
	for sizeName, size := range sizes {
		for _, c := range cases {
			t.Run(sizeName+"/"+c.name, func(t *testing.T) {
				b, f := openFakeS3(t)
				b.partSize = size
				c.prepare(f)
				err := b.putIfAbsent(context.Background(), "out.csv", strings.NewReader(body))
				if !errors.Is(err, c.want) {
					t.Fatalf("got %v want %v", err, c.want)
				}
				if got := f.object("out.csv"); got != c.object {
					t.Fatalf("object %q want %q", got, c.object)
				}
				if c.want != nil && size < int64(len(body)) && f.aborted != 1 {
					t.Fatalf("a refused upload must be aborted, aborted=%d", f.aborted)
				}
			})
		}
	}
}

// An interrupted source must not become a short object.
func TestPutIfAbsentAbortsWhenTheSourceFails(t *testing.T) {
	b, f := openFakeS3(t)
	b.partSize = 4
	src := io.MultiReader(strings.NewReader("0123456789"), failingReader{})
	if err := b.putIfAbsent(context.Background(), "out.csv", src); !errors.Is(err, errSourceFailed) {
		t.Fatalf("got %v", err)
	}
	if _, ok := f.objects["out.csv"]; ok || f.aborted != 1 {
		t.Fatalf("objects=%v aborted=%d", f.objects, f.aborted)
	}
}

var errSourceFailed = errors.New("source failed")

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errSourceFailed }

func TestS3CopyRefusesTheSameObject(t *testing.T) {
	b, f := openFakeS3(t)
	f.set("a.csv", "only copy")
	if _, err := CopyFile(context.Background(), b, "a.csv", b, "./a.csv", CopyOptions{Overwrite: true}); err == nil || !strings.Contains(err.Error(), "same file") {
		t.Fatalf("got %v", err)
	}
}
