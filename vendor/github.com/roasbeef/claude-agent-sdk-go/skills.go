package claudeagent

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Skill represents a filesystem-based capability extension.
//
// Skills are discovered from ~/.claude/skills/ (user) and .claude/skills/
// (project). Each Skill is a directory containing a SKILL.md file with YAML
// frontmatter and Markdown instructions.
type Skill struct {
	// Name is the Skill identifier from YAML frontmatter.
	Name string

	// Description explains what the Skill does and when to use it.
	// This field is critical for Claude's autonomous Skill discovery.
	Description string

	// AllowedTools restricts which tools can be used when Skill is active.
	// Optional. Empty means no restrictions.
	AllowedTools []string

	// Content is the full SKILL.md file content including frontmatter.
	Content string

	// Path is the filesystem path to the SKILL.md file.
	Path string

	// Scope is either "user" or "project" indicating where the Skill was loaded from.
	Scope string

	// SupportFiles lists additional files in the Skill directory.
	// Examples: reference.md, examples.md, scripts/, templates/
	SupportFiles []string
}

// SkillMetadata represents parsed YAML frontmatter from SKILL.md.
type SkillMetadata struct {
	Name         string   `yaml:"name"`
	Description  string   `yaml:"description"`
	AllowedTools []string `yaml:"allowed-tools,omitempty"`
}

// SkillLoader discovers and loads Skills from filesystem.
//
// SkillLoader scans both user Skills directory (~/.claude/skills/) and project
// Skills directory (.claude/skills/) to discover Skill definitions. Each Skill
// must have a SKILL.md file with valid YAML frontmatter.
type SkillLoader struct {
	userSkillsDir    string
	projectSkillsDir string
}

// NewSkillLoader creates a loader with the given Skills directories.
//
// If userSkillsDir is empty, defaults to ~/.claude/skills/
// If projectSkillsDir is empty, defaults to ./.claude/skills/
func NewSkillLoader(userSkillsDir, projectSkillsDir string) *SkillLoader {
	// Default user Skills directory
	if userSkillsDir == "" {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			userSkillsDir = filepath.Join(homeDir, ".claude", "skills")
		}
	}

	// Default project Skills directory
	if projectSkillsDir == "" {
		cwd, err := os.Getwd()
		if err == nil {
			projectSkillsDir = filepath.Join(cwd, ".claude", "skills")
		}
	}

	return &SkillLoader{
		userSkillsDir:    userSkillsDir,
		projectSkillsDir: projectSkillsDir,
	}
}

// Load discovers and loads all Skills from configured directories.
//
// Load scans user Skills directory and project Skills directory in that order.
// Skills with the same name in project scope override user scope.
func (l *SkillLoader) Load() ([]Skill, error) {
	skills := make([]Skill, 0)

	// Load user Skills
	if l.userSkillsDir != "" {
		userSkills, err := l.loadFromDirectory(l.userSkillsDir, "user")
		if err != nil && !os.IsNotExist(err) {
			// Log warning but continue
			// In production, use structured logging here
			_ = err
		}
		skills = append(skills, userSkills...)
	}

	// Load project Skills
	if l.projectSkillsDir != "" {
		projectSkills, err := l.loadFromDirectory(l.projectSkillsDir, "project")
		if err != nil && !os.IsNotExist(err) {
			// Log warning but continue
			_ = err
		}
		skills = append(skills, projectSkills...)
	}

	return skills, nil
}

// loadFromDirectory scans a directory for Skill subdirectories.
func (l *SkillLoader) loadFromDirectory(dir string, scope string) ([]Skill, error) {
	// Check if directory exists
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read Skills directory %s: %w", dir, err)
	}

	skills := make([]Skill, 0)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue // Skip non-directories
		}

		skillPath := filepath.Join(dir, entry.Name())
		skill, err := l.LoadFromPath(skillPath, scope)
		if err != nil {
			// Log warning but continue loading other Skills
			// Invalid Skills are skipped
			continue
		}

		skills = append(skills, *skill)
	}

	return skills, nil
}

