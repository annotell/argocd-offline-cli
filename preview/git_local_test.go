package preview

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNormalizeGitURL tests the Git URL normalization for comparison
func TestNormalizeGitURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "SSH URL converts to normalized form",
			input:    "git@github.com:owner/repo.git",
			expected: "github.com/owner/repo",
		},
		{
			name:     "HTTPS URL strips protocol and .git suffix",
			input:    "https://github.com/owner/repo.git",
			expected: "github.com/owner/repo",
		},
		{
			name:     "HTTP URL strips protocol and .git suffix",
			input:    "http://github.com/owner/repo.git",
			expected: "github.com/owner/repo",
		},
		{
			name:     "URL without .git suffix works",
			input:    "https://github.com/owner/repo",
			expected: "github.com/owner/repo",
		},
		{
			name:     "SSH URL without .git suffix works",
			input:    "git@github.com:owner/repo",
			expected: "github.com/owner/repo",
		},
		{
			name:     "mixed case is lowercased",
			input:    "git@GitHub.com:Owner/Repo.git",
			expected: "github.com/owner/repo",
		},
		{
			name:     "GitLab SSH URL",
			input:    "git@gitlab.com:org/project.git",
			expected: "gitlab.com/org/project",
		},
		{
			name:     "GitLab HTTPS URL",
			input:    "https://gitlab.com/org/project.git",
			expected: "gitlab.com/org/project",
		},
		{
			name:     "Nested paths work",
			input:    "git@github.com:org/team/repo.git",
			expected: "github.com/org/team/repo",
		},
		{
			name:     "Self-hosted Git server",
			input:    "git@git.mycompany.com:team/project.git",
			expected: "git.mycompany.com/team/project",
		},
		{
			name:     "Self-hosted HTTPS",
			input:    "https://git.mycompany.com/team/project.git",
			expected: "git.mycompany.com/team/project",
		},
		{
			name:     "HTTPS URL with port number",
			input:    "https://git.mycompany.com:8443/team/project.git",
			expected: "git.mycompany.com:8443/team/project",
		},
		{
			name:     "Empty string returns empty",
			input:    "",
			expected: "",
		},
		{
			name:     "URL with trailing slash",
			input:    "https://github.com/owner/repo/",
			expected: "github.com/owner/repo/",
		},
		{
			name:     "SSH URL with trailing slash",
			input:    "git@github.com:owner/repo/",
			expected: "github.com/owner/repo/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeGitURL(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

// TestNormalizeGitURLComparison tests that different URL formats for the same repo match
func TestNormalizeGitURLComparison(t *testing.T) {
	tests := []struct {
		name   string
		url1   string
		url2   string
		expect bool
	}{
		{
			name:   "SSH and HTTPS same repo match",
			url1:   "git@github.com:owner/repo.git",
			url2:   "https://github.com/owner/repo.git",
			expect: true,
		},
		{
			name:   "With and without .git suffix match",
			url1:   "https://github.com/owner/repo",
			url2:   "https://github.com/owner/repo.git",
			expect: true,
		},
		{
			name:   "Different repos do not match",
			url1:   "git@github.com:owner/repo1.git",
			url2:   "git@github.com:owner/repo2.git",
			expect: false,
		},
		{
			name:   "Different orgs do not match",
			url1:   "git@github.com:org1/repo.git",
			url2:   "git@github.com:org2/repo.git",
			expect: false,
		},
		{
			name:   "Different hosts do not match",
			url1:   "git@github.com:owner/repo.git",
			url2:   "git@gitlab.com:owner/repo.git",
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			norm1 := normalizeGitURL(tt.url1)
			norm2 := normalizeGitURL(tt.url2)
			if tt.expect {
				require.Equal(t, norm1, norm2, "URLs should normalize to the same value")
			} else {
				require.NotEqual(t, norm1, norm2, "URLs should normalize to different values")
			}
		})
	}
}

