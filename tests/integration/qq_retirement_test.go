package integration_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/qilinbro/msg-manager/internal/api"
	"github.com/qilinbro/msg-manager/internal/connector"
)

func TestRemovedOneBotEndpointIsUnavailableBehindAuthentication(t *testing.T) {
	h := newHandler(t)
	const retiredPath = "/api/v1/connections/qq-onebot/verify"
	checkError(t, request(h, http.MethodGet, retiredPath, nil), http.StatusUnauthorized, "UNAUTHORIZED")
	checkError(t, request(h, http.MethodGet, retiredPath, map[string]string{"Origin": "https://untrusted.example"}), http.StatusForbidden, "ORIGIN_DENIED")
	headers := map[string]string{"Authorization": "Bearer " + token}
	checkError(t, request(h, http.MethodGet, retiredPath, headers), http.StatusNotFound, "NOT_FOUND")
	checkError(t, request(h, http.MethodGet, retiredPath+"?url=http://untrusted.example&access_token=ignored", headers), http.StatusNotFound, "NOT_FOUND")
	// No implicit redirect or alias: an old client's verification must not probe
	// another account or silently become a Milky action.
	checkError(t, request(h, http.MethodGet, "/api/v1/connections/qq-milky/verify", headers), http.StatusServiceUnavailable, "MILKY_NOT_CONFIGURED")
}

func TestQQDiscoveryOnlyAdvertisesMilky(t *testing.T) {
	h := newHandler(t)
	headers := map[string]string{"Authorization": "Bearer " + token}
	system := request(h, http.MethodGet, "/api/v1/system", headers)
	if system.Code != http.StatusOK {
		t.Fatalf("system: %d", system.Code)
	}
	var status api.SystemResponse
	if err := json.Unmarshal(system.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	connections := request(h, http.MethodGet, "/api/v1/connections", headers)
	if connections.Code != http.StatusOK {
		t.Fatalf("connections: %d", connections.Code)
	}
	var list api.ListResponse[connector.Descriptor]
	if err := json.Unmarshal(connections.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	for _, items := range [][]connector.Descriptor{status.Connectors, list.Items} {
		qqCount := 0
		for _, item := range items {
			if item.ID == "qq-onebot" {
				t.Fatal("retired connector is still advertised")
			}
			if item.Platform != "qq" {
				continue
			}
			qqCount++
			if item.ID != "qq-milky" || item.Status != "not_configured" || item.Capabilities.SendText {
				t.Fatalf("incorrect QQ descriptor: %+v", item)
			}
		}
		if qqCount != 1 {
			t.Fatalf("QQ entries = %d; want exactly one", qqCount)
		}
	}
	if status.Automation.AutoSendEnabled {
		t.Fatal("removing a connector enabled automatic sending")
	}
}
