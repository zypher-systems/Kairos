package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/zypher-systems/kairos/internal/protocol"
)

// ErrStaleClaim is returned when the server no longer recognises this
// agent's claim on a run: it was canceled, or its lease lapsed and the run
// was handed to another worker.
var ErrStaleClaim = errors.New("stale claim")

// StatusError is a non-success HTTP answer from the server.
type StatusError struct {
	Op   string
	Code int
	Body string
}

func (e *StatusError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("%s: status %d: %s", e.Op, e.Code, e.Body)
	}
	return fmt.Sprintf("%s: status %d", e.Op, e.Code)
}

// Permanent reports whether retrying the same request is pointless.
func (e *StatusError) Permanent() bool { return e.Code >= 400 && e.Code < 500 }

func statusErr(op string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return &StatusError{Op: op, Code: resp.StatusCode, Body: strings.TrimSpace(string(body))}
}

// Client talks to the kairos server agent API.
type Client struct {
	base      string
	token     string
	hc        *http.Client
	userAgent string
	version   string
}

// NewClient builds an agent API client.
func NewClient(serverURL, token, version string) *Client {
	return &Client{
		base:      strings.TrimRight(serverURL, "/"),
		token:     token,
		hc:        &http.Client{Timeout: 45 * time.Second},
		userAgent: "kairos-agent/" + version,
		version:   version,
	}
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.hc.Do(req)
}

// Register sends attributes; returns the agent id.
func (c *Client) Register(ctx context.Context, name, hostname string, labels map[string]string) (string, error) {
	payload := protocol.AgentRegisterRequest{
		Name:         name,
		Hostname:     hostname,
		Version:      c.version,
		Labels:       labels,
		Capabilities: DetectCapabilities(),
	}
	resp, err := c.do(ctx, "POST", "/api/v1/agent/register", protocol.Marshal(payload))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", statusErr("register", resp)
	}
	var info protocol.AgentInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", err
	}
	return info.ID, nil
}

// Heartbeat reports liveness and returns any server instructions.
func (c *Client) Heartbeat(ctx context.Context, agentID string, load int) (*protocol.HeartbeatResponse, error) {
	resp, err := c.do(ctx, "POST", "/api/v1/agent/heartbeat", protocol.Marshal(protocol.HeartbeatRequest{AgentID: agentID, Load: load}))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusErr("heartbeat", resp)
	}
	var out protocol.HeartbeatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Claim long-polls for the next run; returns nil when the poll expired.
func (c *Client) Claim(ctx context.Context, agentID string) (*protocol.RunSpec, error) {
	resp, err := c.do(ctx, "POST", "/api/v1/agent/claim", protocol.Marshal(protocol.ClaimRequest{AgentID: agentID}))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusErr("claim", resp)
	}
	var out protocol.ClaimResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Run, nil
}

// SendLogs uploads a batch of log chunks. A *StatusError with Permanent()
// set means the batch will never be accepted and should be dropped.
func (c *Client) SendLogs(ctx context.Context, runID string, chunks []protocol.LogChunk) error {
	body := protocol.Marshal(protocol.LogBatch{Chunks: chunks})
	resp, err := c.do(ctx, "POST", "/api/v1/agent/runs/"+runID+"/logs", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return statusErr("logs", resp)
	}
	return nil
}

// RenewLease extends the claim lease during long executions. ErrStaleClaim
// means the server no longer holds this run for us.
func (c *Client) RenewLease(ctx context.Context, runID string, epoch int) error {
	resp, err := c.do(ctx, "POST", "/api/v1/agent/runs/"+runID+"/lease", protocol.Marshal(protocol.LeaseRequest{ClaimEpoch: epoch}))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusConflict:
		return ErrStaleClaim
	}
	return statusErr("lease", resp)
}

// ReportWatch notifies the server that a watched glob's matched set changed.
func (c *Client) ReportWatch(ctx context.Context, jobID uuid.UUID, sig string, files []string) error {
	body := protocol.Marshal(protocol.WatchEvent{JobID: jobID, Sig: sig, Files: files})
	resp, err := c.do(ctx, "POST", "/api/v1/agent/watch-event", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusNoContent {
		return statusErr("watch-event", resp)
	}
	return nil
}

// DownloadLibraryFile streams a library file to disk (push direction).
func (c *Client) DownloadLibraryFile(ctx context.Context, fileID, destPath string, overwrite bool) error {
	resp, err := c.do(ctx, "GET", "/api/v1/agent/files/"+fileID+"/content", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusErr("download", resp)
	}
	if !overwrite {
		if _, err := os.Stat(destPath); err == nil {
			return fmt.Errorf("destination %s exists and overwrite is off", destPath)
		}
	}
	if dir := filepath.Dir(destPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := destPath + ".kairos-part"
	fh, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(fh, resp.Body)
	closeErr := fh.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write: copy=%v close=%v", copyErr, closeErr)
	}
	return os.Rename(tmp, destPath)
}

// UploadFile streams a local file to the server library (pull direction).
func (c *Client) UploadFile(ctx context.Context, runID, srcPath, name string) error {
	fh, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer fh.Close()
	return c.UploadStream(ctx, runID, fh, name)
}

// UploadStream sends a reader to the server library under name.
func (c *Client) UploadStream(ctx context.Context, runID string, body io.Reader, name string) error {
	req, err := http.NewRequestWithContext(ctx, "POST",
		c.base+"/api/v1/agent/files?name="+urlQueryEscape(name)+"&run_id="+runID, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Content-Type", "application/octet-stream")
	// Large files take longer than the client's default timeout.
	hc := &http.Client{Timeout: 0}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return statusErr("upload", resp)
	}
	return nil
}

func urlQueryEscape(s string) string {
	r := strings.NewReplacer(
		"%", "%25", "&", "%26", "=", "%3D", "+", "%2B", " ", "%20", "/", "%2F",
	)
	return r.Replace(s)
}

// Complete reports the final run state. ErrStaleClaim means the server has
// already given the run to someone else; anything else is retryable.
func (c *Client) Complete(ctx context.Context, runID, status string, exitCode *int, errMsg string, epoch int) error {
	body := protocol.Marshal(protocol.CompleteRequest{Status: status, ExitCode: exitCode, Error: errMsg, ClaimEpoch: epoch})
	resp, err := c.do(ctx, "POST", "/api/v1/agent/runs/"+runID+"/complete", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusConflict:
		return ErrStaleClaim
	}
	return statusErr("complete", resp)
}
