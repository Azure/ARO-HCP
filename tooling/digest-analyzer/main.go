package main

import (
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	outputFormats   []string
	envFilter       string
	componentFilter string
	showDiffOnly    bool
)

type Config struct {
	Defaults interface{} `yaml:"defaults"`
	Clouds   interface{} `yaml:"clouds"`
}

type DigestInfo struct {
	Digest      string
	Component   string
	Name        string
	Environment string
	Cloud       string
	MergeTime   string
	CommitHash  string
	SourceFile  string
	ConfigPath  string
}

type ComponentGroup struct {
	Name      string
	Directory string
	Environments map[string][]DigestInfo
}

type TableRow struct {
	Component string
	Cloud     string
	Env       string
	Image     string
	Digest    string
	Age       string
	Rev       string
}

// Environment configuration defines the parsing order for each environment
type EnvConfig struct {
	Cloud       string
	Environment string
	Sources     []ConfigSource
}

type ConfigSource struct {
	File string
	Path string
}

var rootCmd = &cobra.Command{
	Use:   "digest-analyzer <config-dir> [config-dir2]",
	Short: "Analyze ARO-HCP component image digests across environments",
	Long: `digest-analyzer parses ARO-HCP configuration files and extracts
image digests for all components across different cloud environments.

It supports multiple output formats and tracks git commit information
for each digest to provide deployment history.

When two config directories are provided, environments from the first
directory are prefixed with 'left.' and from the second with 'right.'.`,
	Example: `  digest-analyzer config
  digest-analyzer -o csv config
  digest-analyzer --output table config
  digest-analyzer -e dev,int,stg config
  digest-analyzer --envs dev,cspr --output csv config
  digest-analyzer config1 config2 -e left.stg,right.prod`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		var allDigests []DigestInfo

		if len(args) == 1 {
			// Single config directory - original behavior
			configDir := args[0]
			allDigests = parseAllEnvironments(configDir, "")
		} else {
			// Two config directories - parse both with prefixes
			leftDigests := parseAllEnvironments(args[0], "left")
			rightDigests := parseAllEnvironments(args[1], "right")
			allDigests = append(leftDigests, rightDigests...)
		}

		// Create table rows from digests
		rows := createTableRows(allDigests)

		// Apply environment filter if specified
		if envFilter != "" {
			rows = filterByEnvironments(rows, envFilter)
		}

		// Apply component filter if specified
		if componentFilter != "" {
			rows = filterByComponent(rows, componentFilter)
		}

		// Apply diff filter if specified
		if showDiffOnly {
			rows = filterByDiff(rows)
		}

		// Determine data structure and formatting
		var dataStructure string = "standard" // standard, wide, narrow
		var outputFormat string = "table"     // table, md, gs

		// Parse data structure options (mutually exclusive)
		var structureFormats []string
		var formatOptions []string

		for _, format := range outputFormats {
			switch format {
			case "wide", "narrow":
				structureFormats = append(structureFormats, format)
			case "table", "gs", "md":
				formatOptions = append(formatOptions, format)
			default:
				return fmt.Errorf("unsupported output format '%s'. Use 'table', 'gs', 'narrow', 'md', or 'wide'", format)
			}
		}

		// Validate mutual exclusivity of data structure formats
		if len(structureFormats) > 1 {
			return fmt.Errorf("data structure formats %v are mutually exclusive. Use only one: 'wide' or 'narrow'", structureFormats)
		}

		// Validate mutual exclusivity of output formats
		if len(formatOptions) > 1 {
			return fmt.Errorf("output formats %v are mutually exclusive. Use only one: 'table', 'md', or 'gs'", formatOptions)
		}

		// Set data structure
		if len(structureFormats) > 0 {
			dataStructure = structureFormats[0]
		}

		// Set output format
		if len(formatOptions) > 0 {
			outputFormat = formatOptions[0]
		}

		// Display results based on data structure and formatting
		switch dataStructure {
		case "wide":
			wideRows := createWideRows(rows)
			switch outputFormat {
			case "gs":
				displayWideGS(wideRows)
			case "table":
				displayWideTable(wideRows)
			case "md":
				displayWideMarkdown(wideRows)
			}
		case "narrow":
			switch outputFormat {
			case "table":
				displayNarrow(rows)
			case "md":
				displayNarrowMarkdown(rows)
			case "gs":
				displayNarrowGS(rows)
			}
		case "standard":
			switch outputFormat {
			case "gs":
				displayGS(rows)
			case "table":
				displayTable(rows)
			case "md":
				displayMarkdown(rows)
			}
		}

		return nil
	},
}

