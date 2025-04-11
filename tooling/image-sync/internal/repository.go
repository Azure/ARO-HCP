package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/containers/azcontainerregistry"
)

// Registry is the interface for accessing image repositories
type Registry interface {
	GetTags(context.Context, string) ([]string, error)
}

// AuthedTransport is a http.RoundTripper that adds an Authorization header
type AuthedTransport struct {
	Key     string
	Wrapped http.RoundTripper
}

// RoundTrip implements http.RoundTripper and sets Authorization header
func (t *AuthedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", t.Key)
	return t.Wrapped.RoundTrip(req)
}

// QuayRegistry implements Quay Repository access
type QuayRegistry struct {
	httpclient   *http.Client
	baseUrl      string
	numberOftags int
}

// NewQuayRegistry creates a new QuayRegistry access client
func NewQuayRegistry(cfg *SyncConfig, bearerToken string) *QuayRegistry {
	q := &QuayRegistry{
		httpclient: &http.Client{Timeout: time.Duration(cfg.RequestTimeout) * time.Second,
			Transport: &AuthedTransport{
				Key:     "Bearer " + bearerToken,
				Wrapped: http.DefaultTransport,
			},
		},
		baseUrl:      "https://quay.io",
		numberOftags: cfg.NumberOfTags,
	}
	return q
}

type TagsResponse struct {
	Tags          []Tags
	Page          int
	HasAdditional bool `json:"has_additional"`
}

type Tags struct {
	Name string
}

func (q *QuayRegistry) getTagPage(ctx context.Context, image string, page int) (*TagsResponse, error) {
	path := fmt.Sprintf("%s/api/v1/repository/%s/tag/?limit=100&page=%s", q.baseUrl, image, strconv.Itoa(page))
	req, err := http.NewRequestWithContext(ctx, "GET", path, nil)

	Log().Debugw("Sending request", "path", path)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}
	resp, err := q.httpclient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	Log().Debugw("Got response", "statuscode", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	var tagsResponse TagsResponse
	err = json.Unmarshal(body, &tagsResponse)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	return &tagsResponse, nil
}

// GetTags returns the tags for the given image
func (q *QuayRegistry) GetTags(ctx context.Context, image string) ([]string, error) {
	Log().Debugw("Getting tags for image", "image", image)

	var tags []string
	hasAdditional := true

	// hard coded limit of 100, to make sure process does not get stuck
	for page := 1; len(tags) < q.numberOftags && hasAdditional && page < 100; page++ {
		tagsResponse, err := q.getTagPage(ctx, image, page)
		if err != nil {
			return nil, fmt.Errorf("failed to get tags: %v", err)
		}
		for _, tag := range tagsResponse.Tags {
			if tag.Name == "latest" {
				continue
			}
			tags = append(tags, tag.Name)
			// Check length again, cause pagesize might be way bigger than number of tags
			if len(tags) >= q.numberOftags {
				return tags, nil
			}
		}
		if !tagsResponse.HasAdditional {
			break
		}
	}
	return tags, nil
}

type getAccessToken func(context.Context, azcore.TokenCredential) (string, error)
type getACRUrl func(string) string

// AzureContainerRegistry implements ACR Repository access
type AzureContainerRegistry struct {
	acrName      string
	credential   azcore.TokenCredential
	acrClient    *azcontainerregistry.Client
	httpClient   *http.Client
	numberOfTags int
	tenantId     string

	getAccessTokenImpl getAccessToken
	getACRUrlImpl      getACRUrl
}

