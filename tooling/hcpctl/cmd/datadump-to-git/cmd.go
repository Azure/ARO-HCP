// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package datadumptogit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

// logEntry represents a single log line from the backend
type logEntry struct {
	Time  string `json:"time"`
	Level string `json:"level"`
	Msg   string `json:"msg"`
}

// contentData represents the content object in the log JSON
type contentData struct {
	ResourceID     string             `json:"resourceID"`
	ExternalId     string             `json:"externalId"`
	Request        string             `json:"request"`
	CosmosMetadata *cosmosMetadataRef `json:"cosmosMetadata,omitempty"`
}

// cosmosMetadataRef is the relevant slice of arm.CosmosMetadata as serialized inside `content`.
// Resource types whose top-level envelope no longer carries a redundant `resourceID` (e.g.
// Operation, ManagementClusterContent) only expose it here.
type cosmosMetadataRef struct {
	ResourceID string `json:"resourceID"`
}

// logData represents the full log JSON structure
type logData struct {
	// CurrentResourceID is the structured-logging key DumpDataToLogger sets alongside `content`
	// for every dumped record. It's populated even when the inner content has no top-level
	// resourceID (operation statuses, etc.), so it's our most reliable source of the document's
	// ARM resource ID.
	CurrentResourceID string       `json:"currentResourceID"`
	Content           *contentData `json:"content"`
}

// dataDumpEntry represents a parsed data dump entry
type dataDumpEntry struct {
	Timestamp  string
	ResourceID string
	Content    string // The JSON to write to the file
	FullMsg    string // The full log JSON for operation status detection
	// RelativePath, when non-empty, overrides the path derived from
	// ResourceID. Used for log entries that don't carry an Azure resource ID
	// (e.g. cluster-service state dumps).
	RelativePath string
}

func NewCommand(group string) (*cobra.Command, error) {
	opts := defaultOptions()

	cmd := &cobra.Command{
		Use:     "datadump-to-git",
		Short:   "Create git history from backend data dump logs",
		GroupID: group,
		Long: `Parse backend logs to extract DataDump entries and create a git repository
where each resource gets its own file. A new commit is created whenever the
content of a resource changes, allowing you to use git tools like 'git log',
'git diff', and 'git blame' to analyze the history of resource state changes.`,
		Example: `  # Create history from backend log
  hcpctl datadump-to-git --log /path/to/backend.log --output /tmp/history

  # Then use git to explore the history
  cd /tmp/history
  git log --oneline
  git show <commit-hash>
  git diff <commit1> <commit2>`,
		Args:             cobra.NoArgs,
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	if err := bindOptions(opts, cmd); err != nil {
		return nil, err
	}

	return cmd, nil
}

func (opts *options) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	// Clean up temp directory if we created one
	if opts.tempDir != "" {
		defer os.RemoveAll(opts.tempDir)
	}

	// Parse all log files
	var allEntries []dataDumpEntry
	for _, logFile := range opts.logFiles {
		logger.Info("Parsing log file", "path", logFile)
		entries, err := parseLogFile(logFile)
		if err != nil {
			return fmt.Errorf("failed to parse log file %s: %w", logFile, err)
		}
		allEntries = append(allEntries, entries...)
	}

	logger.Info("Found data dump entries", "count", len(allEntries), "files", len(opts.logFiles))

	// Sort entries by timestamp (stable sort preserves order of entries with same timestamp)
	sort.SliceStable(allEntries, func(i, j int) bool {
		return allEntries[i].Timestamp < allEntries[j].Timestamp
	})

	// Create and initialize git repo
	logger.Info("Initializing git repository", "path", opts.OutputDir)
	if err := initGitRepo(opts.OutputDir); err != nil {
		return fmt.Errorf("failed to initialize git repo: %w", err)
	}

	// Process entries and create commits
	logger.Info("Processing entries and creating commits")
	commitCount, err := processEntries(ctx, allEntries, opts.OutputDir)
	if err != nil {
		return fmt.Errorf("failed to process entries: %w", err)
	}

	logger.Info("Completed", "commits", commitCount)
	return nil
}

func parseLogFile(path string) ([]dataDumpEntry, error) {
	// Check if this is a CSV file
	if strings.HasSuffix(strings.ToLower(path), ".csv") {
		return parseCSVFile(path)
	}
	return parseJSONLFile(path)
}

