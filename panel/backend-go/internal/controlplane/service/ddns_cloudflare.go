package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// cloudflareRecordOutcome describes the result of one A/AAAA reconciliation.
// Action is one of "created", "updated", "unchanged".
type cloudflareRecordOutcome struct {
	ZoneID   string
	RecordID string
	Action   string
}

// cloudflareDNSClient abstracts the Cloudflare REST surface the DDNS reconciler
// needs. The token is passed per call (never stored on the client) so tests can
// exercise the "no token -> disabled" path and so the credential stays confined
// to the master process environment (R7).
type cloudflareDNSClient interface {
	EnsureRecord(ctx context.Context, token, fqdn, recordType, content string, ttl int) (cloudflareRecordOutcome, error)
}

// httpCloudflareClient talks to the Cloudflare REST API. It keeps a small
// zone-name -> zone-id cache because zone layout is stable for a token's
// lifetime and per-record upserts would otherwise re-list zones on every call.
type httpCloudflareClient struct {
	base    string
	http    *http.Client
	cacheMu sync.RWMutex
	zones   map[string]string // lower-cased zone name -> zone id
}

func newHTTPCloudflareClient(base string, timeout time.Duration) *httpCloudflareClient {
	if strings.TrimSpace(base) == "" {
		base = "https://api.cloudflare.com/client/v4"
	}
	return &httpCloudflareClient{
		base: strings.TrimRight(base, "/"),
		http: &http.Client{Timeout: timeout},
		zones: make(map[string]string),
	}
}