func init() {
	rootCmd.Flags().StringSliceVarP(&outputFormats, "output", "o", []string{"table"}, "Output format(s) (table|gs|narrow|md|wide)")
	rootCmd.Flags().StringVarP(&envFilter, "envs", "e", "", "Comma-separated list of environments to show (e.g. dev,int,stg)")
	rootCmd.Flags().StringVarP(&componentFilter, "component", "c", "", "Filter by component name(s) - comma-separated list (case-insensitive partial match)")
	rootCmd.Flags().BoolVarP(&showDiffOnly, "diff", "d", false, "Show only images with different digests across environments")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// parseAllEnvironments parses all required environments with proper precedence
func parseAllEnvironments(configDir string, prefix string) []DigestInfo {
	var allDigests []DigestInfo

	// Define environment configurations with proper precedence order
	envConfigs := []EnvConfig{
		{
			Cloud:       "dev",
			Environment: "dev",
			Sources: []ConfigSource{
				{File: "config.yaml", Path: "defaults"},
				{File: "config.yaml", Path: "clouds.dev.defaults"},
				{File: "config.yaml", Path: "clouds.dev.environments.dev.defaults"},
			},
		},
		{
			Cloud:       "dev",
			Environment: "cspr",
			Sources: []ConfigSource{
				{File: "config.yaml", Path: "defaults"},
				{File: "config.yaml", Path: "clouds.dev.defaults"},
				{File: "config.yaml", Path: "clouds.dev.environments.cspr.defaults"},
			},
		},
		{
			Cloud:       "public",
			Environment: "int",
			Sources: []ConfigSource{
				{File: "config.yaml", Path: "defaults"},
				{File: "config.msft.clouds-overlay.yaml", Path: "clouds.public.defaults"},
				{File: "config.msft.clouds-overlay.yaml", Path: "clouds.public.environments.int.defaults"},
			},
		},
		{
			Cloud:       "public",
			Environment: "stg",
			Sources: []ConfigSource{
				{File: "config.yaml", Path: "defaults"},
				{File: "config.msft.clouds-overlay.yaml", Path: "clouds.public.defaults"},
				{File: "config.msft.clouds-overlay.yaml", Path: "clouds.public.environments.stg.defaults"},
			},
		},
		{
			Cloud:       "public",
			Environment: "prod",
			Sources: []ConfigSource{
				{File: "config.yaml", Path: "defaults"},
				{File: "config.msft.clouds-overlay.yaml", Path: "clouds.public.defaults"},
				{File: "config.msft.clouds-overlay.yaml", Path: "clouds.public.environments.prod.defaults"},
			},
		},
	}

	// Process each environment
	for _, envConfig := range envConfigs {
		envDigests := parseEnvironment(configDir, envConfig, prefix)
		allDigests = append(allDigests, envDigests...)
	}

	return allDigests
}

// parseEnvironment parses a single environment with proper precedence
func parseEnvironment(configDir string, envConfig EnvConfig, prefix string) []DigestInfo {
	// Parse base config files
	configs := make(map[string]map[string]interface{})
	for _, source := range envConfig.Sources {
		filePath := filepath.Join(configDir, source.File)
		if configs[source.File] == nil {
			config, err := parseConfigFile(filePath)
			if err != nil {
				log.Printf("Warning: Error parsing %s: %v", source.File, err)
				continue
			}
			configs[source.File] = config
		}
	}

	// Build merged configuration with precedence
	mergedConfig := make(map[string]interface{})
	for _, source := range envConfig.Sources {
		if config, exists := configs[source.File]; exists {
			sectionData := getNestedValue(config, source.Path)
			if sectionData != nil {
				mergeConfigs(mergedConfig, sectionData)
			}
		}
	}

	// Extract digests from merged config
	environmentName := envConfig.Environment
	if prefix != "" {
		environmentName = prefix + "." + envConfig.Environment
	}
	digests := extractDigestsWithEnv(mergedConfig, envConfig.Cloud, environmentName, envConfig.Sources, configDir)

	// Add merge time and commit hash information
	for i := range digests {
		// Use relative path from current working directory to config files
		filePath := filepath.Join(configDir, digests[i].SourceFile)
		mergeTime, commitHash := getDigestMergeInfo(digests[i].Digest, filePath)
		digests[i].MergeTime = mergeTime
		digests[i].CommitHash = commitHash
	}

	return digests
}

func parseConfigFile(filepath string) (map[string]interface{}, error) {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, err
	}

	// Preprocess template variables to make YAML parseable
	processedData := preprocessTemplateVars(data)

	var config map[string]interface{}
	err = yaml.Unmarshal(processedData, &config)
	if err != nil {
		return nil, err
	}

	return config, nil
}

// preprocessTemplateVars replaces Go template variables with placeholder values
func preprocessTemplateVars(data []byte) []byte {
	content := string(data)

	// Replace {{ .ctx.* }} template variables with placeholder strings
	re := regexp.MustCompile(`{{\s*\.ctx\.\w+\s*}}`)
	content = re.ReplaceAllString(content, "PLACEHOLDER")

	return []byte(content)
}

