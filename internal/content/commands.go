package content

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agentctl/internal/tx"
)

// ── Format conversion ───────────────────────────────────────────────

// validCommandFormats defines supported command file formats.
var validCommandFormats = map[string]bool{
	"":     true, // default = md (plain copy)
	"md":   true,
	"toml": true,
}

// convertMdToToml converts a Claude Code .md command file to Gemini CLI .toml format.
//
// Input (.md with optional YAML front matter):
//
//	---
//	description: Some description
//	---
//	Prompt body with $ARGUMENTS placeholder
//
// Output (.toml):
//
//	description = "Some description"
//	prompt = """
//	Prompt body with {{args}} placeholder
//	"""
func convertMdToToml(mdContent string) string {
	description, body := parseFrontMatter(mdContent)

	// Map Claude Code placeholder to Gemini CLI placeholder
	body = strings.ReplaceAll(body, "$ARGUMENTS", "{{args}}")

	var sb strings.Builder
	fmt.Fprintf(&sb, "description = %q\n", description)
	sb.WriteString("prompt = \"\"\"\n")
	sb.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString("\"\"\"\n")
	return sb.String()
}

// parseFrontMatter extracts YAML front matter and body from a markdown file.
// Returns (description, body). If no front matter, description is empty.
func parseFrontMatter(md string) (string, string) {
	if !strings.HasPrefix(md, "---\n") {
		return "", md
	}
	end := strings.Index(md[4:], "\n---")
	if end < 0 {
		return "", md
	}
	frontMatter := md[4 : 4+end]
	body := strings.TrimLeft(md[4+end+4:], "\n")

	var description string
	for _, line := range strings.Split(frontMatter, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "description:") {
			description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			break
		}
	}
	return description, body
}

// targetFileName returns the target filename for a source file, applying
// extension conversion based on format.
func targetFileName(sourceName, format string) string {
	if format == "toml" && strings.HasSuffix(sourceName, ".md") {
		return strings.TrimSuffix(sourceName, ".md") + ".toml"
	}
	return sourceName
}

// fileStem returns the filename without extension.
func fileStem(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// ── Directory sync helpers ───────────────────────────────────────────

// dirSyncPlan compares source dir files with target dir; returns per-file plan items.
// It also detects stale files in target that no longer exist in source.
// The format parameter controls filename extension mapping and content conversion:
//   - "" or "md": plain copy (no conversion)
//   - "toml": convert .md → .toml (filename extension + content format)
func dirSyncPlan(sourceDir, targetDir, agent, itemType, format string) []map[string]any {
	var items []map[string]any

	// Track expected target filenames to detect stale targets later.
	expectedTargets := make(map[string]bool)

	if _, err := os.Stat(sourceDir); !os.IsNotExist(err) {
		entries, err := os.ReadDir(sourceDir)
		if err == nil {
			// Sort entries for deterministic output
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].Name() < entries[j].Name()
			})

			for _, entry := range entries {
				if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || entry.Name() == "config.json" {
					continue
				}
				// Skip symlinks to prevent following them outside source tree
				if entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				srcName := entry.Name()
				tgtName := targetFileName(srcName, format)
				expectedTargets[tgtName] = true

				srcFile := filepath.Join(sourceDir, srcName)
				tgtFile := filepath.Join(targetDir, tgtName)

				desired, err := os.ReadFile(srcFile)
				if err != nil {
					continue
				}

				// Convert content if format requires it
				desiredStr := string(desired)
				if format == "toml" {
					desiredStr = convertMdToToml(desiredStr)
				}

				var current string
				tgtExists := false
				if _, statErr := os.Stat(tgtFile); statErr == nil {
					tgtExists = true
					currentBytes, _ := os.ReadFile(tgtFile)
					current = string(currentBytes)
				}

				item := map[string]any{
					"agent":   agent,
					"type":    itemType,
					"path":    tgtFile,
					"source":  srcFile,
					"exists":  tgtExists,
					"changed": current != desiredStr,
				}
				if format != "" && format != "md" {
					item["format"] = format
				}
				items = append(items, item)
			}
		}
	}

	// Detect stale files: files in target that don't match any expected target.
	if _, err := os.Stat(targetDir); !os.IsNotExist(err) {
		tgtEntries, err := os.ReadDir(targetDir)
		if err == nil {
			sort.Slice(tgtEntries, func(i, j int) bool {
				return tgtEntries[i].Name() < tgtEntries[j].Name()
			})

			for _, entry := range tgtEntries {
				if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || entry.Name() == "config.json" {
					continue
				}
				if !expectedTargets[entry.Name()] {
					tgtFile := filepath.Join(targetDir, entry.Name())
					items = append(items, map[string]any{
						"agent":   agent,
						"type":    itemType,
						"path":    tgtFile,
						"exists":  true,
						"changed": true,
						"stale":   true,
					})
				}
			}
		}
	}

	return items
}

// ── Config loading ───────────────────────────────────────────────────

// loadCommandsConfig loads commands/config.json from configDir.
// H6: validates that all agent target_dir values are non-empty and under $HOME.
func loadCommandsConfig(configDir string) (map[string]any, error) {
	path := filepath.Join(configDir, "commands", "config.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return map[string]any{"agents": map[string]any{}}, nil
	}
	cfg, err := tx.ReadJSON(path)
	if err != nil {
		return nil, fmt.Errorf("commands config: %w", err)
	}
	// Validate targets and formats
	agents := tx.GetMap(cfg, "agents")
	for name, val := range agents {
		agentCfg, ok := val.(map[string]any)
		if !ok {
			continue
		}
		target := tx.GetString(agentCfg, "target_dir", "")
		if target == "" {
			return nil, fmt.Errorf("commands config: agent %q has empty target_dir", name)
		}
		format := tx.GetString(agentCfg, "format", "")
		if !validCommandFormats[format] {
			return nil, fmt.Errorf("commands config: agent %q has invalid format: %q (expected md or toml)", name, format)
		}
	}
	return cfg, nil
}

// ── Apply ────────────────────────────────────────────────────────────

// applyCommandChange copies a command source file to the target path atomically,
// or deletes the target if the item is stale (no longer in source).
// When the item has format="toml", source .md content is converted before writing.
func applyCommandChange(item map[string]any, path string) error {
	if tx.GetBool(item, "stale", false) {
		return os.Remove(path)
	}
	source := tx.GetString(item, "source", "")
	if source == "" {
		return fmt.Errorf("missing source for command item")
	}
	// Reject symlinked source to prevent reading outside source tree
	if err := tx.RejectSymlink(source); err != nil {
		return fmt.Errorf("source symlink check: %w", err)
	}
	// Read source content
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read source %s: %w", source, err)
	}
	// Convert format if needed
	content := string(data)
	format := tx.GetString(item, "format", "")
	if format == "toml" {
		content = convertMdToToml(content)
	}
	return tx.WriteTextAtomic(path, content)
}
