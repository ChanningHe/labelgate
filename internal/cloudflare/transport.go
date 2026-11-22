package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	cf "github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/option"
)

// alreadyExistsCodes are Cloudflare error codes meaning "an equivalent object exists".
var alreadyExistsCodes = []int{
	81053, // an A, AAAA or CNAME record with that host already exists
	81054, // a CNAME cannot be created: an A, AAAA or CNAME record with that host exists
	81057, // identical record already exists
	81058, // a record with the same settings already exists
}

// maxPages guards list loops against servers that ignore the page parameter.
const maxPages = 10000

// request is one Cloudflare API call.
type request struct {
	op     string // e.g. "dns.create"
	method string
	path   string // relative to the API base, may include a query string
	body   any    // nil or []byte (JSON)
	out    any    // pointer the JSON response is decoded into; must be non-nil
}

// call executes r with the per-request timeout and the retry policy.
func (c *Client) call(ctx context.Context, cr *credEntry, r request) error {
	for attempt := 0; ; attempt++ {
		resp, err := c.attempt(ctx, cr, r)
		if err == nil {
			c.setHealth(Health{Reachable: true, LastCheck: c.now()})
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return &APIError{Op: r.op, Err: ctxErr, Credential: cr.name}
		}
		apiErr := toAPIError(r.op, resp, err)
		apiErr.Credential = cr.name
		c.recordFailure(apiErr)
		if attempt+1 >= c.retry.Attempts || !retryable(r.method, apiErr) {
			return apiErr
		}
		delay := c.backoff(attempt, resp)
		c.log.Debug("cloudflare request failed, retrying",
			"op", r.op, "attempt", attempt+1, "status", apiErr.Status, "delay", delay, "error", apiErr.Error())
		if err := c.sleep(ctx, delay); err != nil {
			return &APIError{Op: r.op, Err: err, Credential: cr.name}
		}
	}
}

// attempt performs a single HTTP request bounded by the per-request timeout.
func (c *Client) attempt(ctx context.Context, cr *credEntry, r request) (*http.Response, error) {
	actx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var resp *http.Response
	err := cr.sdk.Execute(actx, r.method, r.path, r.body, r.out, option.WithResponseInto(&resp))
	if err != nil && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		err = fmt.Errorf("request timed out after %s: %w", c.timeout, err)
	}
	return resp, err
}

// recordFailure updates Health: transport errors and 5xx mean unreachable.
func (c *Client) recordFailure(e *APIError) {
	if e.Status != 0 && e.Status < http.StatusInternalServerError {
		c.setHealth(Health{Reachable: true, LastCheck: c.now()})
		return
	}
	c.setHealth(Health{Reachable: false, LastCheck: c.now(), Error: e.Error()})
}

// toAPIError converts an SDK/transport error into *APIError with a sentinel.
func toAPIError(op string, resp *http.Response, err error) *APIError {
	e := &APIError{Op: op, Err: err}
	var cfErr *cf.Error
	switch {
	case errors.As(err, &cfErr):
		e.Status = cfErr.StatusCode
		for _, d := range cfErr.Errors {
			e.Codes = append(e.Codes, int(d.Code))
			e.Messages = append(e.Messages, d.Message)
		}
		e.Err = nil
	case resp != nil:
		// Non-JSON error body (e.g. an HTML 502 page) or undecodable success body.
		e.Status = resp.StatusCode
		e.Messages = []string{err.Error()}
	}
	if s := sentinelFor(e.Status, e.Codes); s != nil {
		e.Err = s
	}
	return e
}

func sentinelFor(status int, codes []int) error {
	for _, code := range codes {
		if slices.Contains(alreadyExistsCodes, code) {
			return ErrAlreadyExists
		}
	}
	switch status {
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrUnauthorized
	}
	return nil
}

// retryable reports whether a failed request may be repeated. POST is not
// idempotent (a lost response could duplicate an object), so it is only
// retried when the request was certainly not processed: 429 or a dial failure.
func retryable(method string, e *APIError) bool {
	if e.Status == http.StatusTooManyRequests {
		// An auth lockout only ends when the token stops failing; retrying
		// the same bad token would extend it.
		return !slices.Contains(e.Codes, codeAuthLockout)
	}
	if method == http.MethodPost {
		var opErr *net.OpError
		return errors.As(e.Err, &opErr) && opErr.Op == "dial"
	}
	return e.Status == 0 || e.Status >= http.StatusInternalServerError
}

// backoff returns the delay before retry number attempt+1: exponential with
// jitter in [d/2, d], or the server's Retry-After if longer; capped at MaxDelay.
func (c *Client) backoff(attempt int, resp *http.Response) time.Duration {
	factor := max(c.retry.Backoff, 1)
	d := time.Duration(float64(c.retry.Delay) * math.Pow(factor, float64(attempt)))
	if c.retry.MaxDelay > 0 && (d > c.retry.MaxDelay || d < 0) {
		d = c.retry.MaxDelay
	}
	if half := d / 2; half > 0 {
		d = half + rand.N(half+1)
	}
	if ra, ok := retryAfter(resp); ok && ra > d {
		d = ra
	}
	if c.retry.MaxDelay > 0 && d > c.retry.MaxDelay {
		d = c.retry.MaxDelay
	}
	return d
}

// retryAfter parses a Retry-After header (seconds or HTTP date).
func retryAfter(resp *http.Response) (time.Duration, bool) {
	if resp == nil {
		return 0, false
	}
	v := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil && secs >= 0 {
		return time.Duration(secs * float64(time.Second)), true
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(time.Until(t), 0), true
	}
	return 0, false
}

// sleepCtx waits for d or until ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
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

// pageEnvelope is a Cloudflare v4 list response.
type pageEnvelope[T any] struct {
	Result     []T `json:"result"`
	ResultInfo struct {
		Page       int `json:"page"`
		PerPage    int `json:"per_page"`
		Count      int `json:"count"`
		TotalCount int `json:"total_count"`
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}

// lastPage reports whether no further page should be requested.
func (p *pageEnvelope[T]) lastPage(page, seen int) bool {
	info := p.ResultInfo
	switch {
	case len(p.Result) == 0:
		return true
	case info.TotalPages > 0:
		return page >= info.TotalPages
	case info.TotalCount > 0:
		return seen >= info.TotalCount
	case info.PerPage == 0:
		return true // endpoint without paging metadata: single page
	default:
		return len(p.Result) < info.PerPage
	}
}

// listAll fetches every page of a list endpoint, honouring ctx on each page.
func listAll[T any](ctx context.Context, c *Client, cr *credEntry, op, path string, perPage int) ([]T, error) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	var out []T
	for page := 1; page <= maxPages; page++ {
		var env pageEnvelope[T]
		p := fmt.Sprintf("%s%spage=%d&per_page=%d", path, sep, page, perPage)
		if err := c.call(ctx, cr, request{op: op, method: http.MethodGet, path: p, out: &env}); err != nil {
			return nil, err
		}
		out = append(out, env.Result...)
		if env.lastPage(page, len(out)) {
			return out, nil
		}
	}
	return nil, &APIError{Op: op, Messages: []string{fmt.Sprintf("more than %d pages", maxPages)}, Credential: cr.name}
}

// envelope is a Cloudflare v4 single-object response.
type envelope[T any] struct {
	Result T `json:"result"`
}
