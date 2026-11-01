package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3 is a prefix in an S3-compatible bucket (AWS, MinIO, Ceph, Wasabi…).
// Objects are written with a single PutObject, which is atomic: a reader
// never sees a partial object, so no temporary name is needed.
type S3 struct {
	root     string
	bucket   string
	endpoint string
	client   *minio.Client
	// partSize overrides every upload part size; zero means s3FirstPart,
	// then parts sized for the largest object S3 allows.
	partSize int64
}

// s3FirstPart is read before an upload commits to anything. A smaller object
// is one PUT, so it never reserves the buffer a 5 TiB upload needs.
const s3FirstPart = 16 << 20

// OpenS3 builds the client for a spec. Endpoint empty means AWS in Region.
func OpenS3(ctx context.Context, s Spec) (*S3, error) {
	if s.Bucket == "" {
		return nil, errors.New("s3 bucket is required")
	}
	endpoint := strings.TrimSpace(s.Endpoint)
	secure := !s.Insecure
	if endpoint == "" {
		endpoint = "s3.amazonaws.com"
		if s.Region != "" && s.Region != "us-east-1" {
			endpoint = "s3." + s.Region + ".amazonaws.com"
		}
		secure = true
	}
	endpoint = strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")
	var creds *credentials.Credentials
	if s.AccessKey != "" || s.SecretKey != "" {
		creds = credentials.NewStaticV4(s.AccessKey, s.SecretKey, "")
	} else {
		// Instance roles, environment, or shared credential files.
		creds = credentials.NewChainCredentials([]credentials.Provider{
			&credentials.EnvAWS{},
			&credentials.FileAWSCredentials{},
			&credentials.IAM{},
		})
	}
	lookup := minio.BucketLookupAuto
	if s.PathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:        creds,
		Secure:       secure,
		Region:       s.Region,
		BucketLookup: lookup,
	})
	if err != nil {
		return nil, fmt.Errorf("s3 client: %w", err)
	}
	return &S3{root: strings.Trim(strings.TrimSpace(s.Root), "/"), bucket: s.Bucket, endpoint: endpoint, client: client}, nil
}

func (b *S3) Kind() Kind { return KindS3 }

func (b *S3) key(p string) (string, error) {
	rel, err := cleanRel("/"+b.root, p)
	if err != nil {
		// cleanRel with a root refuses escapes; for s3 an empty root is
		// still a rooted namespace (the bucket), so use "/" as the root.
		return "", err
	}
	if b.root == "" {
		return rel, nil
	}
	return b.root + "/" + rel, nil
}

func (b *S3) identity(p string) (string, error) {
	key, err := b.key(p)
	if err != nil {
		return "", err
	}
	return "s3://" + strings.ToLower(strings.TrimRight(b.endpoint, "/")) + "/" + b.bucket + "/" + key, nil
}

func (b *S3) rel(key string) string {
	if b.root == "" {
		return key
	}
	return strings.TrimPrefix(key, b.root+"/")
}

func (b *S3) List(ctx context.Context, pattern string) ([]Entry, error) {
	keyPattern, err := b.key(pattern)
	if err != nil {
		return nil, err
	}
	prefix := literalPrefix(keyPattern)
	var out []Entry
	for obj := range b.client.ListObjects(ctx, b.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		if strings.HasSuffix(obj.Key, "/") {
			continue
		}
		ok, err := path.Match(keyPattern, obj.Key)
		if err != nil {
			return nil, fmt.Errorf("bad pattern: %w", err)
		}
		if !ok {
			continue
		}
		out = append(out, Entry{Path: b.rel(obj.Key), Size: obj.Size, ModTime: obj.LastModified})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (b *S3) Stat(ctx context.Context, p string) (Entry, error) {
	key, err := b.key(p)
	if err != nil {
		return Entry{}, err
	}
	info, err := b.client.StatObject(ctx, b.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return Entry{}, ErrNotFound
		}
		return Entry{}, err
	}
	return Entry{Path: p, Size: info.Size, ModTime: info.LastModified}, nil
}

func (b *S3) Open(ctx context.Context, p string) (io.ReadCloser, Entry, error) {
	key, err := b.key(p)
	if err != nil {
		return nil, Entry{}, err
	}
	obj, err := b.client.GetObject(ctx, b.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, Entry{}, err
	}
	info, err := obj.Stat()
	if err != nil {
		obj.Close()
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, Entry{}, ErrNotFound
		}
		return nil, Entry{}, err
	}
	return obj, Entry{Path: p, Size: info.Size, ModTime: info.LastModified}, nil
}

