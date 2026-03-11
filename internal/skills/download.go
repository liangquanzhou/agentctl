package skills

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SkillMeta holds metadata about a discovered or installed skill.
type SkillMeta struct {
	Name        string // skill name (from front matter or directory name)
	Description string // skill description (from front matter)
	SourceURL   string // original GitHub URL
	LocalPath   string // path in agentctl skills source
}

// ── Download ─────────────────────────────────────────────────────────

// Download clones a GitHub repo, finds SKILL.md files, and copies matching
// skill directories to configDir/skills/<name>/. Returns metadata for each
// installed skill. If installAll is true, all discovered skills are installed
// without prompting; otherwise only a single-skill repo is auto-installed.
func Download(source string, configDir string, installAll bool) ([]SkillMeta, error) {
	repoURL := normalizeGitHubURL(source)
	if repoURL == "" {
		return nil, fmt.Errorf("invalid source: %q (expected GitHub URL or user/repo)", source)
	}

	// Clone to temp dir
	tmpDir, err := os.MkdirTemp("", "agentctl-skill-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	cloneDir := filepath.Join(tmpDir, "repo")
	cmd := exec.Command("git", "clone", "--depth", "1", repoURL, cloneDir)
	cmd.Stdout = os.Stderr // show progress on stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git clone %s: %w", repoURL, err)
	}

	// Find all SKILL.md files in the cloned repo
	found := findSkillDirs(cloneDir)
	if len(found) == 0 {
		return nil, fmt.Errorf("no SKILL.md found in %s", repoURL)
	}

	// Determine which skills to install
	var toInstall []skillCandidate
	if len(found) == 1 {
		toInstall = found
	} else if installAll {
		toInstall = found
	} else {
		return nil, fmt.Errorf(
			"repo contains %d skills: %s\nuse --all to install all, or specify a more specific path",
			len(found), candidateNames(found),
		)
	}

	// Install each skill
	skillsDir := filepath.Join(configDir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create skills dir: %w", err)
	}

	var installed []SkillMeta
	for _, c := range toInstall {
		meta, err := installSkill(c, skillsDir, source)
		if err != nil {
			return installed, fmt.Errorf("install skill %s: %w", c.name, err)
		}
		installed = append(installed, *meta)
	}

	return installed, nil
}

// ── ParseSkillMD ─────────────────────────────────────────────────────

// ParseSkillMD reads a SKILL.md file and extracts name/description from
// optional YAML front matter (delimited by ---).
func ParseSkillMD(path string) (*SkillMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	meta := &SkillMeta{}
	scanner := bufio.NewScanner(f)

	// Check for YAML front matter
	if scanner.Scan() {
		firstLine := strings.TrimSpace(scanner.Text())
		if firstLine == "---" {
			// Parse front matter lines until closing ---
			for scanner.Scan() {
				line := scanner.Text()
				if strings.TrimSpace(line) == "---" {
					break
				}
				parseFrontMatterLine(line, meta)
			}
		}
	}

	return meta, scanner.Err()
}

// ── Remove ───────────────────────────────────────────────────────────

// Remove deletes a skill from configDir/skills/<name>/.
func Remove(name string, configDir string) error {
	if err := validateSkillName(name); err != nil {
		return err
	}

	skillDir := filepath.Join(configDir, "skills", name)
	if _, err := os.Stat(skillDir); os.IsNotExist(err) {
		return fmt.Errorf("skill %q not found in %s", name, filepath.Join(configDir, "skills"))
	}

	return os.RemoveAll(skillDir)
}

// ── Search ───────────────────────────────────────────────────────────

// Search wraps the `skills find` CLI if available, otherwise returns
// instructions to search manually.
func Search(query string) (string, error) {
	skillsPath, err := exec.LookPath("skills")
	if err != nil {
		return fmt.Sprintf(
			"'skills' CLI not found.\n"+
				"Search at https://skills.sh or provide a GitHub URL directly:\n"+
				"  agentctl skills add <github-url-or-user/repo>",
		), nil
	}

	cmd := exec.Command(skillsPath, "find", query)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("skills find %s: %w\n%s", query, err, string(out))
	}

	return strings.TrimSpace(string(out)), nil
}

// ── Internal types ───────────────────────────────────────────────────

// skillCandidate represents a skill directory found in a cloned repo.
type skillCandidate struct {
	name    string // resolved skill name
	dir     string // absolute path to the skill directory
	relPath string // relative path within the repo
}

// ── Internal helpers ─────────────────────────────────────────────────

