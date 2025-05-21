package HTTPRequest

import (
	"bytes" // Used to read the payload string into an io.Reader
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url" // For URL parsing and validation
	"strings" // For string manipulations like ToUpper and NewReader
	"time"    // To set a client timeout
	//"github.com/sirupsen/logrus"
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
	// Validate HTTP method
	if config.Method == "" {
		return nil, fmt.Errorf("HTTP method cannot be empty")
	}
	method := strings.ToUpper(config.Method)

	// Validate URL
	if config.URL == "" {
		return nil, fmt.Errorf("request URL cannot be empty")
	}
	parsedURL, err := url.Parse(config.URL)
	if err != nil {
		return nil, fmt.Errorf("error parsing URL '%s': %v", config.URL, err)
	}

	// Prepare request body if payload is provided
	var reqBody io.Reader
	if config.Payload != "" {
		reqBody = bytes.NewBufferString(config.Payload) // Use bytes.NewBufferString for efficiency
	}

	// Create a new HTTP request object
	req, err := http.NewRequest(method, parsedURL.String(), reqBody)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %v", err)
	}

	// Add custom headers from the config
	if config.Headers != nil {
		for key, value := range config.Headers {
			req.Header.Set(key, value)
		}
	}

	// Automatically set Content-Type for relevant methods if a payload exists and Content-Type isn't already set
	if (method == "POST" || method == "PUT" || method == "PATCH") && config.Payload != "" {
		if req.Header.Get("Content-Type") == "" {
			// Default to application/json if not specified.
			// This can be overridden by providing "Content-Type" in config.Headers.
			req.Header.Set("Content-Type", "application/json")
		}
	}

	// Configure the HTTP client
	client := &http.Client{}
	if config.Timeout > 0 {
		client.Timeout = config.Timeout
	} else {
		client.Timeout = 30 * time.Second // Default timeout
	}

	// Handle TLS verification skipping (USE WITH CAUTION)
	if config.SkipTLSVerify {
		customTransport := http.DefaultTransport.(*http.Transport).Clone()
		customTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		client.Transport = customTransport
	}

	// Print request details before sending
	/* logrus.WithFields(logrus.Fields{
		"method":   method,
		"URL":      parsedURL.String,
		"Headers:": req.Header,
		"Payload":  config.Payload,
	}).Info("Sending Request") */
	fmt.Printf("--- Sending Request ---\n")
	fmt.Printf("Method: %s\n", method)
	fmt.Printf("URL: %s\n", parsedURL.String())
	if len(req.Header) > 0 {
		fmt.Println("Headers:")
		for name, values := range req.Header {
			for _, value := range values {
				fmt.Printf("  %s: %s\n", name, value)
			}
		}
	}
	if config.Payload != "" {
		fmt.Printf("Payload: %s\n", config.Payload)
	}
	fmt.Println("-----------------------")

	// Send the request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error sending request: %v", err)
	}
	defer resp.Body.Close() // Ensure the response body is closed

	// --- Process the response ---
	var responseOutput strings.Builder
	httpResp := &HTTPResponse{
		Status:     resp.Status,
		Proto:      resp.Proto,
		Headers:    resp.Header,
		StatusCode: resp.StatusCode,
	}

	// Append status line
	responseOutput.WriteString(fmt.Sprintf("%s %s\n", httpResp.Proto, httpResp.Status))

	// Append response headers
	for name, headers := range httpResp.Headers {
		for _, h := range headers {
			responseOutput.WriteString(fmt.Sprintf("%s: %s\n", name, h))
		}
	}
	responseOutput.WriteString("\n") // Blank line separator

	// Read and append response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		// Still return what we have of the response (headers, status)
		httpResp.FullOutput = responseOutput.String()
		return httpResp, fmt.Errorf("error reading response body: %v", err)
	}
	httpResp.Body = string(bodyBytes)
	responseOutput.WriteString(httpResp.Body)
	httpResp.FullOutput = responseOutput.String()

	return httpResp, nil
}
