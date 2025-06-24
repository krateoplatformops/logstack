package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/krateoplatformops/plumbing/env"
	"github.com/krateoplatformops/plumbing/http/response"
	"github.com/krateoplatformops/plumbing/kubeutil"
	"k8s.io/client-go/rest"
)

// @Summary Liveness Endpoint
// @Description Health HealthCheck
// @ID health
// @Produce  json
// @Success 200 {object} serviceInfo
// @Router /health [get]
func HealthCheck(serviceName, build string, nsgetter func() (string, error)) http.HandlerFunc {
	return func(wri http.ResponseWriter, req *http.Request) {
		if !env.TestMode() {
			if _, err := rest.InClusterConfig(); err != nil {
				response.ServiceUnavailable(wri, err)
				return
			}
		}

		if nsgetter == nil {
			nsgetter = kubeutil.ServiceAccountNamespace
		}

		ns, _ := nsgetter()

		data := serviceInfo{
			Name:      serviceName,
			Build:     build,
			Namespace: ns,
		}

		wri.Header().Set("Content-Type", "application/json")
		wri.WriteHeader(http.StatusOK)
		json.NewEncoder(wri).Encode(data)
	}
}

type serviceInfo struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Build     string `json:"build"`
}