// getNestedValue retrieves a nested value from a map using dot notation
func getNestedValue(data map[string]interface{}, path string) interface{} {
	if path == "" {
		return data
	}

	parts := strings.Split(path, ".")
	current := data

	for _, part := range parts {
		if current == nil {
			return nil
		}
		if nextLevel, exists := current[part]; exists {
			if nextMap, ok := nextLevel.(map[string]interface{}); ok {
				current = nextMap
			} else {
				return nextLevel
			}
		} else {
			return nil
		}
	}

	return current
}

// mergeConfigs recursively merges source into target, with source taking precedence
func mergeConfigs(target map[string]interface{}, source interface{}) {
	if sourceMap, ok := source.(map[string]interface{}); ok {
		for key, value := range sourceMap {
			if existingValue, exists := target[key]; exists {
				if existingMap, ok := existingValue.(map[string]interface{}); ok {
					if valueMap, ok := value.(map[string]interface{}); ok {
						// Both are maps, merge recursively
						mergeConfigs(existingMap, valueMap)
						continue
					}
				}
			}
			// Override with new value
			target[key] = value
		}
	}
}

// extractDigestsWithEnv extracts digests and tracks their source information
func extractDigestsWithEnv(data interface{}, cloud, environment string, sources []ConfigSource, configDir string) []DigestInfo {
	var digests []DigestInfo
	digests = append(digests, extractDigestsRecursive(data, cloud, environment, sources, configDir, "")...)
	return digests
}

// extractDigestsRecursive recursively extracts digests with source tracking
func extractDigestsRecursive(data interface{}, cloud, environment string, sources []ConfigSource, configDir, path string) []DigestInfo {
	var digests []DigestInfo

	switch v := data.(type) {
	case map[string]interface{}:
		for key, value := range v {
			currentPath := path
			if currentPath == "" {
				currentPath = key
			} else {
				currentPath = path + "." + key
			}

			if key == "digest" {
				if digestStr, ok := value.(string); ok && digestStr != "" && digestStr != "placeholder" {
					// Determine source file for this digest
					sourceFile := determineSourceFile(currentPath, sources, digestStr, configDir)
					digests = append(digests, DigestInfo{
						Digest:      digestStr,
						Component:   extractComponent(currentPath),
						Name:        extractImageName(currentPath),
						Environment: environment,
						Cloud:       cloud,
						SourceFile:  sourceFile,
						ConfigPath:  currentPath,
					})
				}
			} else {
				digests = append(digests, extractDigestsRecursive(value, cloud, environment, sources, configDir, currentPath)...)
			}
		}
	case []interface{}:
		for i, value := range v {
			currentPath := fmt.Sprintf("%s[%d]", path, i)
			digests = append(digests, extractDigestsRecursive(value, cloud, environment, sources, configDir, currentPath)...)
		}
	}

	return digests
}

// determineSourceFile determines which source file actually contains a specific digest
func determineSourceFile(configPath string, sources []ConfigSource, digest string, configDir string) string {
	// Check each source file in reverse order (most specific first) to see if it contains the digest
	for i := len(sources) - 1; i >= 0; i-- {
		sourceFile := sources[i].File
		filePath := filepath.Join(configDir, sourceFile)

		// Try to find the digest in this specific file
		if fileContainsDigest(filePath, digest) {
			return sourceFile
		}
	}

	// Fallback to the base config file
	return "config.yaml"
}

// fileContainsDigest checks if a specific file contains the given digest
func fileContainsDigest(filePath, digest string) bool {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), digest)
}

func extractDigests(data interface{}, filename, path string) []DigestInfo {
	var digests []DigestInfo

	switch v := data.(type) {
	case map[string]interface{}:
		for key, value := range v {
			currentPath := path
			if currentPath == "" {
				currentPath = key
			} else {
				currentPath = path + "." + key
			}

			if key == "digest" {
				if digestStr, ok := value.(string); ok && digestStr != "" && digestStr != "placeholder" {
					environment := extractEnvironment(currentPath)
					// Only include public cloud environments
					if environment == "int" || environment == "stg" || environment == "prod" {
						digests = append(digests, DigestInfo{
							Digest:      digestStr,
							Component:   extractComponent(currentPath),
							Name:        extractImageName(currentPath),
							Environment: environment,
						})
					}
				}
			} else {
				digests = append(digests, extractDigests(value, filename, currentPath)...)
			}
		}
	case []interface{}:
		for i, value := range v {
			currentPath := fmt.Sprintf("%s[%d]", path, i)
			digests = append(digests, extractDigests(value, filename, currentPath)...)
		}
	}

	return digests
}

