package HTTPRequest

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPRequestConfig holds the configuration for an HTTP request.
type HTTPRequestConfig struct {
	Method        string            // HTTP method (GET, POST, PUT, DELETE, etc.)
	URL           string            // Full request URL
	Payload       string            // Request body (e.g., JSON string). Empty for GET.
	Headers       map[string]string // Custom headers to add to the request
	Timeout       time.Duration     // Request timeout duration
	SkipTLSVerify bool              // Whether to skip TLS verification (for self-signed certs)
}

// HTTPResponse holds the details of the HTTP response.
type HTTPResponse struct {
	Status     string      // e.g., "200 OK"
	Proto      string      // e.g., "HTTP/1.1"
	Headers    http.Header // Response headers
	Body       string      // Response body as a string
	FullOutput string      // Formatted string with status, headers, and body
	StatusCode int         // e.g., 200
}

// PerformHTTPRequest executes an HTTP request based on the provided configuration.
// It returns a formatted string of the response (status, headers, body) and an error if one occurs.
func PerformHTTPRequest(config HTTPRequestConfig) (*HTTPResponse, error) {
	if config.Method == "" {
		return nil, fmt.Errorf("HTTP method cannot be empty")
	}
	method := strings.ToUpper(config.Method)

	if config.URL == "" {
		return nil, fmt.Errorf("request URL cannot be empty")
	}
	parsedURL, err := url.Parse(config.URL)
	if err != nil {
		return nil, fmt.Errorf("error parsing URL '%s': %v", config.URL, err)
	}

	var reqBody io.Reader
	if config.Payload != "" {
		reqBody = bytes.NewBufferString(config.Payload)
	}

	req, err := http.NewRequest(method, parsedURL.String(), reqBody)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %v", err)
	}

	if config.Headers != nil {
		for key, value := range config.Headers {
			req.Header.Set(key, value)
		}
	}

	if (method == "POST" || method == "PUT" || method == "PATCH") && config.Payload != "" {
		if req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
	}

	client := &http.Client{}
	if config.Timeout > 0 {
		client.Timeout = config.Timeout
	} else {
		client.Timeout = 30 * time.Second
	}

	if config.SkipTLSVerify {
		customTransport := http.DefaultTransport.(*http.Transport).Clone()
		customTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		client.Transport = customTransport
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error sending request: %v", err)
	}
	defer resp.Body.Close()

	var responseOutput strings.Builder
	httpResp := &HTTPResponse{
		Status:     resp.Status,
		Proto:      resp.Proto,
		Headers:    resp.Header,
		StatusCode: resp.StatusCode,
	}

	responseOutput.WriteString(fmt.Sprintf("%s %s\n", httpResp.Proto, httpResp.Status))

	for name, headers := range httpResp.Headers {
		for _, h := range headers {
			responseOutput.WriteString(fmt.Sprintf("%s: %s\n", name, h))
		}
	}
	responseOutput.WriteString("\n")

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		httpResp.FullOutput = responseOutput.String()
		return httpResp, fmt.Errorf("error reading response body: %v", err)
	}
	httpResp.Body = string(bodyBytes)
	responseOutput.WriteString(httpResp.Body)
	httpResp.FullOutput = responseOutput.String()

	return httpResp, nil
}