func parseCSVFile(path string) ([]dataDumpEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Create a buffered reader to handle BOM
	bufReader := bufio.NewReader(file)
	// Skip UTF-8 BOM if present
	bom, err := bufReader.Peek(3)
	if err == nil && len(bom) >= 3 && bom[0] == 0xef && bom[1] == 0xbb && bom[2] == 0xbf {
		if _, err := bufReader.Discard(3); err != nil {
			return nil, fmt.Errorf("failed to skip UTF-8 BOM: %w", err)
		}
	}

	reader := csv.NewReader(bufReader)
	// Read header to find the "log" column index
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV header: %w", err)
	}

	logColIdx := -1
	for i, col := range header {
		// Handle BOM if present
		cleanCol := strings.TrimPrefix(col, "\ufeff")
		if strings.EqualFold(cleanCol, "log") {
			logColIdx = i
			break
		}
	}
	if logColIdx < 0 {
		return nil, fmt.Errorf("CSV file does not have a 'log' column")
	}

	var entries []dataDumpEntry
	for {
		record, err := reader.Read()
		if err != nil {
			break // EOF or error
		}

		if logColIdx >= len(record) {
			continue
		}

		logJSON := record[logColIdx]

		if !looksLikeDataDump(logJSON) {
			continue
		}

		entry, ok := parseLogJSON(logJSON)
		if ok {
			entries = append(entries, entry)
		}
	}

	return entries, nil
}

// looksLikeDataDump is a coarse substring filter to skip log lines that
// clearly aren't data-dump entries before we attempt JSON parsing.
//
// We match against tokens from the *explicit* logger.Info call (the msg text or the log
// keys / values we control). Tokens that only appear because of incidental Go-runtime metadata
// (e.g. the receiver type name leaking into source.function) are too brittle to rely on:
// LogValues.AddControllerName lowercases the controller name, so the registered controller name
// "SubscriptionNonClusterDataDump" lands on the wire as "subscriptionnonclusterdatadump".
func looksLikeDataDump(logJSON string) bool {
	return strings.Contains(logJSON, "dumping resourceID ") ||
		strings.Contains(logJSON, "cluster-service state dump") ||
		strings.Contains(logJSON, "cluster-service node pool state dump")
}