func extractEnvironment(path string) string {
	parts := strings.Split(path, ".")

	// Look for environment names in the path
	for _, part := range parts {
		if part == "int" || part == "stg" || part == "prod" || part == "dev" || part == "cspr" {
			return part
		}
	}

	return "unknown"
}

func extractComponent(path string) string {
	parts := strings.Split(path, ".")

	// Look for known component names
	componentMap := map[string]string{
		"acm":                    "ACM",
		"maestro":               "Maestro",
		"frontend":              "Frontend",
		"backend":               "Backend",
		"hypershift":            "HyperShift",
		"pko":                   "PKO",
		"clustersService":       "Cluster Service",
		"imageSync":             "Image Sync",
		"prometheus":            "Monitoring",
		"routeMonitorOperator":  "Monitoring",
		"acrPull":               "ACR Pull",
		"secretSyncController":  "Secret Sync Controller",
		"backplaneAPI":          "Backplane API",
		"arobit":                "Logging",
	}

	for _, part := range parts {
		if component, exists := componentMap[part]; exists {
			return component
		}
	}

	return "Other"
}

func extractImageName(path string) string {
	parts := strings.Split(path, ".")

	// Check if this is a svc or mgmt prometheus component
	var clusterType string
	if contains(parts, "svc") {
		clusterType = "SVC "
	} else if contains(parts, "mgmt") {
		clusterType = "MGMT "
	}

	// Try to extract meaningful name from path
	var nameParts []string

	for i, part := range parts {
		switch part {
		case "operator", "bundle":
			if i > 0 {
				nameParts = append(nameParts, strings.Title(parts[i-1]) + " " + strings.Title(part))
			}
		case "mce":
			nameParts = append(nameParts, "MCE Bundle")
		case "server", "agent":
			nameParts = append(nameParts, "Maestro " + strings.Title(part))
		case "sidecar":
			nameParts = append(nameParts, "Sidecar")
		case "imagePackage":
			nameParts = append(nameParts, "Package Operator Package")
		case "imageManager":
			nameParts = append(nameParts, "Package Operator Manager")
		case "remotePhaseManager":
			nameParts = append(nameParts, "Remote Phase Manager")
		case "prometheusOperator":
			nameParts = append(nameParts, clusterType + "Prometheus Operator")
		case "prometheusSpec":
			nameParts = append(nameParts, clusterType + "Prometheus Spec")
		case "prometheusConfigReloader":
			nameParts = append(nameParts, clusterType + "Prometheus Config Reloader")
		case "operatorImage":
			nameParts = append(nameParts, "Route Monitor Operator")
		case "blackboxExporterImage":
			nameParts = append(nameParts, "Blackbox Exporter")
		case "forwarder":
			nameParts = append(nameParts, "Fluent Bit Forwarder")
		case "mdsd":
			nameParts = append(nameParts, "Geneva MDSD")
		case "ocMirror":
			nameParts = append(nameParts, "OC Mirror")
		}
	}

	if len(nameParts) > 0 {
		return strings.Join(nameParts, " ")
	}

	// Fallback to last meaningful part
	for i := len(parts) - 2; i >= 0; i-- {
		if parts[i] != "image" && parts[i] != "digest" {
			return strings.Title(parts[i])
		}
	}

	return "Unknown"
}

// Helper function to check if a slice contains a string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func groupByComponentsAndEnvironments(digests []DigestInfo) []ComponentGroup {
	componentMap := make(map[string]map[string][]DigestInfo)

	for _, digest := range digests {
		if componentMap[digest.Component] == nil {
			componentMap[digest.Component] = make(map[string][]DigestInfo)
		}
		// Use cloud.environment as key for grouping
		envKey := fmt.Sprintf("%s.%s", digest.Cloud, digest.Environment)
		componentMap[digest.Component][envKey] = append(componentMap[digest.Component][envKey], digest)
	}

	var components []ComponentGroup

	// Define component order and directories
	componentOrder := []string{
		"ACM", "Maestro", "Frontend", "Backend", "HyperShift",
		"PKO", "Cluster Service", "Image Sync", "Monitoring",
		"ACR Pull", "Secret Sync Controller", "Backplane API", "Logging", "Other",
	}

	directoryMap := map[string]string{
		"ACM":                    "acm/",
		"Maestro":               "maestro/",
		"Frontend":              "frontend/",
		"Backend":               "backend/",
		"HyperShift":            "hypershiftoperator/",
		"PKO":                   "pko/",
		"Cluster Service":       "cluster-service/",
		"Image Sync":            "image-sync/",
		"Monitoring":            "",
		"ACR Pull":              "",
		"Secret Sync Controller": "",
		"Backplane API":         "",
		"Logging":               "",
		"Other":                 "",
	}

	for _, componentName := range componentOrder {
		if envMap, exists := componentMap[componentName]; exists {
			// Sort digests within each environment
			for env, digestList := range envMap {
				sort.Slice(digestList, func(i, j int) bool {
					return digestList[i].Name < digestList[j].Name
				})
				envMap[env] = digestList
			}

			components = append(components, ComponentGroup{
				Name:         componentName,
				Directory:    directoryMap[componentName],
				Environments: envMap,
			})
		}
	}

	return components
}

