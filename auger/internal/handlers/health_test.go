package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/krateoplatformops/plumbing/env"
	"k8s.io/client-go/rest"
)

// Mock di `rest.InClusterConfig`
var mockInClusterConfig func() (*rest.Config, error)

func mockNSGetter() (string, error) {
	return "test-namespace", nil
}

func TestHealthCheck(t *testing.T) {
	env.SetTestMode(true)

	tests := []struct {
		name               string
		expectedStatusCode int
		expectedResponse   serviceInfo
	}{
		{
			name:               "Success - valid cluster config & namespace",
			expectedStatusCode: http.StatusOK,
			expectedResponse:   serviceInfo{Name: "test-service", Build: "v1.0.0", Namespace: "test-namespace"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {

			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			rec := httptest.NewRecorder()

			handler := HealthCheck("test-service", "v1.0.0", mockNSGetter)
			handler.ServeHTTP(rec, req)

			// Verifica codice HTTP
			if rec.Code != tc.expectedStatusCode {
				t.Errorf("expected status %d, got %d", tc.expectedStatusCode, rec.Code)
			}

			// Se è un 200 OK, verifichiamo il JSON restituito
			if tc.expectedStatusCode == http.StatusOK {
				var resp serviceInfo
				err := json.Unmarshal(rec.Body.Bytes(), &resp)
				if err != nil {
					t.Errorf("failed to parse response JSON: %v", err)
				}
				if resp != tc.expectedResponse {
					t.Errorf("expected response %+v, got %+v", tc.expectedResponse, resp)
				}
			}
		})
	}
}