func parseJSONLFile(path string) ([]dataDumpEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	var entries []dataDumpEntry
	scanner := bufio.NewScanner(file)
	// Increase buffer size for long lines
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if !looksLikeDataDump(line) {
			continue
		}

		entry, ok := parseLogJSON(line)
		if ok {
			entries = append(entries, entry)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	return entries, nil
}

// unwrapLogEnvelope checks if the JSON is wrapped in a {"log": {...}} envelope
// and returns the inner JSON if so, otherwise returns the original.
func unwrapLogEnvelope(logJSON string) string {
	var envelope struct {
		Log json.RawMessage `json:"log"`
	}
	if err := json.Unmarshal([]byte(logJSON), &envelope); err != nil {
		return logJSON
	}
	if envelope.Log != nil {
		return string(envelope.Log)
	}
	return logJSON
}

// parseLogJSON parses a single log JSON line and returns a dataDumpEntry
func parseLogJSON(logJSON string) (dataDumpEntry, bool) {
	// Unwrap {"log": {...}} envelope if present
	inner := unwrapLogEnvelope(logJSON)

	var entry logEntry
	if err := json.Unmarshal([]byte(inner), &entry); err != nil {
		return dataDumpEntry{}, false
	}

	switch entry.Msg {
	case "cluster-service state dump":
		return parseCSClusterStateDump(inner, entry.Time)
	case "cluster-service node pool state dump":
		return parseCSNodePoolStateDump(inner, entry.Time)
	}

	// We need to extract resourceID and content from the inner log JSON
	resourceID := extractResourceID(inner)
	if resourceID == "" {
		return dataDumpEntry{}, false
	}

	// Extract the content to write to the file
	content := extractContent(inner)

	return dataDumpEntry{
		Timestamp:  entry.Time,
		ResourceID: resourceID,
		Content:    content,
		FullMsg:    inner,
	}, true
}

// parseCSClusterStateDump parses a "cluster-service state dump" log entry,
// whose payload is the raw cluster-service Cluster object under .csCluster.
// The resulting file is placed peer to the cluster's hcpopenshiftcluster
// JSON file.
func parseCSClusterStateDump(inner, timestamp string) (dataDumpEntry, bool) {
	csID := extractStringField(inner, "clusterServiceID")
	clusterResourceID := extractStringField(inner, "resource_id")
	csClusterRaw := extractRawField(inner, "csCluster")
	if csID == "" || clusterResourceID == "" || csClusterRaw == "" {
		return dataDumpEntry{}, false
	}
	relPath := csClusterStatePath(clusterResourceID, csID)
	return dataDumpEntry{
		Timestamp:    timestamp,
		ResourceID:   "cluster-service-cluster:" + csID,
		Content:      csClusterRaw,
		FullMsg:      inner,
		RelativePath: relPath,
	}, true
}

// parseCSNodePoolStateDump parses a "cluster-service node pool state dump"
// log entry, whose payload is the raw cluster-service NodePool object under
// .csNodePool. The resulting file is placed peer to the node pool's
// hcpopenshiftcluster nodepools JSON file.
func parseCSNodePoolStateDump(inner, timestamp string) (dataDumpEntry, bool) {
	npCSID := extractStringField(inner, "nodePoolClusterServiceID")
	clusterResourceID := extractStringField(inner, "resource_id")
	csNodePoolRaw := extractRawField(inner, "csNodePool")
	if npCSID == "" || clusterResourceID == "" || csNodePoolRaw == "" {
		return dataDumpEntry{}, false
	}
	relPath := csNodePoolStatePath(clusterResourceID, npCSID)
	return dataDumpEntry{
		Timestamp:    timestamp,
		ResourceID:   "cluster-service-nodepool:" + npCSID,
		Content:      csNodePoolRaw,
		FullMsg:      inner,
		RelativePath: relPath,
	}, true
}

// extractContent extracts the .content field to write to the git repo file
func extractContent(logJSON string) string {
	return extractRawField(logJSON, "content")
}

// extractRawField returns the raw JSON of a top-level field, or "" if the
// field is absent or the JSON does not parse.
func extractRawField(logJSON, field string) string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(logJSON), &raw); err != nil {
		return ""
	}
	if v, ok := raw[field]; ok {
		return string(v)
	}
	return ""
}

// extractStringField returns the value of a top-level string field, or ""
// if the field is absent, not a string, or the JSON does not parse.
func extractStringField(logJSON, field string) string {
	raw := extractRawField(logJSON, field)
	if raw == "" {
		return ""
	}
	var s string
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return ""
	}
	return s
}

// extractResourceID extracts the resource ID of the dumped record. It tries, in order:
//  1. The top-level structured-logging `currentResourceID` key set by DumpDataToLogger. This is
//     always present for data dumps and is the only reliable location for record types whose
//     `content` does not carry its own top-level `resourceID` (e.g. hcpOperationStatuses,
//     managementClusterContents).
//  2. `.content.resourceID` for record types whose envelope still serializes it (clusters,
//     nodepools, externalauths).
//  3. `.content.cosmosMetadata.resourceID` as a final fallback.
func extractResourceID(logJSON string) string {
	var data logData
	if err := json.Unmarshal([]byte(logJSON), &data); err != nil {
		return ""
	}

	if data.CurrentResourceID != "" {
		return data.CurrentResourceID
	}
	if data.Content != nil && data.Content.ResourceID != "" {
		return data.Content.ResourceID
	}
	if data.Content != nil && data.Content.CosmosMetadata != nil && data.Content.CosmosMetadata.ResourceID != "" {
		return data.Content.CosmosMetadata.ResourceID
	}
	return ""
}

func initGitRepo(dir string) error {
	// Create directory if it doesn't exist
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Check if already a git repo
	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		// Already a git repo
		return nil
	}

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to init git repo: %w\n%s", err, output)
	}

	// Configure git user for commits
	cmd = exec.Command("git", "config", "user.email", "datadump-to-git@localhost")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to configure git email: %w\n%s", err, output)
	}

	cmd = exec.Command("git", "config", "user.name", "DataDump History")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to configure git name: %w\n%s", err, output)
	}

	return nil
}