// createTableRows converts digest information into table rows
func createTableRows(allDigests []DigestInfo) []TableRow {
	var rows []TableRow

	for _, digest := range allDigests {
		githubLink := fmt.Sprintf("https://github.com/Azure/ARO-HCP/commit/%s", digest.CommitHash)

		// Remove sha256: prefix from digest
		cleanDigest := digest.Digest
		if strings.HasPrefix(cleanDigest, "sha256:") {
			cleanDigest = strings.TrimPrefix(cleanDigest, "sha256:")
		}

		row := TableRow{
			Component: digest.Component,
			Cloud:     digest.Cloud,
			Env:       digest.Environment,
			Image:     digest.Name,
			Digest:    cleanDigest,
			Age:       digest.MergeTime,
			Rev:       githubLink,
		}
		rows = append(rows, row)
	}

	// Sort rows by Component, then Image, then Cloud, then Environment
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Component != rows[j].Component {
			return getComponentOrder(rows[i].Component) < getComponentOrder(rows[j].Component)
		}
		if rows[i].Image != rows[j].Image {
			return rows[i].Image < rows[j].Image
		}
		if rows[i].Cloud != rows[j].Cloud {
			return getCloudOrder(rows[i].Cloud) < getCloudOrder(rows[j].Cloud)
		}
		return getEnvOrder(rows[i].Env) < getEnvOrder(rows[j].Env)
	})

	return rows
}

// filterByEnvironments filters table rows to only include specified environments
func filterByEnvironments(rows []TableRow, envFilter string) []TableRow {
	if envFilter == "" {
		return rows
	}

	// Parse the comma-separated environment list
	envs := make(map[string]bool)
	for _, env := range strings.Split(envFilter, ",") {
		env = strings.TrimSpace(env)
		if env != "" {
			envs[env] = true
		}
	}

	// Filter rows to only include specified environments
	var filteredRows []TableRow
	for _, row := range rows {
		// Check exact match first
		if envs[row.Env] {
			filteredRows = append(filteredRows, row)
			continue
		}

		// Check suffix match for prefixed environments (e.g., "stg" matches "left.stg", "right.stg")
		for env := range envs {
			if strings.HasSuffix(row.Env, "."+env) {
				filteredRows = append(filteredRows, row)
				break
			}
		}
	}

	return filteredRows
}

// filterByComponent filters table rows to only include components matching the filter(s) (case-insensitive partial match)
func filterByComponent(rows []TableRow, componentFilter string) []TableRow {
	if componentFilter == "" {
		return rows
	}

	// Parse the comma-separated component list
	filters := make([]string, 0)
	for _, filter := range strings.Split(componentFilter, ",") {
		filter = strings.TrimSpace(filter)
		if filter != "" {
			filters = append(filters, strings.ToLower(filter))
		}
	}

	// Filter rows to only include components that match any of the filters
	var filteredRows []TableRow
	for _, row := range rows {
		componentLower := strings.ToLower(row.Component)
		for _, filter := range filters {
			if strings.Contains(componentLower, filter) {
				filteredRows = append(filteredRows, row)
				break
			}
		}
	}

	return filteredRows
}

// filterByDiff filters table rows to only include images that have different digests across environments
func filterByDiff(rows []TableRow) []TableRow {
	// Group rows by component and image
	imageGroups := make(map[string][]TableRow)

	for _, row := range rows {
		key := fmt.Sprintf("%s|%s", row.Component, row.Image)
		imageGroups[key] = append(imageGroups[key], row)
	}

	// Filter out images where all digests are the same
	var filteredRows []TableRow
	for _, group := range imageGroups {
		if len(group) == 0 {
			continue
		}

		// Check if all digests in the group are identical
		firstDigest := group[0].Digest
		hasDifferences := false

		for _, row := range group[1:] {
			if row.Digest != firstDigest {
				hasDifferences = true
				break
			}
		}

		// Only include this image if there are differences
		if hasDifferences {
			filteredRows = append(filteredRows, group...)
		}
	}

	return filteredRows
}

