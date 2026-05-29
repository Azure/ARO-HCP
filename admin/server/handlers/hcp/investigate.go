package hcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"github.com/google/uuid"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/ARO-HCP/admin/server/holmes"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/fpa"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"

	mc "github.com/Azure/ARO-HCP/sessiongate/pkg/mc"
)

const (
	maxQuestionLength = 1000
)

var (
	validScopes  = map[string]bool{"dataplane": true, "controlplane": true, "serviceplane": true}
	controlChars = regexp.MustCompile(`[\x00-\x1f\x7f]`)
)

type investigateRequest struct {
	Question string `json:"question"`
	Scope    string `json:"scope"`
}

type HCPInvestigateHandler struct {
	resourcesDBClient      database.ResourcesDBClient
	clustersServiceClient  ocm.ClusterServiceClientSpec
	fpaCredentialRetriever fpa.FirstPartyApplicationTokenCredentialRetriever
	holmesConfig           *holmes.HolmesConfig
	limiter                *holmes.ConcurrencyLimiter
	kubeconfigBuilder      *holmes.KubeconfigBuilder
	podManager             *holmes.PodManager
}

func NewHCPInvestigateHandler(
	resourcesDBClient database.ResourcesDBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	fpaCredentialRetriever fpa.FirstPartyApplicationTokenCredentialRetriever,
	holmesConfig *holmes.HolmesConfig,
	limiter *holmes.ConcurrencyLimiter,
	kubeconfigBuilder *holmes.KubeconfigBuilder,
	podManager *holmes.PodManager,
) *HCPInvestigateHandler {
	return &HCPInvestigateHandler{
		resourcesDBClient:      resourcesDBClient,
		clustersServiceClient:  clustersServiceClient,
		fpaCredentialRetriever: fpaCredentialRetriever,
		holmesConfig:           holmesConfig,
		limiter:                limiter,
		kubeconfigBuilder:      kubeconfigBuilder,
		podManager:             podManager,
	}
}

func (h *HCPInvestigateHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()

	resourceID, err := utils.ResourceIDFromContext(ctx)
	if err != nil {
		return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "invalid resource identifier in request")
	}

	var req investigateRequest
	if err := json.NewDecoder(request.Body).Decode(&req); err != nil {
		return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "invalid request body: %s", err)
	}

	if err := validateInvestigateRequest(&req); err != nil {
		return err
	}

	if !h.limiter.Acquire() {
		return arm.NewCloudError(http.StatusTooManyRequests, "TooManyRequests", "", "maximum concurrent investigations reached, please try again later")
	}
	defer h.limiter.Release()

	// Serviceplane investigates the service cluster itself — no HCP lookup needed.
	if req.Scope == "serviceplane" {
		return holmes.AskHolmes(ctx, h.holmesConfig.ServiceClusterEndpoint, req.Question, h.holmesConfig.Model, writer)
	}

	hcp, err := h.resourcesDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(ctx, resourceID.Name)
	if err != nil {
		return fmt.Errorf("failed to get HCP from database: %w", err)
	}

	if hcp.ServiceProviderProperties.ClusterServiceID == nil {
		return fmt.Errorf("cluster has no ClusterServiceID")
	}

	switch req.Scope {
	case "dataplane":
		return h.handleDataplane(writer, request, hcp, req.Question)
	case "controlplane":
		return h.handleControlplane(writer, request, hcp, req.Question)
	default:
		return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "invalid scope: %s", req.Scope)
	}
}