// normalizeGitHubURL converts various GitHub reference formats to a
// clone-able HTTPS URL. Returns empty string if the input is not
// recognized.
func normalizeGitHubURL(source string) string {
	s := strings.TrimSpace(source)

	// Strip trailing .git
	s = strings.TrimSuffix(s, ".git")

	// Full HTTPS URL
	if strings.HasPrefix(s, "https://github.com/") {
		return s + ".git"
	}

	// github.com/user/repo (no scheme)
	if strings.HasPrefix(s, "github.com/") {
		return "https://" + s + ".git"
	}

	// user/repo shorthand (must have exactly one slash, no dots before it)
	parts := strings.SplitN(s, "/", 3)
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" &&
		!strings.Contains(parts[0], ".") &&
		!strings.Contains(parts[0], ":") {
		return "https://github.com/" + s + ".git"
	}

	// SSH URL: git@github.com:user/repo
	if strings.HasPrefix(s, "git@github.com:") {
		path := strings.TrimPrefix(s, "git@github.com:")
		return "https://github.com/" + path + ".git"
	}

	return ""
}

// findSkillDirs walks a cloned repo directory and returns all directories
// containing a SKILL.md or skill.md marker file.
func findSkillDirs(repoDir string) []skillCandidate {
	var candidates []skillCandidate

	// First check if the repo root itself is a skill
	if isSkillDir(repoDir) {
		meta, _ := parseSkillMDInDir(repoDir)
		name := resolveSkillName(meta, filepath.Base(repoDir))
		candidates = append(candidates, skillCandidate{
			name:    name,
			dir:     repoDir,
			relPath: ".",
		})
		return candidates // Root skill = the whole repo is one skill
	}

	// Walk one level deep for skill directories
	entries, err := os.ReadDir(repoDir)
	if err != nil {
		return candidates
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirName := entry.Name()
		if strings.HasPrefix(dirName, ".") {
			continue // skip hidden directories
		}
		dirPath := filepath.Join(repoDir, dirName)
		if isSkillDir(dirPath) {
			meta, _ := parseSkillMDInDir(dirPath)
			name := resolveSkillName(meta, dirName)
			candidates = append(candidates, skillCandidate{
				name:    name,
				dir:     dirPath,
				relPath: dirName,
			})
		}
	}

	// If no top-level skills, do a deeper walk
	if len(candidates) == 0 {
		filepath.WalkDir(repoDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			if path == repoDir {
				return nil
			}
			if isSkillDir(path) {
				rel, _ := filepath.Rel(repoDir, path)
				meta, _ := parseSkillMDInDir(path)
				name := resolveSkillName(meta, d.Name())
				candidates = append(candidates, skillCandidate{
					name:    name,
					dir:     path,
					relPath: rel,
				})
				return filepath.SkipDir // don't recurse into skill dirs
			}
			return nil
		})
	}

	return candidates
}

// parseSkillMDInDir finds and parses the SKILL.md file in a directory.
func parseSkillMDInDir(dir string) (*SkillMeta, error) {
	for _, marker := range skillMarkers {
		path := filepath.Join(dir, marker)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return ParseSkillMD(path)
		}
	}
	return nil, fmt.Errorf("no SKILL.md found in %s", dir)
}

// resolveSkillName determines the skill name from front matter or falls
// back to the directory name.
func resolveSkillName(meta *SkillMeta, dirName string) string {
	if meta != nil && meta.Name != "" {
		// Sanitize: use the name as-is if it's a valid skill name
		if validateSkillName(meta.Name) == nil {
			return meta.Name
		}
	}
	return dirName
}

// parseFrontMatterLine extracts key-value pairs from a YAML front matter
// line. Only handles simple "key: value" format (no nested YAML).
func parseFrontMatterLine(line string, meta *SkillMeta) {
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return
	}
	key := strings.TrimSpace(line[:idx])
	val := strings.TrimSpace(line[idx+1:])

	// Strip surrounding quotes
	if len(val) >= 2 {
		if (val[0] == '"' && val[len(val)-1] == '"') ||
			(val[0] == '\'' && val[len(val)-1] == '\'') {
			val = val[1 : len(val)-1]
		}
	}

	switch strings.ToLower(key) {
	case "name":
		meta.Name = val
	case "description":
		meta.Description = val
	}
}

// installSkill copies a skill candidate into the skills directory.
func installSkill(c skillCandidate, skillsDir string, sourceURL string) (*SkillMeta, error) {
	if err := validateSkillName(c.name); err != nil {
		return nil, fmt.Errorf("invalid skill name %q: %w", c.name, err)
	}

	dst := filepath.Join(skillsDir, c.name)

	// Copy skill directory (reuse the existing replaceTree)
	if err := replaceTree(c.dir, dst, false); err != nil {
		return nil, err
	}

	// Remove .git if it was copied (root-level skill)
	gitDir := filepath.Join(dst, ".git")
	if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
		os.RemoveAll(gitDir)
	}

	meta, _ := parseSkillMDInDir(dst)
	result := &SkillMeta{
		Name:      c.name,
		SourceURL: sourceURL,
		LocalPath: dst,
	}
	if meta != nil {
		if meta.Description != "" {
			result.Description = meta.Description
		}
	}

	return result, nil
}

// candidateNames returns a comma-separated list of skill candidate names.
func candidateNames(candidates []skillCandidate) string {
	names := make([]string, len(candidates))
	for i, c := range candidates {
		names[i] = c.name
	}
	return strings.Join(names, ", ")
}