func getComponentOrder(component string) int {
	order := map[string]int{
		"ACM": 0, "Maestro": 1, "Frontend": 2, "Backend": 3, "HyperShift": 4,
		"PKO": 5, "Cluster Service": 6, "Image Sync": 7, "Monitoring": 8,
		"ACR Pull": 9, "Secret Sync Controller": 10, "Backplane API": 11, "Logging": 12, "Other": 13,
	}
	if idx, exists := order[component]; exists {
		return idx
	}
	return 999
}

func getCloudOrder(cloud string) int {
	switch cloud {
	case "dev":
		return 0
	case "public":
		return 1
	default:
		return 999
	}
}

func getEnvOrder(env string) int {
	// Handle prefixed environments
	baseEnv := env
	prefix := ""
	if strings.Contains(env, ".") {
		parts := strings.SplitN(env, ".", 2)
		if len(parts) == 2 {
			prefix = parts[0]
			baseEnv = parts[1]
		}
	}

	order := map[string]int{
		"dev": 0, "cspr": 1, "int": 2, "stg": 3, "prod": 4,
	}

	baseOrder := 999
	if idx, exists := order[baseEnv]; exists {
		baseOrder = idx
	}

	// Adjust order based on prefix: left comes before right
	if prefix == "left" {
		return baseOrder * 10
	} else if prefix == "right" {
		return baseOrder * 10 + 5
	}

	return baseOrder
}

func displayNarrow(rows []TableRow) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	// Print header
	fmt.Fprintln(w, "ENV\tIMAGE\tDIGEST\tREV AGE\tREVISION")

	// Print rows
	for _, row := range rows {
		// Limit digest to first 10 characters
		shortDigest := row.Digest
		if len(shortDigest) > 10 {
			shortDigest = shortDigest[:10]
		}

		// Extract just commit SHA from URL (last 10 characters of commit hash)
		shortRev := row.Rev
		if strings.Contains(shortRev, "/commit/") {
			// Extract commit hash from GitHub URL
			parts := strings.Split(shortRev, "/commit/")
			if len(parts) > 1 {
				commitHash := parts[1]
				if len(commitHash) >= 10 {
					shortRev = commitHash[:10]
				}
			}
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			row.Env, row.Image, shortDigest, row.Age, shortRev)
	}

	w.Flush()
}

func displayNarrowMarkdown(rows []TableRow) {
	// Print header
	fmt.Println("| ENV | IMAGE | DIGEST | REV AGE | REVISION |")
	fmt.Println("|-----|-------|--------|---------|----------|")

	// Print rows
	for _, row := range rows {
		// Limit digest to first 10 characters
		shortDigest := row.Digest
		if len(shortDigest) > 10 {
			shortDigest = shortDigest[:10]
		}

		// Extract commit SHA from URL
		shortRev := row.Rev
		if strings.Contains(shortRev, "/commit/") {
			parts := strings.Split(shortRev, "/commit/")
			if len(parts) > 1 {
				commitHash := parts[1]
				if len(commitHash) >= 10 {
					shortRev = commitHash[:10]
				}
			}
		}

		// Create markdown link for Rev column
		revMarkdown := fmt.Sprintf("[%s](%s)", shortRev, row.Rev)

		fmt.Printf("| %s | %s | %s | %s | %s |\n",
			row.Env, row.Image, shortDigest, row.Age, revMarkdown)
	}
}

func displayNarrowGS(rows []TableRow) {
	writer := csv.NewWriter(os.Stdout)
	defer writer.Flush()

	// Write header
	header := []string{"Env", "Image", "Digest", "Rev Age", "Revision"}
	writer.Write(header)

	// Write rows
	for _, row := range rows {
		// Limit digest to first 10 characters
		shortDigest := row.Digest
		if len(shortDigest) > 10 {
			shortDigest = shortDigest[:10]
		}

		// Extract commit SHA from URL
		shortRev := row.Rev
		if strings.Contains(shortRev, "/commit/") {
			parts := strings.Split(shortRev, "/commit/")
			if len(parts) > 1 {
				commitHash := parts[1]
				if len(commitHash) >= 10 {
					shortRev = commitHash[:10]
				}
			}
		}

		// Create hyperlink for Rev column
		revHyperlink := fmt.Sprintf(`=HYPERLINK("%s","%s")`, row.Rev, shortRev)

		record := []string{row.Env, row.Image, shortDigest, row.Age, revHyperlink}
		writer.Write(record)
	}
}

func displayMarkdown(rows []TableRow) {
	// Print header
	fmt.Println("| COMPONENT | CLOUD | ENV | IMAGE | DIGEST | REV AGE | REVISION |")
	fmt.Println("|-----------|-------|-----|-------|--------|---------|----------|")

	// Print rows
	for _, row := range rows {
		// Create markdown link for Rev column
		revMarkdown := fmt.Sprintf("[%s](%s)", row.Digest[:10], row.Rev)

		fmt.Printf("| %s | %s | %s | %s | %s | %s | %s |\n",
			row.Component, row.Cloud, row.Env, row.Image, row.Digest, row.Age, revMarkdown)
	}
}

