package api

import "time"

type HcpSREKubeconfig struct {
	ExpirationTimestamp *time.Time
	Kubeconfig          *string
}
