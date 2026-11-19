package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"time"
)

// Only the fixed official DeepSeek hostname is sent to the DNS resolver.
// Model authentication and request content stay on the model connection.
func resolveDeepSeekPublicIP(ctx context.Context) ([]net.IPAddr, error) {
	const endpoint = "https://dns.alidns.com/resolve?name=api.deepseek.com&type=A"
	const maxResponse = 8 << 10
	transport := &http.Transport{
		DialContext:       (&net.Dialer{Timeout: 4 * time.Second}).DialContext,
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   6 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, failure(Unavailable)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, failure(Unavailable)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, failure(Unavailable)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil || len(body) > maxResponse {
		return nil, failure(Unavailable)
	}
	var answer struct {
		Status *int
		Answer []struct {
			Type int
			Data string
		}
	}
	if json.Unmarshal(body, &answer) != nil || answer.Status == nil || *answer.Status != 0 {
		return nil, failure(Unavailable)
	}
	var addresses []net.IPAddr
	for _, record := range answer.Answer {
		if record.Type == 1 {
			addresses = append(addresses, net.IPAddr{IP: net.ParseIP(record.Data)})
		}
	}
	return addresses, nil
}