func displayTable(rows []TableRow) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	// Print header
	fmt.Fprintln(w, "COMPONENT\tCLOUD\tENV\tIMAGE\tDIGEST\tREV AGE\tREVISION")

	// Print rows
	for _, row := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.Component, row.Cloud, row.Env, row.Image, row.Digest, row.Age, row.Rev)
	}

	w.Flush()
}

func displayGS(rows []TableRow) {
	writer := csv.NewWriter(os.Stdout)
	defer writer.Flush()

	// Write header
	header := []string{"Component", "Cloud", "Env", "Image", "Digest", "Rev Age", "Revision"}
	writer.Write(header)

	// Write rows
	for _, row := range rows {
		// Create hyperlink for revision: =HYPERLINK("url", "display_text")
		revHyperlink := fmt.Sprintf(`=HYPERLINK("%s","%s")`, row.Rev, row.Digest[:10])
		record := []string{row.Component, row.Cloud, row.Env, row.Image, row.Digest, row.Age, revHyperlink}
		writer.Write(record)
	}
}

type WideRow struct {
	Component string
	Image     string
	EnvData   map[string]EnvData // key is "cloud.env"
}

type EnvData struct {
	Age    string
	Digest string
	Rev    string
}

type WideTable struct {
	SortedEnvs     []string
	SortedWideRows []*WideRow
}

func createWideRows(rows []TableRow) *WideTable {
	// Group rows by component and image
	wideRows := make(map[string]*WideRow)
	envs := make(map[string]bool)

	for _, row := range rows {
		key := fmt.Sprintf("%s|%s", row.Component, row.Image)
		envKey := fmt.Sprintf("%s.%s", row.Cloud, row.Env)
		envs[envKey] = true

		if wideRows[key] == nil {
			wideRows[key] = &WideRow{
				Component: row.Component,
				Image:     row.Image,
				EnvData:   make(map[string]EnvData),
			}
		}

		wideRows[key].EnvData[envKey] = EnvData{
			Age:    row.Age,
			Digest: row.Digest,
			Rev:    row.Rev,
		}
	}

	// Sort environments
	var sortedEnvs []string
	for env := range envs {
		sortedEnvs = append(sortedEnvs, env)
	}
	sort.Slice(sortedEnvs, func(i, j int) bool {
		parts1 := strings.Split(sortedEnvs[i], ".")
		parts2 := strings.Split(sortedEnvs[j], ".")
		if parts1[0] != parts2[0] {
			return getCloudOrder(parts1[0]) < getCloudOrder(parts2[0])
		}
		return getEnvOrder(parts1[1]) < getEnvOrder(parts2[1])
	})

	// Sort wide rows
	var sortedWideRows []*WideRow
	for _, wideRow := range wideRows {
		sortedWideRows = append(sortedWideRows, wideRow)
	}
	sort.Slice(sortedWideRows, func(i, j int) bool {
		if sortedWideRows[i].Component != sortedWideRows[j].Component {
			return getComponentOrder(sortedWideRows[i].Component) < getComponentOrder(sortedWideRows[j].Component)
		}
		return sortedWideRows[i].Image < sortedWideRows[j].Image
	})

	return &WideTable{
		SortedEnvs:     sortedEnvs,
		SortedWideRows: sortedWideRows,
	}
}

func displayWideTable(wideTable *WideTable) {
	// Create tabwriter
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	// Print header
	fmt.Fprintf(w, "COMPONENT\tIMAGE")
	for _, env := range wideTable.SortedEnvs {
		fmt.Fprintf(w, "\t%s DIGEST\t%s REV AGE\t%s REVISION", strings.ToUpper(env), strings.ToUpper(env), strings.ToUpper(env))
	}
	fmt.Fprintln(w)

	// Print rows
	for _, wideRow := range wideTable.SortedWideRows {
		fmt.Fprintf(w, "%s\t%s", wideRow.Component, wideRow.Image)
		for _, env := range wideTable.SortedEnvs {
			if data, exists := wideRow.EnvData[env]; exists {
				// Extract short revision from URL
				shortRev := data.Rev
				if strings.Contains(shortRev, "/commit/") {
					parts := strings.Split(shortRev, "/commit/")
					if len(parts) > 1 {
						commitHash := parts[1]
						if len(commitHash) >= 10 {
							shortRev = commitHash[:10]
						}
					}
				}
				fmt.Fprintf(w, "\t%s\t%s\t%s", data.Digest, data.Age, shortRev)
			} else {
				fmt.Fprintf(w, "\t-\t-\t-")
			}
		}
		fmt.Fprintln(w)
	}

	w.Flush()
}

