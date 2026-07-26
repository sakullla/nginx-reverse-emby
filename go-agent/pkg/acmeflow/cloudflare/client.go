package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
)

const (
	DefaultAPIBaseURL = "https://api.cloudflare.com/client/v4"
	DefaultAPITimeout = 30 * time.Second
	DefaultRecordTTL  = 120

	maxProviderResponseBytes = 1 << 20
	maxProviderPages         = 1000
)

type ClientConfig struct {
	DNSAPIToken  string
	ZoneAPIToken string
	BaseURL      string
	HTTPClient   *http.Client
	APITimeout   time.Duration
	Now          func() time.Time
}

type Client struct {
	dnsToken   string
	zoneToken  string
	baseURL    *url.URL
	httpClient *http.Client
	apiTimeout time.Duration
	now        func() time.Time
}

type Zone struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type TXTRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Type    string `json:"type"`
}

func NewClient(config ClientConfig) (*Client, error) {
	dnsToken, err := validateToken(config.DNSAPIToken)
	if err != nil {
		return nil, providerError(acmeflow.CategoryAuthorization, "cloudflare_config", err)
	}
	zoneToken := strings.TrimSpace(config.ZoneAPIToken)
	if zoneToken == "" {
		zoneToken = dnsToken
	} else if zoneToken, err = validateToken(zoneToken); err != nil {
		return nil, providerError(acmeflow.CategoryAuthorization, "cloudflare_config", err)
	}
	baseURL := strings.TrimSpace(config.BaseURL)
	if baseURL == "" {
		baseURL = DefaultAPIBaseURL
	}
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil || parsedBaseURL.Host == "" || (parsedBaseURL.Scheme != "https" && parsedBaseURL.Scheme != "http") || parsedBaseURL.User != nil || parsedBaseURL.RawQuery != "" || parsedBaseURL.Fragment != "" {
		return nil, providerError(acmeflow.CategoryProtocol, "cloudflare_config", errors.New("Cloudflare API URL is invalid"))
	}
	parsedBaseURL.Path = strings.TrimRight(parsedBaseURL.Path, "/")
	parsedBaseURL.RawPath = ""

	apiTimeout := config.APITimeout
	if apiTimeout == 0 {
		apiTimeout = DefaultAPITimeout
	}
	if apiTimeout < 0 || apiTimeout > 10*time.Minute {
		return nil, providerError(acmeflow.CategoryProtocol, "cloudflare_config", errors.New("Cloudflare API timeout is invalid"))
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	clientCopy := *httpClient
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &Client{
		dnsToken:   dnsToken,
		zoneToken:  zoneToken,
		baseURL:    parsedBaseURL,
		httpClient: &clientCopy,
		apiTimeout: apiTimeout,
		now:        now,
	}, nil
}

func (client *Client) FindZone(ctx context.Context, fqdn string) (Zone, error) {
	const operation = "cloudflare_zone_lookup"
	if client == nil {
		return Zone{}, providerError(acmeflow.CategoryProtocol, operation, errors.New("Cloudflare client is nil"))
	}
	if err := contextFailure(ctx, operation); err != nil {
		return Zone{}, err
	}
	name, err := normalizeLookupName(fqdn)
	if err != nil {
		return Zone{}, providerError(acmeflow.CategoryChallenge, operation, err)
	}
	labels := strings.Split(name, ".")
	for index := 0; index < len(labels); index++ {
		candidate := strings.Join(labels[index:], ".")
		zones, err := client.listZones(ctx, candidate)
		if err != nil {
			return Zone{}, err
		}
		var matches []Zone
		for _, zone := range zones {
			zoneName, normalizeErr := normalizeDNSName(zone.Name)
			if normalizeErr != nil || zoneName != candidate || !validProviderID(zone.ID) {
				continue
			}
			zone.Name = zoneName
			matches = append(matches, zone)
		}
		if len(matches) == 0 {
			continue
		}
		if len(matches) != 1 {
			return Zone{}, providerError(acmeflow.CategoryChallenge, operation, errors.New("Cloudflare zone lookup was ambiguous"))
		}
		return matches[0], nil
	}
	return Zone{}, providerError(acmeflow.CategoryChallenge, operation, errors.New("Cloudflare zone was not found"))
}

func (client *Client) ListTXTRecords(ctx context.Context, zoneID, fqdn string) ([]TXTRecord, error) {
	const operation = "cloudflare_record_list"
	if client == nil {
		return nil, providerError(acmeflow.CategoryProtocol, operation, errors.New("Cloudflare client is nil"))
	}
	if err := contextFailure(ctx, operation); err != nil {
		return nil, err
	}
	if !validProviderID(zoneID) {
		return nil, providerError(acmeflow.CategoryChallenge, operation, errors.New("Cloudflare zone identifier is invalid"))
	}
	name, err := normalizeDNSName(fqdn)
	if err != nil {
		return nil, providerError(acmeflow.CategoryChallenge, operation, err)
	}
	var records []TXTRecord
	for page := 1; ; page++ {
		query := url.Values{
			"type":       {"TXT"},
			"name.exact": {name},
			"match":      {"all"},
			"page":       {fmt.Sprint(page)},
			"per_page":   {"100"},
		}
		var batch []TXTRecord
		info, err := client.requestJSON(ctx, client.dnsToken, http.MethodGet, fmt.Sprintf("zones/%s/dns_records", zoneID), query, nil, &batch, operation)
		if err != nil {
			return nil, err
		}
		for _, record := range batch {
			recordName, normalizeErr := normalizeDNSName(record.Name)
			if normalizeErr != nil || !validProviderID(record.ID) || len(record.Content) > 4096 || strings.ContainsRune(record.Content, '\x00') {
				return nil, providerError(acmeflow.CategoryProtocol, operation, errProviderResponse)
			}
			if recordName != name || record.Type != "TXT" {
				continue
			}
			record.Name = recordName
			record.Content = canonicalCloudflareTXTContent(record.Content)
			records = append(records, record)
		}
		if !validProviderPage(page, info, len(batch)) {
			return nil, providerError(acmeflow.CategoryProtocol, operation, errProviderResponse)
		}
		if !hasNextPage(page, info) {
			break
		}
		if page >= maxProviderPages {
			return nil, providerError(acmeflow.CategoryProtocol, operation, errors.New("Cloudflare pagination limit exceeded"))
		}
	}
	return records, nil
}

func (client *Client) CreateTXTRecord(ctx context.Context, zoneID, fqdn, content string) (TXTRecord, error) {
	const operation = "cloudflare_record_create"
	if client == nil {
		return TXTRecord{}, providerError(acmeflow.CategoryProtocol, operation, errors.New("Cloudflare client is nil"))
	}
	if err := contextFailure(ctx, operation); err != nil {
		return TXTRecord{}, err
	}
	if !validProviderID(zoneID) {
		return TXTRecord{}, providerError(acmeflow.CategoryChallenge, operation, errors.New("Cloudflare zone identifier is invalid"))
	}
	name, err := normalizeDNSName(fqdn)
	if err != nil || !validDNS01TXTValue(content) {
		return TXTRecord{}, providerError(acmeflow.CategoryChallenge, operation, errors.New("Cloudflare TXT record is invalid"))
	}
	providerContent := `"` + content + `"`
	input := struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Content string `json:"content"`
		TTL     int    `json:"ttl"`
	}{Type: "TXT", Name: name, Content: providerContent, TTL: DefaultRecordTTL}
	var record TXTRecord
	if _, err := client.requestJSON(ctx, client.dnsToken, http.MethodPost, fmt.Sprintf("zones/%s/dns_records", zoneID), nil, input, &record, operation); err != nil {
		return TXTRecord{}, err
	}
	recordName, err := normalizeDNSName(record.Name)
	if err != nil || recordName != name || canonicalCloudflareTXTContent(record.Content) != content || record.Type != "TXT" || record.TTL != DefaultRecordTTL || !validProviderID(record.ID) {
		return TXTRecord{}, providerError(acmeflow.CategoryProtocol, operation, errProviderResponse)
	}
	record.Name = recordName
	record.Content = content
	return record, nil
}

