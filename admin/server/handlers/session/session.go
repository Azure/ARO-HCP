package session

import "context"

type SessionService interface {
	// CreateSession starts a breakglass session using the mgmt cluster kubeconfig
	CreateSession(ctx context.Context, mgmtClusterKubeconfig string) (sessionID string, err error)

	// GetSession returns (ready, kubeconfig, err)
	GetSession(ctx context.Context, sessionID string) (bool, string, error)
}