func displayWideMarkdown(wideTable *WideTable) {
	// Build header
	header := "| COMPONENT | IMAGE"
	separator := "|-----------|-------"
	for _, env := range wideTable.SortedEnvs {
		header += fmt.Sprintf(" | %s DIGEST | %s REV AGE | %s REVISION", strings.ToUpper(env), strings.ToUpper(env), strings.ToUpper(env))
		separator += "|--------|---------|-------"
	}
	header += " |"
	separator += " |"

	fmt.Println(header)
	fmt.Println(separator)

	// Write rows
	for _, wideRow := range wideTable.SortedWideRows {
		row := fmt.Sprintf("| %s | %s", wideRow.Component, wideRow.Image)
		for _, env := range wideTable.SortedEnvs {
			if data, exists := wideRow.EnvData[env]; exists {
				// Extract short revision from URL
				shortRev := data.Rev
				if strings.Contains(shortRev, "/commit/") {
					parts := strings.Split(shortRev, "/commit/")
					if len(parts) > 1 {
						commitHash := parts[1]
						if len(commitHash) >= 10 {
							shortRev = commitHash[:10]
						}
					}
				}
				// Create markdown link for revision
				revLink := fmt.Sprintf("[%s](%s)", shortRev, data.Rev)
				row += fmt.Sprintf(" | %s | %s | %s", data.Digest, data.Age, revLink)
			} else {
				row += " | - | - | -"
			}
		}
		row += " |"
		fmt.Println(row)
	}
}

func displayWideGS(wideTable *WideTable) {
	writer := csv.NewWriter(os.Stdout)
	defer writer.Flush()

	// Build header
	header := []string{"Component", "Image"}
	for _, env := range wideTable.SortedEnvs {
		header = append(header, fmt.Sprintf("%s\nDigest", env))
		header = append(header, fmt.Sprintf("%s\nRev Age", env))
		header = append(header, fmt.Sprintf("%s\nRevision", env))
	}
	writer.Write(header)

	// Write rows
	for _, wideRow := range wideTable.SortedWideRows {
		record := []string{wideRow.Component, wideRow.Image}
		for _, env := range wideTable.SortedEnvs {
			if data, exists := wideRow.EnvData[env]; exists {
				// Extract short revision from URL
				shortRev := data.Rev
				if strings.Contains(shortRev, "/commit/") {
					parts := strings.Split(shortRev, "/commit/")
					if len(parts) > 1 {
						commitHash := parts[1]
						if len(commitHash) >= 10 {
							shortRev = commitHash[:10]
						}
					}
				}
				// Create hyperlink for revision: =HYPERLINK("url", "display_text")
				revHyperlink := fmt.Sprintf(`=HYPERLINK("%s","%s")`, data.Rev, shortRev)
				record = append(record, data.Digest, data.Age, revHyperlink)
			} else {
				record = append(record, "-", "-", "-")
			}
		}
		writer.Write(record)
	}
}

func getDigestMergeInfo(digest, filePath string) (string, string) {
	// Use git blame to find when this specific line was last modified
	cmd := exec.Command("git", "blame", filePath)
	output, err := cmd.Output()
	if err != nil {
		return "unknown", "unknown"
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, digest) {
			// Parse git blame line format: "commit (author date time timezone linenum) content"
			// Extract just the commit hash (first field)
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}

			commitHash := fields[0]
			if strings.HasPrefix(commitHash, "^") {
				commitHash = commitHash[1:] // Remove ^ prefix for initial commit
			}

			// Get timestamp for this commit
			cmd = exec.Command("git", "show", "-s", "--format=%ct", commitHash)
			timestampOutput, err := cmd.Output()
			if err != nil {
				return "unknown", commitHash
			}

			timestampStr := strings.TrimSpace(string(timestampOutput))
			timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
			if err != nil {
				return "unknown", commitHash
			}

			commitTime := time.Unix(timestamp, 0)
			relativeTime := formatRelativeTime(time.Since(commitTime))

			return relativeTime, commitHash
		}
	}

	return "unknown", "unknown"
}

func formatRelativeTime(duration time.Duration) string {
	if duration < time.Minute {
		return fmt.Sprintf("%ds", int(duration.Seconds()))
	}
	if duration < time.Hour {
		return fmt.Sprintf("%dm", int(duration.Minutes()))
	}
	if duration < 24*time.Hour {
		return fmt.Sprintf("%dh", int(duration.Hours()))
	}
	if duration < 30*24*time.Hour {
		return fmt.Sprintf("%dd", int(duration.Hours()/24))
	}
	if duration < 365*24*time.Hour {
		return fmt.Sprintf("%dmo", int(duration.Hours()/(24*30)))
	}
	return fmt.Sprintf("%dy", int(duration.Hours()/(24*365)))
}
