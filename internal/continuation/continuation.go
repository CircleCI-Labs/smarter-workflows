// Package continuation is the client of CircleCI's continue-pipeline API
// (POST /api/v2/pipeline/continue), which ends a setup workflow.
//
// The continuation key is a secret and the configuration can hold anything, so
// the client never logs or prints either, and its errors name the HTTP status
// and nothing else the server sent.
package continuation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultHost is the API host when none is set.
	DefaultHost = "https://circleci.com"

	// Path is the endpoint of the continuation API.
	Path = "/api/v2/pipeline/continue"

	// MaxConfigBytes is the largest configuration CircleCI accepts (8 MiB).
	MaxConfigBytes = 8 << 20

	// DefaultTimeout is how long one request may take.
	DefaultTimeout = 30 * time.Second

	// maxDrain bounds what is read of a response body before closing it.
	maxDrain = 1 << 20
)

// defaultBackoff is the wait before each retry: a request is tried once, then
// again after 1 s, after 2 s and after 4 s.
var defaultBackoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

var (
	// ErrNoKey is returned when the client has no continuation key.
	ErrNoKey = errors.New("no continuation key")
	// ErrNoConfig is returned when the configuration is empty.
	ErrNoConfig = errors.New("the configuration is empty")
)

// ConfigTooLargeError is returned, before anything is sent, when the
// configuration is over MaxConfigBytes.
type ConfigTooLargeError struct{ Size int }

func (e *ConfigTooLargeError) Error() string {
	return fmt.Sprintf("the configuration is %d bytes, over the limit of %d bytes", e.Size, MaxConfigBytes)
}

// StatusError is returned when the API answers with a status that is not 2xx,
// after the retries when the status allows them.
type StatusError struct{ Code int }

func (e *StatusError) Error() string {
	return fmt.Sprintf("the continuation API answered HTTP %d", e.Code)
}

// Client sends continuation requests. Only Key has no usable zero value.
type Client struct {
	// Host is the base URL of the API: DefaultHost if empty. It must be https,
	// or http on a loopback address (the key travels in the request body), and
	// carry no credentials.
	Host string
	// Key is the continuation key ($CIRCLE_CONTINUATION_KEY).
	Key string

	// HTTPClient sends the requests. If nil, the client is one that follows no
	// redirect, so that the key is never sent on to another host.
	HTTPClient *http.Client
	// Timeout limits each request: DefaultTimeout if zero.
	Timeout time.Duration
	// Backoff is the wait before each retry, so a request is tried
	// len(Backoff)+1 times. If nil, the waits are 1 s, 2 s and 4 s. If empty,
	// there are no retries.
	Backoff []time.Duration
	// UserAgent is sent as the User-Agent header, if set.
	UserAgent string

	// sleep waits between tries. Tests replace it.
	sleep func(context.Context, time.Duration) error
}

// request is the body of POST /api/v2/pipeline/continue.
type request struct {
	Key           string         `json:"continuation-key"`
	Configuration string         `json:"configuration"`
	Parameters    map[string]any `json:"parameters"`
}

// Continue sends the configuration (YAML) and the pipeline parameters, and
// returns the HTTP status of the last answer (0 if the server never answered).
//
// It tries again when the server answers 429 or 5xx, and when a request fails
// or times out. Any other status that is not 2xx ends it at once. No error it
// returns contains the continuation key.
func (c *Client) Continue(ctx context.Context, configuration []byte, parameters map[string]any) (int, error) {
	status, err := c.continueOnce(ctx, configuration, parameters)
	return status, c.redact(err)
}

func (c *Client) continueOnce(ctx context.Context, configuration []byte, parameters map[string]any) (int, error) {
	if c.Key == "" {
		return 0, ErrNoKey
	}
	if len(configuration) == 0 {
		return 0, ErrNoConfig
	}
	if len(configuration) > MaxConfigBytes {
		return 0, &ConfigTooLargeError{Size: len(configuration)}
	}
	endpoint, err := c.endpoint()
	if err != nil {
		return 0, err
	}
	body, err := c.encode(configuration, parameters)
	if err != nil {
		return 0, err
	}

	backoff := c.Backoff
	if backoff == nil {
		backoff = defaultBackoff
	}
	var (
		status int
		failed error
	)
	for try := 0; try <= len(backoff); try++ {
		if try > 0 {
			if err := c.wait(ctx, backoff[try-1]); err != nil {
				return status, err
			}
		}
		status, failed = c.post(ctx, endpoint, body)
		switch {
		case failed == nil && status >= 200 && status < 300:
			return status, nil
		case failed == nil && !retryable(status):
			return status, &StatusError{Code: status}
		case failed == nil:
			failed = &StatusError{Code: status}
		case ctx.Err() != nil:
			return status, ctx.Err()
		}
	}
	return status, failed
}

// retryable reports whether a status is worth another try.
func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// endpoint is the URL to post to.
func (c *Client) endpoint() (string, error) {
	host := c.Host
	if host == "" {
		host = DefaultHost
	}
	u, err := url.Parse(host)
	if err != nil || u.Hostname() == "" {
		return "", errors.New("the API host is not a URL with a scheme and a host, such as https://circleci.com")
	}
	if u.User != nil {
		return "", errors.New("the API host must not carry credentials")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopback(u.Hostname()) {
			return "", errors.New("the API host must be https: the continuation key would travel in clear text")
		}
	default:
		return "", fmt.Errorf("the API host has the scheme %q, want https", u.Scheme)
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/") + Path, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// encode builds the JSON body. HTML escaping is off, so that `<<` in the
// configuration stays as it is.
func (c *Client) encode(configuration []byte, parameters map[string]any) ([]byte, error) {
	if parameters == nil {
		parameters = map[string]any{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(request{Key: c.Key, Configuration: string(configuration), Parameters: parameters}); err != nil {
		return nil, fmt.Errorf("encode the request: %w", err)
	}
	return buf.Bytes(), nil
}

// post sends the request once, and returns the status of the answer.
func (c *Client) post(ctx context.Context, endpoint string, body []byte) (int, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("build the request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return 0, fmt.Errorf("the request timed out after %s", timeout)
		}
		return 0, fmt.Errorf("send the request: %w", err)
	}
	defer resp.Body.Close()
	// What the server says is not used, and never printed. Read a little of it
	// so that the connection can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrain))
	return resp.StatusCode, nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (c *Client) wait(ctx context.Context, d time.Duration) error {
	if c.sleep != nil {
		return c.sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// redacted is an error whose text has the continuation key replaced. It has
// no Unwrap, so the text of the original cannot be reached; errors.Is and
// errors.As still see through it.
type redacted struct {
	msg string
	err error
}

func (e *redacted) Error() string        { return e.msg }
func (e *redacted) Is(target error) bool { return errors.Is(e.err, target) }
func (e *redacted) As(target any) bool   { return errors.As(e.err, target) }

// redact makes sure the text of err cannot contain the key. The key is only in
// the request body, which no error of this package quotes, so this guards
// against a mistake of the future.
func (c *Client) redact(err error) error {
	if err == nil || c.Key == "" || !strings.Contains(err.Error(), c.Key) {
		return err
	}
	return &redacted{msg: strings.ReplaceAll(err.Error(), c.Key, "[redacted]"), err: err}
}