func processEntries(ctx context.Context, entries []dataDumpEntry, outputDir string) (int, error) {
	logger := logr.FromContextOrDiscard(ctx)

	// Track last content per resource to detect changes and generate diffs
	lastContent := make(map[string]map[string]interface{})
	commitCount := 0

	for i, entry := range entries {
		// Normalize resource ID to lowercase for consistent tracking
		normalizedResourceID := strings.ToLower(entry.ResourceID)

		// Convert resource_id to directory path structure
		// For operation statuses, place them in the same directory as their externalId
		// Use FullMsg for operation status detection (contains externalId and request fields)
		relPath := entry.RelativePath
		if relPath == "" {
			relPath = resourceIDToPathWithContent(entry.ResourceID, entry.FullMsg)
		}
		filePath := filepath.Join(outputDir, relPath)

		// Create parent directories
		parentDir := filepath.Dir(filePath)
		if err := os.MkdirAll(parentDir, 0755); err != nil {
			return commitCount, fmt.Errorf("failed to create directory %s: %w", parentDir, err)
		}

		// Pretty-print the JSON content for readability
		prettyContent, err := prettyPrintJSON(entry.Content)
		if err != nil {
			// If we can't pretty-print, use the raw content
			prettyContent = entry.Content
		}

		// Parse current content as JSON
		var currentContent map[string]interface{}
		if err := json.Unmarshal([]byte(entry.Content), &currentContent); err != nil {
			currentContent = nil
		}

		// Check if content changed (direct string comparison)
		previousContent, exists := lastContent[normalizedResourceID]
		if exists && prettyContent == mustPrettyPrint(previousContent) {
			// Content unchanged, skip
			continue
		}

		// Generate commit message
		commitMsg := generateCommitMessage(normalizedResourceID, previousContent, currentContent)

		// Update last content
		lastContent[normalizedResourceID] = currentContent

		// Write file
		if err := os.WriteFile(filePath, []byte(prettyContent), 0644); err != nil {
			return commitCount, fmt.Errorf("failed to write file %s: %w", filePath, err)
		}

		// Stage the file
		cmd := exec.Command("git", "add", relPath)
		cmd.Dir = outputDir
		if output, err := cmd.CombinedOutput(); err != nil {
			return commitCount, fmt.Errorf("failed to stage file: %w\n%s", err, output)
		}

		// Check if there are staged changes
		cmd = exec.Command("git", "diff", "--cached", "--quiet")
		cmd.Dir = outputDir
		if err := cmd.Run(); err == nil {
			// No changes staged, skip commit
			continue
		}

		// Create commit
		cmd = exec.Command("git", "commit", "-m", commitMsg, "--date", entry.Timestamp)
		cmd.Dir = outputDir
		cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+entry.Timestamp)
		if output, err := cmd.CombinedOutput(); err != nil {
			return commitCount, fmt.Errorf("failed to commit: %w\n%s", err, output)
		}

		commitCount++
		if commitCount%100 == 0 {
			logger.V(1).Info("Progress", "processed", i+1, "total", len(entries), "commits", commitCount)
		}
	}

	return commitCount, nil
}

// generateCommitMessage creates a commit message describing the change
func generateCommitMessage(resourceID string, oldContent, newContent map[string]interface{}) string {
	var sb strings.Builder

	// Extract request type from content if available
	request := ""
	if newContent != nil {
		if r, ok := newContent["request"].(string); ok {
			request = r
		}
	}

	// First line: resourceID request
	sb.WriteString(resourceID)
	if request != "" {
		sb.WriteString(" ")
		sb.WriteString(request)
	}
	sb.WriteString("\n")

	if oldContent == nil {
		// This is a create
		sb.WriteString("\nCREATED")
		return sb.String()
	}

	if newContent == nil {
		// newContent is nil when JSON unmarshal fails, not a true deletion
		sb.WriteString("\nUPDATED (content could not be parsed as JSON)")
		return sb.String()
	}

	// This is an update - find changed fields
	changes := findChanges("", oldContent, newContent)
	if len(changes) == 0 {
		sb.WriteString("\nUPDATED (no field changes detected)")
		return sb.String()
	}

	sb.WriteString("\nUPDATED:")
	for _, change := range changes {
		sb.WriteString("\n  ")
		sb.WriteString(change)
	}

	return sb.String()
}