// NewAzureContainerRegistry creates a new AzureContainerRegistry access client
func NewAzureContainerRegistry(cfg *SyncConfig) *AzureContainerRegistry {
	var cred azcore.TokenCredential
	var err error
	if cfg.ManagedIdentityClientID != "" {
		cred, err = azidentity.NewManagedIdentityCredential(&azidentity.ManagedIdentityCredentialOptions{
			ID: azidentity.ClientID(cfg.ManagedIdentityClientID),
		})
		if err != nil {
			Log().Fatalf("failed to obtain a credentials for managed identity %s: %v", cfg.ManagedIdentityClientID, err)
		}
	} else {
		cred, err = azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			Log().Fatalf("failed to obtain default credentials: %v", err)
		}
	}

	client, err := azcontainerregistry.NewClient(fmt.Sprintf("https://%s", cfg.AcrTargetRegistry), cred, nil)
	if err != nil {
		Log().Fatalf("failed to create client: %v", err)
	}

	return &AzureContainerRegistry{
		acrName:      cfg.AcrTargetRegistry,
		acrClient:    client,
		credential:   cred,
		httpClient:   &http.Client{Timeout: time.Duration(cfg.RequestTimeout) * time.Second},
		numberOfTags: cfg.NumberOfTags,
		tenantId:     cfg.TenantId,

		getAccessTokenImpl: func(ctx context.Context, dac azcore.TokenCredential) (string, error) {
			accessToken, err := dac.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{"https://management.core.windows.net//.default"}})
			if err != nil {
				return "", err
			}
			return accessToken.Token, nil
		},

		getACRUrlImpl: func(acrName string) string {
			return fmt.Sprintf("https://%s", acrName)
		},
	}
}

type AuthSecret struct {
	RefreshToken string `json:"refresh_token"`
}

func (a *AzureContainerRegistry) createOauthRequest(ctx context.Context, accessToken string) (*http.Request, error) {
	path := fmt.Sprintf("%s/oauth2/exchange/", a.getACRUrlImpl(a.acrName))

	form := url.Values{}
	form.Add("grant_type", "access_token")
	form.Add("service", a.acrName)
	form.Add("tenant", a.tenantId)
	form.Add("access_token", accessToken)

	Log().Debugw("Creating request", "path", path)
	req, err := http.NewRequestWithContext(ctx, "POST", path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	return req, nil
}

func (a *AzureContainerRegistry) GetPullSecret(ctx context.Context) (*AuthSecret, error) {
	accessToken, err := a.getAccessTokenImpl(ctx, a.credential)
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %v", err)
	}

	req, err := a.createOauthRequest(ctx, accessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create OAuth request: %v", err)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	var authSecret AuthSecret

	err = json.Unmarshal(body, &authSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	return &authSecret, nil
}

// EnsureRepositoryExists ensures that the repository exists
func (a *AzureContainerRegistry) RepositoryExists(ctx context.Context, repository string) (bool, error) {

	pager := a.acrClient.NewListRepositoriesPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return false, fmt.Errorf("failed to advance page: %v", err)
		}
		for _, v := range page.Names {
			if *v == repository {
				return true, nil
			}
		}
	}

	return false, nil
}

func ptr[T any](v T) *T {
	return &v
}

// GetTags returns the tags in the given repository
func (a *AzureContainerRegistry) GetTags(ctx context.Context, repository string) ([]string, error) {

	var tags []string

	pager := a.acrClient.NewListTagsPager(repository, &azcontainerregistry.ClientListTagsOptions{OrderBy: ptr(azcontainerregistry.ArtifactTagOrderByLastUpdatedOnDescending)})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to advance page: %v", err)
		}
		for _, v := range page.Tags {
			if *v.Name == "latest" {
				continue
			}
			tags = append(tags, *v.Name)
		}
		if len(tags) >= a.numberOfTags {
			break
		}
	}

	return tags, nil
}

type ACRWithTokenAuth struct {
	httpclient   *http.Client
	acrName      string
	numberOftags int
	bearerToken  string
}

type AccessSecret struct {
	AccessToken string `json:"access_token"`
}

type rawACRTagResponse struct {
	Tags []rawACRTags
}

type rawACRTags struct {
	Name string
}

func getACRBearerToken(ctx context.Context, secret AzureSecretFile, acrName string) (string, error) {
	scope := "repository:*:*"
	path := fmt.Sprintf("https://%s/oauth2/token?service=%s&scope=%s", acrName, acrName, scope)

	Log().Debugw("Creating request", "path", path)
	req, err := http.NewRequestWithContext(ctx, "GET", path, nil)
	req.Header.Add("Authorization", fmt.Sprintf("Basic %s", secret.BasicAuthEncoded()))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	// todo replace with timeout enabled client
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %v", err)
	}

	var accessSecret AccessSecret

	err = json.Unmarshal(body, &accessSecret)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %v", err)
	}

	return accessSecret.AccessToken, nil
}

