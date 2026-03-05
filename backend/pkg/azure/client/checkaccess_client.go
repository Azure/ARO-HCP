package client

import (
	"context"

	checkaccess "github.com/Azure/checkaccess-v2-go-sdk/client"
)

type CheckAccessv2Client interface {
	CheckAccess(context.Context,
		checkaccess.AuthorizationRequest) (*checkaccess.AuthorizationDecisionResponse,
		error)
	CreateAuthorizationRequest(string, []string,
		string) (*checkaccess.AuthorizationRequest,
		error)
}

var _ CheckAccessv2Client = (checkaccess.RemotePDPClient)(nil)
