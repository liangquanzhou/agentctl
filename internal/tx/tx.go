// Package tx provides shared atomic I/O, snapshot, and lock primitives.
//
// Extracted as a foundational package so that both the MCP control plane (engine)
// and content plane (content) can reuse the same transactional helpers.
package tx

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

// ── Timestamp ────────────────────────────────────────────────────────

// UTCNowISO returns the current UTC time in ISO8601 format with "Z" suffix.
func UTCNowISO() string {
	return time.Now().UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z")
}

// TodayISO returns today's date in ISO8601 format (YYYY-MM-DD).
func TodayISO() string {
	return time.Now().UTC().Format("2006-01-02")
}

// ── Hashing ──────────────────────────────────────────────────────────

// SHA256File computes the SHA-256 hex digest of a file.
// Rejects symlinked paths to prevent following symlinks to unexpected locations.
func SHA256File(path string) (string, error) {
	if err := RejectSymlink(path); err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	buf := make([]byte, 65536)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SHA256Bytes computes the SHA-256 hex digest of a byte slice.
func SHA256Bytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// ── Path helpers ─────────────────────────────────────────────────────

// EnsureParent creates the parent directory of path if it doesn't exist.
func EnsureParent(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

// HomeDir returns the current user's home directory.
func HomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return os.Getenv("HOME")
	}
	return home
}

// ExpandUser expands ~ to the home directory.
func ExpandUser(path string) string {
	if len(path) == 0 {
		return path
	}
	if path[0] == '~' {
		return filepath.Join(HomeDir(), path[1:])
	}
	return path
}

// IsUnderHome validates that a resolved path is under $HOME.
// It resolves symlinks on both home and the target path for proper comparison.
func IsUnderHome(path string) error {
	home := HomeDir()
	homeResolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		homeResolved = home
	}
	// Resolve the path; if it doesn't exist yet, resolve the parent
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		// Path may not exist yet -- resolve parent
		parentResolved, perr := filepath.EvalSymlinks(filepath.Dir(path))
		if perr != nil {
			parentResolved = filepath.Dir(path)
		}
		resolved = filepath.Join(parentResolved, filepath.Base(path))
	}
	resolved = filepath.Clean(resolved)
	homeResolved = filepath.Clean(homeResolved)
	if resolved == homeResolved {
		return nil
	}
	if strings.HasPrefix(resolved, homeResolved+string(filepath.Separator)) {
		return nil
	}
	return fmt.Errorf("path escapes home directory: %s (resolved: %s)", path, resolved)
}

// RejectSymlink checks if path is a symlink and returns an error if so.
// If the path does not exist, returns nil (safe for not-yet-created paths).
func RejectSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return nil // doesn't exist yet, OK
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to operate on symlinked path: %s", path)
	}
	return nil
}

// ── Read ─────────────────────────────────────────────────────────────

// ReadJSON reads and parses a JSON file into a map.
func ReadJSON(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("invalid json in %s: %w", path, err)
	}
	if result == nil {
		return nil, fmt.Errorf("expected object json: %s", path)
	}
	return result, nil
}

// ReadTOML reads and parses a TOML file into a map.
func ReadTOML(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := toml.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("invalid toml in %s: %w", path, err)
	}
	if result == nil {
		return nil, fmt.Errorf("expected table toml: %s", path)
	}
	return result, nil
}

// ── Atomic write ─────────────────────────────────────────────────────

// WriteJSONAtomic writes data to path atomically (write to temp, then rename).
func WriteJSONAtomic(path string, data map[string]any) error {
	if err := EnsureParent(path); err != nil {
		return err
	}

	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')

	return writeAtomic(path, encoded)
}

// WriteTextAtomic writes text content to path atomically.
func WriteTextAtomic(path string, content string) error {
	if err := EnsureParent(path); err != nil {
		return err
	}
	return writeAtomic(path, []byte(content))
}

// WriteTOMLAtomic writes a TOML map to path atomically.
func WriteTOMLAtomic(path string, data map[string]any) error {
	encoded, err := toml.Marshal(data)
	if err != nil {
		return err
	}
	return WriteTextAtomic(path, string(encoded))
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	f, err := os.CreateTemp(dir, "."+base+".*")
	if err != nil {
		return err
	}
	tmpPath := f.Name()

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// ── Snapshot ─────────────────────────────────────────────────────────

// SnapshotKey generates a short hash key for snapshot filenames.
func SnapshotKey(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	h := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(h[:])[:12]
}

// SnapshotOne snapshots a file before modification. Returns (existed, snapshotPath, error).
func SnapshotOne(path, snapshotDir string) (bool, string, error) {
	key := SnapshotKey(path)
	snapPath := filepath.Join(snapshotDir, key+".snapshot")

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, "", nil
	}

	if err := EnsureParent(snapPath); err != nil {
		return false, "", err
	}
	if err := copyFile(path, snapPath); err != nil {
		return false, "", err
	}
	return true, snapPath, nil
}

// SnapshotWithSeq snapshots with a sequence number to avoid collision.
func SnapshotWithSeq(path, snapshotDir string, seq int) (bool, string, error) {
	key := SnapshotKey(path)
	snapPath := filepath.Join(snapshotDir, fmt.Sprintf("%s-%d.snapshot", key, seq))

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, "", nil
	}

	if err := EnsureParent(snapPath); err != nil {
		return false, "", err
	}
	if err := copyFile(path, snapPath); err != nil {
		return false, "", err
	}
	return true, snapPath, nil
}