// findChanges recursively finds differences between two JSON objects
func findChanges(prefix string, oldObj, newObj map[string]interface{}) []string {
	var changes []string

	// Check for modified or new fields in newObj
	for key, newVal := range newObj {
		fieldPath := key
		if prefix != "" {
			fieldPath = prefix + "." + key
		}

		oldVal, exists := oldObj[key]
		if !exists {
			// New field
			changes = append(changes, fmt.Sprintf("%s: %v (added)", fieldPath, formatValue(newVal)))
			continue
		}

		// Check if values are different
		if !valuesEqual(oldVal, newVal) {
			// Check if both are maps - recurse
			oldMap, oldIsMap := oldVal.(map[string]interface{})
			newMap, newIsMap := newVal.(map[string]interface{})
			if oldIsMap && newIsMap {
				changes = append(changes, findChanges(fieldPath, oldMap, newMap)...)
				continue
			}

			// Check if both are arrays - recurse into array elements
			oldArr, oldIsArr := oldVal.([]interface{})
			newArr, newIsArr := newVal.([]interface{})
			if oldIsArr && newIsArr {
				changes = append(changes, findArrayChanges(fieldPath, oldArr, newArr)...)
				continue
			}

			changes = append(changes, fmt.Sprintf("%s: %v", fieldPath, formatValue(newVal)))
		}
	}

	// Check for removed fields
	for key := range oldObj {
		if _, exists := newObj[key]; !exists {
			fieldPath := key
			if prefix != "" {
				fieldPath = prefix + "." + key
			}
			changes = append(changes, fmt.Sprintf("%s: (removed)", fieldPath))
		}
	}

	return changes
}

// findArrayChanges compares two arrays and returns changes
func findArrayChanges(prefix string, oldArr, newArr []interface{}) []string {
	var changes []string

	// Compare by index
	maxLen := len(oldArr)
	if len(newArr) > maxLen {
		maxLen = len(newArr)
	}

	for i := 0; i < maxLen; i++ {
		elemPath := fmt.Sprintf("%s[%d]", prefix, i)

		if i >= len(oldArr) {
			changes = append(changes, fmt.Sprintf("%s: %v (added)", elemPath, formatValue(newArr[i])))
			continue
		}
		if i >= len(newArr) {
			changes = append(changes, fmt.Sprintf("%s: (removed)", elemPath))
			continue
		}

		oldVal := oldArr[i]
		newVal := newArr[i]

		if !valuesEqual(oldVal, newVal) {
			// Check if both are maps - recurse
			oldMap, oldIsMap := oldVal.(map[string]interface{})
			newMap, newIsMap := newVal.(map[string]interface{})
			if oldIsMap && newIsMap {
				changes = append(changes, findChanges(elemPath, oldMap, newMap)...)
				continue
			}

			// Check if both are arrays - recurse
			oldSubArr, oldIsArr := oldVal.([]interface{})
			newSubArr, newIsArr := newVal.([]interface{})
			if oldIsArr && newIsArr {
				changes = append(changes, findArrayChanges(elemPath, oldSubArr, newSubArr)...)
				continue
			}

			changes = append(changes, fmt.Sprintf("%s: %v", elemPath, formatValue(newVal)))
		}
	}

	return changes
}

// valuesEqual checks if two values are equal
func valuesEqual(a, b interface{}) bool {
	aJSON, _ := json.Marshal(a)
	bJSON, _ := json.Marshal(b)
	return string(aJSON) == string(bJSON)
}

// formatValue formats a value for display in commit message
func formatValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		if len(val) > 50 {
			return fmt.Sprintf("%q...", val[:50])
		}
		return fmt.Sprintf("%q", val)
	case map[string]interface{}, []interface{}:
		return "{...}"
	default:
		return fmt.Sprintf("%v", val)
	}
}