// TestIsLocalRepository tests detection of whether we're running from the target repository
func TestIsLocalRepository(t *testing.T) {
	// Get the current repository's actual remote URL for testing
	cmd := exec.Command("git", "config", "--get", "remote.origin.url")
	output, err := cmd.Output()
	require.NoError(t, err, "Test must run from within a git repository")
	currentRepoURL := strings.TrimSpace(string(output))

	// Get the expected root directory
	cmd = exec.Command("git", "rev-parse", "--show-toplevel")
	rootOutput, err := cmd.Output()
	require.NoError(t, err)
	expectedRoot := strings.TrimSpace(string(rootOutput))

	tests := []struct {
		name          string
		repoURL       string
		expectMatch   bool
		expectPath    bool
		expectNoError bool
	}{
		{
			name:          "SSH URL matches current repository",
			repoURL:       currentRepoURL,
			expectMatch:   true,
			expectPath:    true,
			expectNoError: true,
		},
		{
			name:          "HTTPS equivalent matches current repository",
			repoURL:       convertSSHToHTTPSForTest(currentRepoURL),
			expectMatch:   true,
			expectPath:    true,
			expectNoError: true,
		},
		{
			name:          "URL without .git suffix matches",
			repoURL:       strings.TrimSuffix(currentRepoURL, ".git"),
			expectMatch:   true,
			expectPath:    true,
			expectNoError: true,
		},
		{
			name:          "Different repository does not match",
			repoURL:       "git@github.com:argoproj/argo-cd.git",
			expectMatch:   false,
			expectPath:    false,
			expectNoError: true,
		},
		{
			name:          "Different org same repo name does not match",
			repoURL:       "git@github.com:different-org/argocd-offline-cli.git",
			expectMatch:   false,
			expectPath:    false,
			expectNoError: true,
		},
		{
			name:          "Different host does not match",
			repoURL:       "git@gitlab.com:nissessenap/argocd-offline-cli.git",
			expectMatch:   false,
			expectPath:    false,
			expectNoError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isLocal, path, err := isLocalRepository(tt.repoURL)

			if tt.expectNoError {
				require.NoError(t, err)
			}

			if tt.expectMatch {
				require.True(t, isLocal, "Expected repository to be detected as local")
			} else {
				require.False(t, isLocal, "Expected repository to NOT be detected as local")
			}

			if tt.expectPath {
				require.NotEmpty(t, path, "Expected local path to be returned")
				require.Equal(t, expectedRoot, path, "Path should match repository root")
			} else {
				require.Empty(t, path, "Expected empty path for non-matching repository")
			}
		})
	}
}

// TestIsLocalRepositoryOutsideGitRepo tests behavior when not in a git repository
func TestIsLocalRepositoryOutsideGitRepo(t *testing.T) {
	// Create a temporary directory that is not a git repo
	tmpDir, err := os.MkdirTemp("", "not-a-git-repo")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Save current directory
	originalDir, err := os.Getwd()
	require.NoError(t, err)

	// Change to the non-git directory
	err = os.Chdir(tmpDir)
	require.NoError(t, err)
	defer os.Chdir(originalDir)

	// Test that isLocalRepository returns false when not in a git repo
	isLocal, path, err := isLocalRepository("git@github.com:any/repo.git")
	require.NoError(t, err, "Should not error when not in a git repo")
	require.False(t, isLocal, "Should return false when not in a git repo")
	require.Empty(t, path, "Should return empty path when not in a git repo")
}

// TestIsLocalRepositoryWithSubdirectory tests detection works from subdirectories
func TestIsLocalRepositoryWithSubdirectory(t *testing.T) {
	// Get current repo URL
	cmd := exec.Command("git", "config", "--get", "remote.origin.url")
	output, err := cmd.Output()
	require.NoError(t, err)
	currentRepoURL := strings.TrimSpace(string(output))

	// Get expected root
	cmd = exec.Command("git", "rev-parse", "--show-toplevel")
	rootOutput, err := cmd.Output()
	require.NoError(t, err)
	expectedRoot := strings.TrimSpace(string(rootOutput))

	// Change to a subdirectory (preview directory)
	subDir := filepath.Join(expectedRoot, "preview")
	originalDir, err := os.Getwd()
	require.NoError(t, err)

	err = os.Chdir(subDir)
	require.NoError(t, err)
	defer os.Chdir(originalDir)

	// Test from subdirectory
	isLocal, path, err := isLocalRepository(currentRepoURL)
	require.NoError(t, err)
	require.True(t, isLocal, "Should detect local repository from subdirectory")
	require.Equal(t, expectedRoot, path, "Should return repository root, not subdirectory")
}

// convertSSHToHTTPSForTest converts SSH URL to HTTPS for test purposes
func convertSSHToHTTPSForTest(sshURL string) string {
	if !strings.HasPrefix(sshURL, "git@") {
		return sshURL
	}
	// git@github.com:owner/repo.git -> https://github.com/owner/repo.git
	parts := strings.SplitN(sshURL, ":", 2)
	if len(parts) != 2 {
		return sshURL
	}
	host := strings.TrimPrefix(parts[0], "git@")
	return "https://" + host + "/" + parts[1]
}