// Create streams the upload through a pipe so a large file never sits in
// memory; the upload runs in the background until Commit.
func (b *S3) Create(ctx context.Context, p string, overwrite bool) (Writer, error) {
	key, err := b.key(p)
	if err != nil {
		return nil, err
	}
	if !overwrite {
		if _, err := b.Stat(ctx, p); err == nil {
			return nil, ErrExists
		}
	}
	pr, pw := io.Pipe()
	w := &s3Writer{pw: pw, done: make(chan error, 1)}
	go func() {
		err := b.upload(ctx, key, pr, !overwrite)
		if err != nil {
			pr.CloseWithError(err)
		} else {
			pr.Close()
		}
		w.done <- err
	}()
	return w, nil
}

// upload streams r to key. With exclusive set the object lands only if the
// key is still free when the upload finishes: the check in Create cannot
// promise that, since a whole file passes between it and the commit. The
// request that makes the object visible carries If-None-Match, which
// PutObject cannot do: it drops custom headers from the request completing
// a streaming upload.
func (b *S3) upload(ctx context.Context, key string, r io.Reader, exclusive bool) error {
	core := minio.Core{Client: b.client}
	land := func(write func(minio.PutObjectOptions) error) error {
		if exclusive {
			return ifAbsent(write)
		}
		return write(minio.PutObjectOptions{})
	}
	size := b.partSize
	if size == 0 {
		size = s3FirstPart
	}
	buf := make([]byte, size)
	n, err := io.ReadFull(r, buf)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return land(func(opts minio.PutObjectOptions) error {
			_, err := core.PutObject(ctx, b.bucket, key, bytes.NewReader(buf[:n]), int64(n), "", "", opts)
			return err
		})
	}
	if err != nil {
		return err
	}

	id, err := core.NewMultipartUpload(ctx, b.bucket, key, minio.PutObjectOptions{})
	if err != nil {
		return err
	}
	completed := false
	defer func() {
		if !completed {
			_ = core.AbortMultipartUpload(context.WithoutCancel(ctx), b.bucket, key, id)
		}
	}()
	var parts []minio.CompletePart
	for num := 1; ; num++ {
		part, err := core.PutObjectPart(ctx, b.bucket, key, id, num, bytes.NewReader(buf[:n]), int64(n), minio.PutObjectPartOptions{})
		if err != nil {
			return err
		}
		parts = append(parts, minio.CompletePart{PartNumber: num, ETag: part.ETag})
		if num == 1 && b.partSize == 0 {
			// Past one part the object can be any size, and S3 allows
			// 10,000 parts, so the rest are sized for the limit.
			_, rest, _, err := minio.OptimalPartInfo(-1, 0)
			if err != nil {
				return err
			}
			buf = make([]byte, rest)
		}
		n, err = io.ReadFull(r, buf)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return err
		}
	}
	err = land(func(opts minio.PutObjectOptions) error {
		_, err := core.CompleteMultipartUpload(ctx, b.bucket, key, id, parts, opts)
		return err
	})
	completed = err == nil
	return err
}

// ifAbsent runs the write that makes an object visible, on condition that
// the key is free. A store without conditional writes is asked again
// without the condition: for it, the check in Create is all there is, and
// refusing the transfer would be worse.
func ifAbsent(write func(minio.PutObjectOptions) error) error {
	var opts minio.PutObjectOptions
	opts.SetMatchETagExcept("*")
	err := write(opts)
	switch minio.ToErrorResponse(err).Code {
	case "PreconditionFailed":
		return ErrExists
	case "NotImplemented":
		return write(minio.PutObjectOptions{})
	}
	return err
}

type s3Writer struct {
	pw   *io.PipeWriter
	done chan error
	once sync.Once
	err  error
}

func (w *s3Writer) Write(p []byte) (int, error) { return w.pw.Write(p) }

func (w *s3Writer) Commit(modTime time.Time) error {
	w.once.Do(func() {
		w.pw.Close()
		w.err = <-w.done
	})
	return w.err
}

func (w *s3Writer) Abort() error {
	w.once.Do(func() {
		w.pw.CloseWithError(errors.New("aborted"))
		<-w.done
	})
	return nil
}

func (b *S3) Remove(ctx context.Context, p string) error {
	key, err := b.key(p)
	if err != nil {
		return err
	}
	return b.client.RemoveObject(ctx, b.bucket, key, minio.RemoveObjectOptions{})
}

func (b *S3) Close() error { return nil }