func (client *Client) DeleteRecord(ctx context.Context, zoneID, recordID string) error {
	const operation = "cloudflare_record_delete"
	if client == nil {
		return providerError(acmeflow.CategoryProtocol, operation, errors.New("Cloudflare client is nil"))
	}
	if err := contextFailure(ctx, operation); err != nil {
		return err
	}
	if !validProviderID(zoneID) || !validProviderID(recordID) {
		return providerError(acmeflow.CategoryCleanup, operation, errors.New("Cloudflare record identifier is invalid"))
	}
	var result struct {
		ID string `json:"id"`
	}
	_, err := client.requestJSON(ctx, client.dnsToken, http.MethodDelete, fmt.Sprintf("zones/%s/dns_records/%s", zoneID, recordID), nil, nil, &result, operation)
	if errors.Is(err, errRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if result.ID != "" && result.ID != recordID {
		return providerError(acmeflow.CategoryProtocol, operation, errProviderResponse)
	}
	return nil
}

func (client *Client) listZones(ctx context.Context, name string) ([]Zone, error) {
	const operation = "cloudflare_zone_lookup"
	var zones []Zone
	for page := 1; ; page++ {
		query := url.Values{
			"name":     {name},
			"page":     {fmt.Sprint(page)},
			"per_page": {"50"},
		}
		var batch []Zone
		info, err := client.requestJSON(ctx, client.zoneToken, http.MethodGet, "zones", query, nil, &batch, operation)
		if err != nil {
			return nil, err
		}
		zones = append(zones, batch...)
		if !validProviderPage(page, info, len(batch)) {
			return nil, providerError(acmeflow.CategoryProtocol, operation, errProviderResponse)
		}
		if !hasNextPage(page, info) {
			break
		}
		if page >= maxProviderPages {
			return nil, providerError(acmeflow.CategoryProtocol, operation, errors.New("Cloudflare pagination limit exceeded"))
		}
	}
	sort.SliceStable(zones, func(i, j int) bool { return len(zones[i].Name) > len(zones[j].Name) })
	return zones, nil
}

type providerResultInfo struct {
	Page       int `json:"page"`
	TotalPages int `json:"total_pages"`
}

type providerEnvelope struct {
	Success    bool               `json:"success"`
	Result     json.RawMessage    `json:"result"`
	ResultInfo providerResultInfo `json:"result_info"`
}

func (client *Client) requestJSON(
	ctx context.Context,
	token string,
	method string,
	path string,
	query url.Values,
	input any,
	result any,
	operation string,
) (providerResultInfo, error) {
	var info providerResultInfo
	if err := contextFailure(ctx, operation); err != nil {
		return info, err
	}
	requestContext, cancel := context.WithTimeout(ctx, client.apiTimeout)
	defer cancel()

	endpoint := *client.baseURL
	endpoint.Path = strings.TrimRight(client.baseURL.Path, "/") + "/" + strings.TrimLeft(path, "/")
	endpoint.RawPath = ""
	if query != nil {
		endpoint.RawQuery = query.Encode()
	}
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return info, providerError(acmeflow.CategoryProtocol, operation, errors.New("Cloudflare request could not be encoded"))
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(requestContext, method, endpoint.String(), body)
	if err != nil {
		return info, providerError(acmeflow.CategoryProtocol, operation, errors.New("Cloudflare request could not be created"))
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		if requestContext.Err() != nil {
			return info, providerError("", operation, requestContext.Err())
		}
		return info, providerError(acmeflow.CategoryNetwork, operation, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes+1))
	if err != nil {
		return info, providerError(acmeflow.CategoryNetwork, operation, errors.New("Cloudflare response could not be read"))
	}
	if len(data) > maxProviderResponseBytes {
		return info, providerError(acmeflow.CategoryProtocol, operation, errors.New("Cloudflare response is too large"))
	}
	if response.StatusCode == http.StatusNotFound && method == http.MethodDelete {
		return info, errRecordNotFound
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defaultCategory := acmeflow.CategoryChallenge
		if method == http.MethodDelete {
			defaultCategory = acmeflow.CategoryCleanup
		}
		return info, providerHTTPError(operation, response.StatusCode, response.Header.Get("Retry-After"), client.now(), defaultCategory)
	}
	var rawEnvelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawEnvelope); err != nil {
		return info, providerError(acmeflow.CategoryProtocol, operation, errProviderResponse)
	}
	var envelope providerEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil || len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return info, providerError(acmeflow.CategoryProtocol, operation, errProviderResponse)
	}
	if successJSON, exists := rawEnvelope["success"]; exists {
		if err := json.Unmarshal(successJSON, &envelope.Success); err != nil || !envelope.Success {
			return info, providerError(acmeflow.CategoryProtocol, operation, errProviderResponse)
		}
	} else if method != http.MethodDelete {
		return info, providerError(acmeflow.CategoryProtocol, operation, errProviderResponse)
	}
	if result != nil {
		if err := json.Unmarshal(envelope.Result, result); err != nil {
			return info, providerError(acmeflow.CategoryProtocol, operation, errProviderResponse)
		}
	}
	return envelope.ResultInfo, nil
}

func hasNextPage(page int, info providerResultInfo) bool {
	return info.TotalPages > page
}

func validProviderPage(page int, info providerResultInfo, resultCount int) bool {
	if page <= 0 || info.Page != page || info.TotalPages < 0 || info.TotalPages > maxProviderPages || resultCount < 0 {
		return false
	}
	if info.TotalPages == 0 {
		return page == 1 && resultCount == 0
	}
	return info.TotalPages >= page
}

func canonicalCloudflareTXTContent(content string) string {
	if len(content) >= 2 && content[0] == '"' && content[len(content)-1] == '"' {
		candidate := content[1 : len(content)-1]
		if validDNS01TXTValue(candidate) {
			return candidate
		}
	}
	return content
}

func validDNS01TXTValue(value string) bool {
	if value == "" || len(value) > 4096 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func validateToken(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("Cloudflare API token is invalid")
	}
	return value, nil
}

func validProviderID(value string) bool {
	if value == "" || len(value) > 32 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func normalizeLookupName(value string) (string, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "*.")
	return normalizeDNSName(value)
}