func NewACRWithTokenAuth(cfg *SyncConfig, acrName string, bearerToken string) *ACRWithTokenAuth {
	return &ACRWithTokenAuth{
		httpclient:   &http.Client{Timeout: time.Duration(cfg.RequestTimeout) * time.Second},
		acrName:      acrName,
		bearerToken:  bearerToken,
		numberOftags: cfg.NumberOfTags,
	}
}

func (n *ACRWithTokenAuth) GetTags(ctx context.Context, image string) ([]string, error) {
	Log().Debugw("Getting tags for image", "image", image)

	path := fmt.Sprintf("https://%s/acr/v1/%s/_tags?orderby=%s&n=%d", n.acrName, image, azcontainerregistry.ArtifactTagOrderByLastUpdatedOnDescending, n.numberOftags)
	req, err := http.NewRequestWithContext(ctx, "GET", path, nil)
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", n.bearerToken))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	Log().Debugw("Sending request", "path", path)
	resp, err := n.httpclient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	Log().Debugw("Got response", "statuscode", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	var acrResponse rawACRTagResponse
	err = json.Unmarshal(body, &acrResponse)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	tagList := make([]string, 0)

	for _, tag := range acrResponse.Tags {
		tagList = append(tagList, tag.Name)
	}

	return tagList, nil
}

// OCIRegistry implements OCI Repository access
type OCIRegistry struct {
	httpclient   *http.Client
	baseURL      string
	numberOftags int
	bearerToken  string
}

// NewOCIRegistry creates a new OCIRegistry access client
func NewOCIRegistry(cfg *SyncConfig, baseURL, bearerToken string) *OCIRegistry {
	o := &OCIRegistry{
		httpclient:   &http.Client{Timeout: time.Duration(cfg.RequestTimeout) * time.Second},
		numberOftags: cfg.NumberOfTags,
		bearerToken:  bearerToken,
	}
	if !strings.HasPrefix(o.baseURL, "https://") {
		o.baseURL = fmt.Sprintf("https://%s", baseURL)
	} else {
		o.baseURL = baseURL
	}
	return o
}

type rawManifest struct {
	TimeUploadedMs string
	Tag            []string
}

type rawOCIResponse struct {
	Manifest map[string]rawManifest
	Tags     []string
}

func getNewestTags(response *rawOCIResponse, numberOfTags int) ([]string, error) {
	var returnTags []string

	uploadedTagAt := make(map[int][]string)
	uploadTimes := make([]int, 0, len(response.Manifest))

	for _, manifest := range response.Manifest {
		if len(manifest.Tag) == 0 {
			continue
		}
		uploadedAt, err := strconv.Atoi(manifest.TimeUploadedMs)
		if err != nil {
			return nil, fmt.Errorf("failed to parse manifest %s time: %v", manifest, err)
		}
		uploadedTagAt[uploadedAt] = manifest.Tag
		uploadTimes = append(uploadTimes, uploadedAt)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(uploadTimes)))

	for i, k := range uploadTimes {
		if i >= numberOfTags {
			break
		}
		returnTags = append(returnTags, uploadedTagAt[k]...)
	}

	return returnTags, nil
}

// GetTags returns the tags in the given repository
func (o *OCIRegistry) GetTags(ctx context.Context, image string) ([]string, error) {
	Log().Debugw("Getting tags for image", "image", image)

	path := fmt.Sprintf("%s/v2/%s/tags/list", o.baseURL, image)
	req, err := http.NewRequestWithContext(ctx, "GET", path, nil)
	if o.bearerToken != "" {
		req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", o.bearerToken))
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	Log().Debugw("Sending request", "path", path)
	resp, err := o.httpclient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	Log().Debugw("Got response", "statuscode", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	var rawOCIResponse rawOCIResponse
	err = json.Unmarshal(body, &rawOCIResponse)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	return getNewestTags(&rawOCIResponse, o.numberOftags)
}
