package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"log-download-portal/internal/config"
	"log-download-portal/internal/security"
)

type Client struct {
	http     *http.Client
	fileList config.FileListConfig
	download config.DownloadConfig
}

type File struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
}

type AutoIndexEntry struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	MTime string `json:"mtime"`
	Size  int64  `json:"size"`
}

var (
	ErrListTooLarge  = errors.New("file list too large")
	ErrBadAgentReply = errors.New("bad agent reply")
)

func NewClient(agentCfg config.AgentConfig, fileList config.FileListConfig, download config.DownloadConfig) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	return &Client{
		http: &http.Client{
			Timeout:   agentCfg.HTTPTimeout.Duration,
			Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		fileList: fileList,
		download: download,
	}
}

func (c *Client) ListFiles(ctx context.Context, baseURL, directory string) ([]File, error) {
	target, err := joinAgentURL(baseURL, directory, "")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return nil, osPathNotFound()
		}
		return nil, ErrBadAgentReply
	}
	limited := &io.LimitedReader{R: resp.Body, N: int64(c.fileList.MaxResponseBytes) + 1}
	files, err := decodeAutoIndex(limited, c.fileList.MaxItems)
	if err != nil {
		return nil, err
	}
	if limited.N <= 0 {
		return nil, ErrListTooLarge
	}
	return files, nil
}

func (c *Client) NewDownloadRequest(ctx context.Context, baseURL, directory, filename, rangeHeader string) (*http.Response, error) {
	target, err := joinAgentURL(baseURL, directory, filename)
	if err != nil {
		return nil, err
	}

	// Optional hard cap on the overall download duration. When set, the
	// deadline context is cancelled either on body close (via cancelOnClose)
	// or automatically when the timeout elapses.
	var cancel context.CancelFunc
	if c.download.MaxDuration.Duration > 0 {
		ctx, cancel = context.WithTimeout(ctx, c.download.MaxDuration.Duration)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	if cancel != nil {
		resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	}

	// If the agent ignored a Range request and returned the full content,
	// treat it as a complete download instead of failing the client (RFC 7233
	// permits a server to ignore Range). Other 4xx such as 416 still pass
	// through to the browser as-is.
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode >= 500 || isRedirect(resp.StatusCode) {
		resp.Body.Close()
		if cancel != nil {
			cancel()
		}
		return nil, ErrBadAgentReply
	}
	return resp, nil
}

func (c *Client) CopyDownload(w io.Writer, body io.ReadCloser) (int64, error) {
	defer body.Close()
	reader := &idleReadCloser{ReadCloser: body, timeout: c.download.BodyIdleTimeout.Duration}
	buf := make([]byte, c.download.BufferBytes)
	var written int64
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			m, writeErr := w.Write(buf[:n])
			written += int64(m)
			if writeErr != nil {
				return written, writeErr
			}
			if m != n {
				return written, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}

func decodeAutoIndex(r io.Reader, maxItems int) ([]File, error) {
	dec := json.NewDecoder(r)
	tok, err := dec.Token()
	if err != nil {
		return nil, ErrBadAgentReply
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '[' {
		return nil, ErrBadAgentReply
	}

	files := make([]File, 0)
	seen := 0
	for dec.More() {
		seen++
		if seen > maxItems {
			return nil, ErrListTooLarge
		}
		var entry AutoIndexEntry
		if err := dec.Decode(&entry); err != nil {
			return nil, ErrBadAgentReply
		}
		if entry.Type != "file" {
			continue
		}
		if entry.Size < 0 || security.ValidateFileName(entry.Name) != nil {
			continue
		}
		files = append(files, File{Name: entry.Name, Size: entry.Size, ModTime: entry.MTime})
	}
	if _, err := dec.Token(); err != nil {
		return nil, ErrBadAgentReply
	}
	return files, nil
}

func joinAgentURL(baseURL, directory, filename string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "http" || parsed.Host == "" {
		return "", fmt.Errorf("invalid agent base URL")
	}
	segments := []string{"logs", directory}
	escapedSegments := []string{"logs", url.PathEscape(directory)}
	if filename != "" {
		segments = append(segments, filename)
		escapedSegments = append(escapedSegments, url.PathEscape(filename))
	}
	parsed.Path = "/" + strings.Join(segments, "/")
	parsed.RawPath = "/" + strings.Join(escapedSegments, "/")
	if filename == "" {
		parsed.Path += "/"
		parsed.RawPath += "/"
	}
	return parsed.String(), nil
}

func isRedirect(status int) bool {
	return status >= 300 && status < 400
}

type idleReadCloser struct {
	io.ReadCloser
	timeout time.Duration
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

func (r *idleReadCloser) Read(p []byte) (int, error) {
	if r.timeout <= 0 {
		return r.ReadCloser.Read(p)
	}
	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		n, err := r.ReadCloser.Read(p)
		ch <- result{n: n, err: err}
	}()
	timer := time.NewTimer(r.timeout)
	defer timer.Stop()
	select {
	case result := <-ch:
		return result.n, result.err
	case <-timer.C:
		_ = r.ReadCloser.Close()
		return 0, context.DeadlineExceeded
	}
}

func osPathNotFound() error {
	return os.ErrNotExist
}
