package holmes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type chatRequest struct {
	Ask   string `json:"ask"`
	Model string `json:"model,omitempty"`
}

func AskHolmes(ctx context.Context, endpoint, question, model string, w http.ResponseWriter) error {
	reqBody, err := json.Marshal(chatRequest{Ask: question, Model: model})
	if err != nil {
		return fmt.Errorf("failed to marshal chat request: %w", err)
	}

	url := strings.TrimRight(endpoint, "/") + "/api/chat"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(reqBody)))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call Holmes service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Holmes service returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Cache-Control", "no-cache")

	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return fmt.Errorf("failed to write response: %w", writeErr)
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("failed to read Holmes response: %w", readErr)
		}
	}

	return nil
}