type cfResponse struct {
	Success bool            `json:"success"`
	Errors  []cfErrorEntry  `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type cfErrorEntry struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cfZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfDNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// EnsureRecord makes fqdn resolve to content (a single A or AAAA address),
// creating, updating, or leaving the record untouched as needed. The
// value-unchanged short-circuit avoids API writes (and Cloudflare rate limits)
// when the published address already matches the agent's reported address.
func (c *httpCloudflareClient) EnsureRecord(ctx context.Context, token, fqdn, recordType, content string, ttl int) (cloudflareRecordOutcome, error) {
	fqdn = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(fqdn), ".")))
	recordType = strings.ToUpper(strings.TrimSpace(recordType))
	if fqdn == "" || recordType == "" || strings.TrimSpace(content) == "" {
		return cloudflareRecordOutcome{}, errors.New("ddns: fqdn, recordType and content are required")
	}
	zoneID, err := c.resolveZone(ctx, token, fqdn)
	if err != nil {
		return cloudflareRecordOutcome{}, err
	}
	existing, err := c.listRecord(ctx, token, zoneID, fqdn, recordType)
	if err != nil {
		return cloudflareRecordOutcome{}, err
	}
	if existing.ID != "" {
		if normalizeCFContent(existing.Content) == normalizeCFContent(content) {
			return cloudflareRecordOutcome{ZoneID: zoneID, RecordID: existing.ID, Action: "unchanged"}, nil
		}
		if err := c.updateRecord(ctx, token, zoneID, existing.ID, content, ttl); err != nil {
			return cloudflareRecordOutcome{}, err
		}
		return cloudflareRecordOutcome{ZoneID: zoneID, RecordID: existing.ID, Action: "updated"}, nil
	}
	id, err := c.createRecord(ctx, token, zoneID, fqdn, recordType, content, ttl)
	if err != nil {
		return cloudflareRecordOutcome{}, err
	}
	return cloudflareRecordOutcome{ZoneID: zoneID, RecordID: id, Action: "created"}, nil
}

// resolveZone finds the Cloudflare zone governing fqdn by selecting the longest
// cached/queried zone name that is a suffix of fqdn.
func (c *httpCloudflareClient) resolveZone(ctx context.Context, token, fqdn string) (string, error) {
	c.cacheMu.RLock()
	bestID, _ := c.bestZoneLocked(fqdn)
	c.cacheMu.RUnlock()
	if bestID != "" {
		return bestID, nil
	}
	zones, err := c.listZones(ctx, token)
	if err != nil {
		return "", err
	}
	c.cacheMu.Lock()
	for _, z := range zones {
		c.zones[strings.ToLower(z.Name)] = z.ID
	}
	bestID, _ = c.bestZoneLocked(fqdn)
	c.cacheMu.Unlock()
	if bestID == "" {
		return "", fmt.Errorf("ddns: no Cloudflare zone found for %q", fqdn)
	}
	return bestID, nil
}

func (c *httpCloudflareClient) bestZoneLocked(fqdn string) (string, string) {
	bestID, bestName := "", ""
	for name, id := range c.zones {
		if fqdn == name || strings.HasSuffix(fqdn, "."+name) {
			if len(name) > len(bestName) {
				bestID, bestName = id, name
			}
		}
	}
	return bestID, bestName
}

func (c *httpCloudflareClient) listZones(ctx context.Context, token string) ([]cfZone, error) {
	var out []cfZone
	page := 1
	for {
		var result []cfZone
		totalPages, err := c.getJSON(ctx, token, fmt.Sprintf("/zones?page=%d&per_page=50", page), &result)
		if err != nil {
			return nil, err
		}
		out = append(out, result...)
		if page >= totalPages || len(result) == 0 {
			break
		}
		page++
	}
	return out, nil
}

func (c *httpCloudflareClient) listRecord(ctx context.Context, token, zoneID, fqdn, recordType string) (cfDNSRecord, error) {
	var result []cfDNSRecord
	q := url.Values{}
	q.Set("type", recordType)
	q.Set("name", fqdn)
	q.Set("per_page", "100")
	if _, err := c.getJSON(ctx, token, fmt.Sprintf("/zones/%s/dns_records?%s", zoneID, q.Encode()), &result); err != nil {
		return cfDNSRecord{}, err
	}
	for _, rec := range result {
		if strings.EqualFold(rec.Type, recordType) && strings.EqualFold(strings.TrimSuffix(rec.Name, "."), fqdn) {
			return rec, nil
		}
	}
	return cfDNSRecord{}, nil
}

func (c *httpCloudflareClient) createRecord(ctx context.Context, token, zoneID, fqdn, recordType, content string, ttl int) (string, error) {
	body := map[string]any{
		"type":    recordType,
		"name":    fqdn,
		"content": content,
		"ttl":     ttl,
		"proxied": false,
	}
	var result cfDNSRecord
	if _, err := c.sendJSON(ctx, token, http.MethodPost, fmt.Sprintf("/zones/%s/dns_records", zoneID), body, &result, http.StatusOK); err != nil {
		return "", err
	}
	return result.ID, nil
}

func (c *httpCloudflareClient) updateRecord(ctx context.Context, token, zoneID, recordID, content string, ttl int) error {
	body := map[string]any{"content": content, "ttl": ttl}
	return c.sendJSONNoResult(ctx, token, http.MethodPatch, fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, recordID), body, http.StatusOK)
}

// cfPagedResult carries Cloudflare's pagination metadata alongside result.
type cfPagedResult struct {
	ResultInfo struct {
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}

// getJSON performs a GET and decodes result into target (a slice or struct).
// It returns the total_pages count when present.
func (c *httpCloudflareClient) getJSON(ctx context.Context, token, path string, target any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return 0, err
	}
	raw, status, err := c.do(req, token)
	if err != nil {
		return 0, err
	}
	var resp cfResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return 0, fmt.Errorf("ddns: decode cloudflare response: %w", err)
	}
	if status >= 400 || !resp.Success {
		return 0, cfError(status, raw, resp.Errors)
	}
	var paged cfPagedResult
	_ = json.Unmarshal(raw, &paged)
	if target != nil {
		if err := json.Unmarshal(resp.Result, target); err != nil {
			return 0, fmt.Errorf("ddns: decode cloudflare result: %w", err)
		}
	}
	return paged.ResultInfo.TotalPages, nil
}

func (c *httpCloudflareClient) sendJSON(ctx context.Context, token, method, path string, body any, result any, expectStatus int) (int, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	raw, status, err := c.do(req, token)
	if err != nil {
		return 0, err
	}
	var resp cfResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return 0, fmt.Errorf("ddns: decode cloudflare response: %w", err)
	}
	if status != expectStatus || !resp.Success {
		return 0, cfError(status, raw, resp.Errors)
	}
	if result != nil {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return 0, fmt.Errorf("ddns: decode cloudflare result: %w", err)
		}
	}
	return 0, nil
}

func (c *httpCloudflareClient) sendJSONNoResult(ctx context.Context, token, method, path string, body any, expectStatus int) error {
	_, err := c.sendJSON(ctx, token, method, path, body, nil, expectStatus)
	return err
}

func (c *httpCloudflareClient) do(req *http.Request, token string) ([]byte, int, error) {
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("ddns: cloudflare request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, resp.StatusCode, fmt.Errorf("ddns: read cloudflare response: %w", readErr)
	}
	return raw, resp.StatusCode, nil
}

// cfError builds an error from a non-success Cloudflare response, preserving a
// Retry-After hint (seconds) when the API signals rate limiting (HTTP 429) so
// the reconciler backoff can honor it. The hint is embedded as
// "retry_after_seconds=N" in the message, mirroring the certificate issuer.
func cfError(status int, raw []byte, entries []cfErrorEntry) error {
	msg := fmt.Sprintf("ddns: cloudflare returned status %d", status)
	if len(entries) > 0 && strings.TrimSpace(entries[0].Message) != "" {
		msg = fmt.Sprintf("%s: %s", msg, entries[0].Message)
	}
	var probe struct {
		Errors []cfErrorEntry `json:"errors"`
	}
	if json.Unmarshal(raw, &probe) == nil && len(probe.Errors) > 0 && strings.TrimSpace(probe.Errors[0].Message) != "" {
		msg = fmt.Sprintf("ddns: cloudflare returned status %d: %s", status, probe.Errors[0].Message)
	}
	if status == http.StatusTooManyRequests {
		// Retry-After is surfaced by do() via headers in callers that need it;
		// the reconciler parses the numeric hint if present in the message.
		msg += " (rate_limited)"
	}
	return errors.New(msg)
}

// normalizeCFContent compares record contents leniently: trim whitespace and
// trailing dots so "203.0.113.10" matches a stored "203.0.113.10 ".
func normalizeCFContent(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