// LoadFromPath loads a single Skill from the given directory.
//
// The directory must contain a SKILL.md file with valid YAML frontmatter.
func (l *SkillLoader) LoadFromPath(path string, scope string) (*Skill, error) {
	skillMdPath := filepath.Join(path, "SKILL.md")

	// Read SKILL.md file
	content, err := os.ReadFile(skillMdPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read SKILL.md at %s: %w", skillMdPath, err)
	}

	// Parse frontmatter and Markdown
	metadata, _, err := parseSKILLMd(content)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SKILL.md at %s: %w", skillMdPath, err)
	}

	// Validate required fields
	if validateErr := validateSkillMetadata(metadata); validateErr != nil {
		return nil, fmt.Errorf("invalid SKILL.md at %s: %w", skillMdPath, validateErr)
	}

	// Discover supporting files
	supportFiles, err := discoverSupportFiles(path)
	if err != nil {
		// Log warning but continue
		supportFiles = []string{}
	}

	// Reconstruct full content (frontmatter + markdown)
	fullContent := string(content)

	return &Skill{
		Name:         metadata.Name,
		Description:  metadata.Description,
		AllowedTools: metadata.AllowedTools,
		Content:      fullContent,
		Path:         skillMdPath,
		Scope:        scope,
		SupportFiles: supportFiles,
	}, nil
}

// ValidateSKILLMd validates a SKILL.md file without loading the full Skill.
func (l *SkillLoader) ValidateSKILLMd(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read SKILL.md: %w", err)
	}

	metadata, _, err := parseSKILLMd(content)
	if err != nil {
		return fmt.Errorf("failed to parse SKILL.md: %w", err)
	}

	return validateSkillMetadata(metadata)
}

// parseSKILLMd extracts YAML frontmatter and Markdown content.
//
// SKILL.md format:
//
//	---
//	name: Skill Name
//	description: What this Skill does
//	allowed-tools: tool1, tool2
//	---
//
//	# Markdown content here
func parseSKILLMd(content []byte) (SkillMetadata, string, error) {
	// Split by "---" delimiters
	parts := bytes.SplitN(content, []byte("---"), 3)

	if len(parts) < 3 {
		return SkillMetadata{}, "", errors.New("invalid SKILL.md format: missing frontmatter delimiters")
	}

	// parts[0] is empty (before first ---)
	// parts[1] is YAML frontmatter
	// parts[2] is Markdown content

	yamlContent := parts[1]
	markdownContent := string(bytes.TrimSpace(parts[2]))

	// Parse YAML frontmatter
	var metadata SkillMetadata
	if err := yaml.Unmarshal(yamlContent, &metadata); err != nil {
		return SkillMetadata{}, "", fmt.Errorf("failed to parse YAML frontmatter: %w", err)
	}

	return metadata, markdownContent, nil
}

// validateSkillMetadata validates required fields in Skill metadata.
func validateSkillMetadata(metadata SkillMetadata) error {
	if strings.TrimSpace(metadata.Name) == "" {
		return &ErrSkillInvalid{
			Field:  "name",
			Reason: "name is required",
		}
	}

	if strings.TrimSpace(metadata.Description) == "" {
		return &ErrSkillInvalid{
			Field:  "description",
			Reason: "description is required (critical for Skill discovery)",
		}
	}

	return nil
}

// discoverSupportFiles finds additional files in a Skill directory.
//
// Returns paths relative to the Skill directory root.
// Common support files: reference.md, examples.md, scripts/, templates/
func discoverSupportFiles(skillDir string) ([]string, error) {
	entries, err := os.ReadDir(skillDir)
	if err != nil {
		return nil, err
	}

	supportFiles := make([]string, 0)

	for _, entry := range entries {
		name := entry.Name()

		// Skip SKILL.md (already processed)
		if name == "SKILL.md" {
			continue
		}

		// Skip hidden files
		if strings.HasPrefix(name, ".") {
			continue
		}

		supportFiles = append(supportFiles, name)
	}

	return supportFiles, nil
}
