package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func TestServerRejectsHeaderValueFlood(t *testing.T) {
	testServer, _ := StartTestServer(t, GetDBType())

	tests := []struct {
		name        string
		headerCount int
		wantStatus  int
	}{
		{name: "ordinary request", headerCount: 64, wantStatus: http.StatusOK},
		{name: "header flood", headerCount: 128, wantStatus: http.StatusRequestHeaderFieldsTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, testServer.BaseURL+"/healthz", nil)
			if err != nil {
				t.Fatalf("create request: %v", err)
			}
			for i := range tt.headerCount {
				req.Header.Add("X-Windshift-Flood", fmt.Sprintf("value-%d", i))
			}

			resp, err := testHTTPClient.Do(req)
			if err != nil {
				t.Fatalf("send request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
		})
	}
}