// mustPrettyPrint pretty prints a map, returning empty string on error
// Uses the same formatting as prettyPrintJSON (no HTML escaping) for consistent comparison
func mustPrettyPrint(content map[string]interface{}) string {
	if content == nil {
		return ""
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(content); err != nil {
		return ""
	}
	return strings.TrimSpace(buf.String())
}

// resourceIDToPathWithContent converts an Azure resource ID to a directory path structure
// For operation statuses, it places them in a hcpoperationstatuses subdirectory under
// their externalId's directory, with the request type as filename prefix.
func resourceIDToPathWithContent(resourceID string, logJSON string) string {
	// Check if this is an operation status with an externalId in .content
	var data logData
	if err := json.Unmarshal([]byte(logJSON), &data); err != nil {
		return resourceIDToPath(resourceID)
	}

	if data.Content == nil || data.Content.ExternalId == "" || data.Content.Request == "" {
		return resourceIDToPath(resourceID)
	}

	// Extract operation ID from the resource ID (last path component)
	operationID := filepath.Base(resourceID)

	// Use externalId as the base path
	basePath := strings.TrimPrefix(data.Content.ExternalId, "/")
	basePath = strings.ReplaceAll(basePath, "\\", "/")
	basePath = strings.ToLower(basePath)

	// Sanitize path components
	parts := strings.Split(basePath, "/")
	for i, part := range parts {
		re := regexp.MustCompile(`[:*?"<>|]`)
		parts[i] = re.ReplaceAllString(part, "_")
	}

	// Add hcpoperationstatuses subdirectory
	parts = append(parts, "hcpoperationstatuses")

	// Create filename with request prefix
	filename := fmt.Sprintf("%s-%s.json", strings.ToLower(data.Content.Request), strings.ToLower(operationID))
	return filepath.Join(append(parts, filename)...)
}

// csClusterStatePath returns the relative path for a cluster-service cluster
// state dump. The file is placed peer to the cluster's hcpopenshiftcluster
// JSON file, using the cluster-service ID as a disambiguator so multiple
// clusters in the same resource group don't collide.
func csClusterStatePath(clusterResourceID, csID string) string {
	clusterPath := resourceIDToPath(clusterResourceID)
	parentDir := filepath.Dir(clusterPath)
	csIDLower := strings.ToLower(csIDBaseName(csID))
	return filepath.Join(parentDir, fmt.Sprintf("cluster-service-cluster-%s.json", csIDLower))
}

// csNodePoolStatePath returns the relative path for a cluster-service
// node-pool state dump. The file is placed peer to per-nodepool JSON files
// under the cluster's nodepools directory.
func csNodePoolStatePath(clusterResourceID, npCSID string) string {
	clusterPath := resourceIDToPath(clusterResourceID)
	clusterDir := strings.TrimSuffix(clusterPath, ".json")
	npCSIDLower := strings.ToLower(csIDBaseName(npCSID))
	return filepath.Join(clusterDir, "nodepools", fmt.Sprintf("cluster-service-nodepool-%s.json", npCSIDLower))
}

// csIDBaseName returns the last path segment of a cluster-service ID, e.g.
// "/api/aro_hcp/v1alpha1/clusters/<id>" -> "<id>", or
// "/api/aro_hcp/v1alpha1/clusters/<cid>/node_pools/<id>" -> "<id>".
func csIDBaseName(csID string) string {
	csID = strings.TrimSuffix(csID, "/")
	return path.Base(csID)
}

// resourceIDToPath converts an Azure resource ID to a directory path structure
// Example: /subscriptions/abc-123/resourceGroups/my-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/my-cluster
// becomes: subscriptions/abc-123/resourcegroups/my-rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/my-cluster.json
func resourceIDToPath(resourceID string) string {
	// Remove leading slash if present
	path := strings.TrimPrefix(resourceID, "/")

	// Normalize path separators (handle both / and \)
	// Backslashes may appear in resource IDs from Windows-based tooling or escaped JSON strings
	path = strings.ReplaceAll(path, "\\", "/")

	// Lowercase the entire path for consistent file/directory names
	path = strings.ToLower(path)

	// Sanitize each path component to remove invalid filesystem characters
	parts := strings.Split(path, "/")
	for i, part := range parts {
		// Replace characters that are invalid in filenames
		re := regexp.MustCompile(`[:*?"<>|]`)
		parts[i] = re.ReplaceAllString(part, "_")
	}

	// Join back and add .json extension
	return filepath.Join(parts...) + ".json"
}

func prettyPrintJSON(content string) (string, error) {
	var parsed interface{}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return "", err
	}

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(parsed); err != nil {
		return "", err
	}

	return strings.TrimSpace(buf.String()), nil
}