func copyFile(src, dst string) error {
	// Reject symlinks on both source and destination
	if err := RejectSymlink(src); err != nil {
		return err
	}
	if err := RejectSymlink(dst); err != nil {
		return err
	}

	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	return os.Chmod(dst, srcInfo.Mode())
}

// CopyFile is the exported version of copyFile.
func CopyFile(src, dst string) error {
	return copyFile(src, dst)
}

// ── Lock ─────────────────────────────────────────────────────────────

// LockFile represents an acquired file lock.
type LockFile struct {
	f *os.File
}

// AcquireLock acquires an exclusive file lock with timeout.
func AcquireLock(lockPath string, timeoutSec int) (*LockFile, error) {
	if err := EnsureParent(lockPath); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			// Got the lock — write PID + timestamp
			f.Truncate(0)
			f.Seek(0, 0)
			lockInfo := map[string]any{
				"pid":         os.Getpid(),
				"acquired_at": UTCNowISO(),
			}
			enc, _ := json.Marshal(lockInfo)
			f.Write(enc)
			f.Sync()
			return &LockFile{f: f}, nil
		}

		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("failed to acquire lock: %s (timeout %ds)", lockPath, timeoutSec)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// ReleaseLock releases the file lock.
func ReleaseLock(lf *LockFile) {
	if lf == nil || lf.f == nil {
		return
	}
	syscall.Flock(int(lf.f.Fd()), syscall.LOCK_UN)
	lf.f.Close()
}

// ── State dirs ───────────────────────────────────────────────────────

// EnsureStateDirs creates the standard state directory structure.
func EnsureStateDirs(stateDir string) error {
	dirs := []string{
		filepath.Join(stateDir, "runs"),
		filepath.Join(stateDir, "snapshots"),
		filepath.Join(stateDir, "locks"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// ── Env loading ──────────────────────────────────────────────────────

// LoadEnvValues reads *.env files from secretsDir (sorted by name), parses
// KEY=VALUE lines (skipping comments and blanks, supporting "export " prefix),
// then merges os.Environ() on top. Shell environment takes priority.
func LoadEnvValues(secretsDir string) map[string]string {
	values := make(map[string]string)

	info, err := os.Stat(secretsDir)
	if err != nil || !info.IsDir() {
		// Still merge shell env even if secretsDir is missing.
		for _, kv := range os.Environ() {
			idx := strings.IndexByte(kv, '=')
			if idx < 0 {
				continue
			}
			values[kv[:idx]] = kv[idx+1:]
		}
		return values
	}

	entries, err := os.ReadDir(secretsDir)
	if err != nil {
		return values
	}

	var envFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".env") {
			envFiles = append(envFiles, filepath.Join(secretsDir, entry.Name()))
		}
	}
	sort.Strings(envFiles)

	for _, path := range envFiles {
		parsed := ParseEnvFile(path)
		for k, v := range parsed {
			values[k] = v
		}
	}

	// Shell environment has higher priority.
	for _, kv := range os.Environ() {
		idx := strings.IndexByte(kv, '=')
		if idx < 0 {
			continue
		}
		values[kv[:idx]] = kv[idx+1:]
	}

	return values
}

// ParseEnvFile is a .env parser: key=value lines, # comments, empty lines
// ignored. Supports "export KEY=VALUE" syntax and surrounding quotes.
func ParseEnvFile(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	result := make(map[string]string)
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Support "export KEY=VALUE" syntax.
		line = strings.TrimPrefix(line, "export ")
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])

		// Strip surrounding quotes if present.
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') ||
				(val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}

		if key != "" {
			result[key] = val
		}
	}
	return result
}

// ── JSON helpers ─────────────────────────────────────────────────────

// Normalize produces a canonical JSON string for comparison.
func Normalize(obj any) string {
	data, _ := json.Marshal(obj)
	// Re-parse and re-marshal with sorted keys
	var parsed any
	json.Unmarshal(data, &parsed)
	sorted, _ := json.Marshal(parsed)
	return string(sorted)
}

// GetStringSlice extracts a []string from a map value.
func GetStringSlice(m map[string]any, key string) []string {
	val, ok := m[key]
	if !ok {
		return nil
	}
	arr, ok := val.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

// GetMap extracts a map[string]any from a map value.
func GetMap(m map[string]any, key string) map[string]any {
	val, ok := m[key]
	if !ok {
		return nil
	}
	result, ok := val.(map[string]any)
	if !ok {
		return nil
	}
	return result
}

// GetString extracts a string from a map value with a default.
func GetString(m map[string]any, key, defaultVal string) string {
	val, ok := m[key]
	if !ok {
		return defaultVal
	}
	s, ok := val.(string)
	if !ok {
		return defaultVal
	}
	return s
}

// GetBool extracts a bool from a map value with a default.
func GetBool(m map[string]any, key string, defaultVal bool) bool {
	val, ok := m[key]
	if !ok {
		return defaultVal
	}
	b, ok := val.(bool)
	if !ok {
		return defaultVal
	}
	return b
}
