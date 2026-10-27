package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// The batch endpoint commits all writes in one database transaction. A failed
// replacement must never leave the previous records deleted. DNS propagation
// remains asynchronous. See Cloudflare's batch-record-changes documentation.
func (s *CloudflareAPI) replaceTunnelDNS(hostname, tunnelID string) (*DNSReplaceResult, error) {
	host := cleanHostname(hostname)
	if host == "" || tunnelID == "" || tunnelID == "your-tunnel-uuid" || strings.ContainsAny(tunnelID, "/?#. \t\r\n") {
		return nil, fmt.Errorf("valid hostname and tunnel ID are required")
	}
	creds := s.GetCredentials()
	if creds.APIToken == "" {
		return nil, fmt.Errorf("Cloudflare API Token required to replace DNS records")
	}
	if isConnectorToken(creds.APIToken) {
		return nil, fmt.Errorf("the configured token is a Tunnel Connector Token; save it under Tunnel connector token and use a Cloudflare User API Token with Zone:DNS:Edit here")
	}
	zone, zoneErr := s.FindZoneID(creds.APIToken, host)
	if zone == "" {
		zone = creds.ZoneID
	}
	if zone == "" {
		return nil, fmt.Errorf("could not resolve Cloudflare zone for %s: %w", host, zoneErr)
	}
	endpoint := "https://api.cloudflare.com/client/v4/zones/" + url.PathEscape(zone) + "/dns_records"
	target := tunnelID + ".cfargotunnel.com"
	var records []cfDNSRecord
	for page := 1; ; page++ {
		if page > 100 {
			return nil, fmt.Errorf("too many DNS record pages; no records changed")
		}
		query := url.Values{"name": {host}, "per_page": {"100"}, "page": {fmt.Sprint(page)}}
		response, err := s.dnsRequest(http.MethodGet, endpoint+"?"+query.Encode(), creds.APIToken, host, nil)
		if err != nil {
			return nil, fmt.Errorf("query DNS records: %w", err)
		}
		var found []cfDNSRecord
		if err := json.Unmarshal(response.Result, &found); err != nil {
			return nil, fmt.Errorf("invalid DNS records response; no records changed")
		}
		for _, record := range found {
			if record.ID == "" || !strings.EqualFold(strings.TrimSuffix(record.Name, "."), strings.TrimSuffix(host, ".")) {
				return nil, fmt.Errorf("DNS query returned an unexpected record; no records changed")
			}
		}
		records = append(records, found...)
		if page >= response.ResultInfo.TotalPages {
			break
		}
	}
	result := &DNSReplaceResult{Hostname: host, ZoneID: zone, CreatedTarget: target, DeletedRecords: []string{}}
	if len(records) == 1 && records[0].Type == "CNAME" && strings.EqualFold(strings.TrimSuffix(records[0].Content, "."), target) && records[0].Proxied {
		result.Success = true
		result.Message = fmt.Sprintf("%s already points to %s (Proxied).", host, target)
		return result, nil
	}
	deletes := make([]map[string]string, 0)
	for _, record := range records {
		// TXT, MX, verification and other non-address records are never purged.
		switch record.Type {
		case "A", "AAAA", "CNAME":
			deletes = append(deletes, map[string]string{"id": record.ID})
			result.DeletedRecords = append(result.DeletedRecords, fmt.Sprintf("%s (%s -> %s)", record.ID, record.Type, record.Content))
		}
	}
	payload := map[string]interface{}{
		"deletes": deletes,
		"posts":   []map[string]interface{}{{"type": "CNAME", "name": host, "content": target, "proxied": true, "ttl": 1, "comment": "Managed by DockTun"}},
	}
	if _, err := s.dnsRequest(http.MethodPost, endpoint+"/batch", creds.APIToken, host, payload); err != nil {
		return nil, fmt.Errorf("replace DNS records: %w", err)
	}
	result.Success = true
	result.Message = fmt.Sprintf("Configured %s -> %s (Proxied). Replaced %d address record(s).", host, target, len(deletes))
	return result, nil
}

type dnsAPIResponse struct {
	cfApiResponse
	ResultInfo struct {
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}

func (s *CloudflareAPI) dnsRequest(method, endpoint, token, hostname string, payload interface{}) (dnsAPIResponse, error) {
	var result dnsAPIResponse
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return result, err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return result, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return result, fmt.Errorf("Cloudflare request failed; check DNS status before retrying: %w", err)
	}
	defer response.Body.Close()
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result); err != nil {
		return result, fmt.Errorf("invalid Cloudflare response (HTTP %d)", response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !result.Success {
		message := formatCfErrorMessage(result.Errors, "Cloudflare rejected the DNS request", hostname)
		if response.StatusCode == 401 || response.StatusCode == 403 {
			message += "; use a User API Token with Zone:DNS:Edit for this zone and Zone:Zone:Read for automatic zone discovery"
		}
		return result, fmt.Errorf("%s (HTTP %d)", message, response.StatusCode)
	}
	return result, nil
}

// cloudflared encodes its a/s/t credential fields as Base64 JSON, not a JWT.
// Check their contents instead of rejecting every API credential with an eyJ prefix.
func isConnectorToken(token string) bool {
	var raw []byte
	var err error
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		raw, err = encoding.DecodeString(token)
		if err == nil {
			break
		}
	}
	if err != nil {
		return false
	}
	var fields struct {
		Account string `json:"a"`
		Secret  string `json:"s"`
		Tunnel  string `json:"t"`
	}
	return json.Unmarshal(raw, &fields) == nil && fields.Account != "" && fields.Secret != "" && fields.Tunnel != ""
}