func (h *HCPInvestigateHandler) handleControlplane(writer http.ResponseWriter, request *http.Request, hcp *api.HCPOpenShiftCluster, question string) error {
	ctx := request.Context()
	csID := *hcp.ServiceProviderProperties.ClusterServiceID

	provisionShard, err := h.clustersServiceClient.GetClusterProvisionShard(ctx, csID)
	if err != nil {
		return ClusterServiceError(err, "provision shard")
	}

	mgmtClusterResourceID := provisionShard.AzureShard().AksManagementClusterResourceId()

	subscription, err := h.resourcesDBClient.Subscriptions().Get(ctx, hcp.ID.SubscriptionID)
	if err != nil {
		return fmt.Errorf("failed to get subscription: %w", err)
	}

	credential, err := h.fpaCredentialRetriever.RetrieveCredential(*subscription.Properties.TenantId)
	if err != nil {
		return fmt.Errorf("failed to retrieve FPA credential: %w", err)
	}

	mgmtRESTConfig, err := mc.GetAKSRESTConfig(ctx, mgmtClusterResourceID, credential)
	if err != nil {
		return fmt.Errorf("failed to get management cluster REST config: %w", err)
	}

	proxyURL := holmes.ServiceProxyURL(mgmtRESTConfig, holmes.HolmesNamespace, "holmesgpt-svc")

	httpClient, err := holmes.HTTPClientForRESTConfig(mgmtRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create HTTP client for management cluster: %w", err)
	}

	return holmes.AskHolmesWithClient(ctx, httpClient, proxyURL, question, h.holmesConfig.Model, writer)
}

func (h *HCPInvestigateHandler) handleDataplane(writer http.ResponseWriter, request *http.Request, hcp *api.HCPOpenShiftCluster, question string) error {
	ctx := request.Context()
	csID := *hcp.ServiceProviderProperties.ClusterServiceID

	clusterHypershiftDetails, err := h.clustersServiceClient.GetClusterHypershiftDetails(ctx, csID)
	if err != nil {
		return ClusterServiceError(err, "hypershift details")
	}

	provisionShard, err := h.clustersServiceClient.GetClusterProvisionShard(ctx, csID)
	if err != nil {
		return ClusterServiceError(err, "provision shard")
	}

	mgmtClusterResourceID := provisionShard.AzureShard().AksManagementClusterResourceId()
	hcpNamespace := clusterHypershiftDetails.HCPNamespace()

	subscription, err := h.resourcesDBClient.Subscriptions().Get(ctx, hcp.ID.SubscriptionID)
	if err != nil {
		return fmt.Errorf("failed to get subscription: %w", err)
	}

	credential, err := h.fpaCredentialRetriever.RetrieveCredential(*subscription.Properties.TenantId)
	if err != nil {
		return fmt.Errorf("failed to retrieve FPA credential: %w", err)
	}

	kasEndpoint := fmt.Sprintf("https://kube-apiserver.%s.svc.cluster.local:6443", hcpNamespace)

	kubeconfigResult, err := h.kubeconfigBuilder.BuildDataplaneKubeconfig(
		ctx, credential, mgmtClusterResourceID, hcpNamespace,
		csID.ClusterID(), kasEndpoint,
	)
	if err != nil {
		return fmt.Errorf("failed to build kubeconfig: %w", err)
	}
	defer kubeconfigResult.Cleanup()

	mgmtRESTConfig, err := mc.GetAKSRESTConfig(ctx, mgmtClusterResourceID, credential)
	if err != nil {
		return fmt.Errorf("failed to get management cluster REST config: %w", err)
	}

	mgmtKubeClient, err := kubernetes.NewForConfig(mgmtRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create management cluster kubernetes client: %w", err)
	}

	investigationID := uuid.New().String()[:8]

	return h.podManager.RunInvestigation(ctx, mgmtKubeClient, kubeconfigResult.KubeconfigYAML, question, investigationID, writer)
}

func validateInvestigateRequest(req *investigateRequest) error {
	if req.Question == "" {
		return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "question is required")
	}

	if len(req.Question) > maxQuestionLength {
		return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "question must not exceed %d characters", maxQuestionLength)
	}

	if controlChars.MatchString(req.Question) {
		return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "question must not contain control characters")
	}

	if req.Scope == "" {
		req.Scope = "dataplane"
	}

	if !validScopes[req.Scope] {
		return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "scope must be one of: dataplane, controlplane, serviceplane")
	}

	return nil
}
