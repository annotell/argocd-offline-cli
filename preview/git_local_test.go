package preview

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	argoappv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
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

// TestResolveLocalRevision tests resolving HEAD to a commit SHA
func TestResolveLocalRevision(t *testing.T) {
	// Get current repo path
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		t.Skip("Not in a git repository")
	}
	repoPath := strings.TrimSpace(string(output))

	// Test resolution
	sha, err := resolveLocalRevision(repoPath)
	assert.NoError(t, err)
	assert.Len(t, sha, 40, "SHA should be 40 characters")
	assert.Regexp(t, regexp.MustCompile("^[a-f0-9]{40}$"), sha, "SHA should be 40-character hex string")
}

// TestResolveLocalRevision_InvalidPath tests error handling for invalid paths
func TestResolveLocalRevision_InvalidPath(t *testing.T) {
	_, err := resolveLocalRevision("/nonexistent/path")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "/nonexistent/path", "Error should contain the invalid path")
}

// TestResolveLocalRevision_MatchesGitCommand tests that our result matches git rev-parse HEAD
func TestResolveLocalRevision_MatchesGitCommand(t *testing.T) {
	// Get current repo path
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		t.Skip("Not in a git repository")
	}
	repoPath := strings.TrimSpace(string(output))

	// Get expected SHA using git command directly
	cmd = exec.Command("git", "-C", repoPath, "rev-parse", "HEAD")
	expectedOutput, err := cmd.Output()
	require.NoError(t, err)
	expectedSHA := strings.TrimSpace(string(expectedOutput))

	// Test our function
	sha, err := resolveLocalRevision(repoPath)
	require.NoError(t, err)
	assert.Equal(t, expectedSHA, sha, "Resolved SHA should match git rev-parse HEAD")
}

// TestBuildRefSourcesWithResolvedRevisions tests that buildRefSources correctly uses resolved revisions
// This verifies the fix for the issue where refSources was built before local revisions were resolved
func TestBuildRefSourcesWithResolvedRevisions(t *testing.T) {
	// Get current repo path and HEAD SHA
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		t.Skip("Not in a git repository")
	}
	repoPath := strings.TrimSpace(string(output))

	// Get the expected HEAD SHA
	expectedSHA, err := resolveLocalRevision(repoPath)
	require.NoError(t, err)

	// Get current repo URL
	cmd = exec.Command("git", "config", "--get", "remote.origin.url")
	output, err = cmd.Output()
	require.NoError(t, err)
	currentRepoURL := strings.TrimSpace(string(output))

	// Simulate the flow in generateMultiSourceManifests:
	// 1. Create sources with a Ref field using original branch name
	// 2. Resolve local revisions (simulating the first pass)
	// 3. Build refSources from resolved sources
	// 4. Verify refSources contains the resolved SHA, not original branch name

	originalBranchName := "master" // Original targetRevision before resolution

	// Create test sources simulating a multi-source app with cross-source references
	sources := []struct {
		RepoURL        string
		TargetRevision string
		Ref            string
		Chart          string
	}{
		{
			RepoURL:        currentRepoURL,
			TargetRevision: originalBranchName,
			Ref:            "values", // This source has a Ref field
			Chart:          "",       // Git source, not Helm
		},
		{
			RepoURL:        "https://charts.example.com",
			TargetRevision: "1.0.0",
			Ref:            "",
			Chart:          "my-chart", // Helm chart, should not be resolved
		},
	}

	// First pass: resolve local revisions (simulating what generateMultiSourceManifests does)
	resolvedSources := make([]struct {
		RepoURL        string
		TargetRevision string
		Ref            string
		Chart          string
	}, len(sources))

	for i, source := range sources {
		resolvedSources[i] = source

		isLocal, localPath, _ := isLocalRepository(source.RepoURL)
		if isLocal && source.Chart == "" {
			resolvedRevision, err := resolveLocalRevision(localPath)
			require.NoError(t, err)
			resolvedSources[i].TargetRevision = resolvedRevision
		}
	}

	// Verify the first source (with Ref) was resolved to HEAD SHA
	assert.Equal(t, expectedSHA, resolvedSources[0].TargetRevision,
		"Local Git source with Ref should have resolved targetRevision")

	// Verify the second source (Helm chart) was not resolved
	assert.Equal(t, "1.0.0", resolvedSources[1].TargetRevision,
		"Helm chart source should keep original targetRevision")

	// Now verify what would go into refSources
	// In the actual code, buildRefSources uses argoappv1.ApplicationSource
	// Here we just verify the logic is correct
	for i, source := range resolvedSources {
		if source.Ref != "" {
			assert.Equal(t, expectedSHA, source.TargetRevision,
				"Source %d with Ref '%s' should have resolved revision in refSources", i, source.Ref)
			assert.NotEqual(t, originalBranchName, source.TargetRevision,
				"Source %d with Ref '%s' should NOT have original branch name", i, source.Ref)
		}
	}
}

// TestMultiSourceRefSourcesIntegration tests the full integration of resolved revisions in refSources
// using the actual buildRefSources function from the codebase
func TestMultiSourceRefSourcesIntegration(t *testing.T) {
	// Import is already done at package level for argoappv1

	// Get current repo URL and HEAD SHA
	cmd := exec.Command("git", "config", "--get", "remote.origin.url")
	output, err := cmd.Output()
	if err != nil {
		t.Skip("Not in a git repository with origin")
	}
	currentRepoURL := strings.TrimSpace(string(output))

	cmd = exec.Command("git", "rev-parse", "--show-toplevel")
	output, err = cmd.Output()
	require.NoError(t, err)
	repoPath := strings.TrimSpace(string(output))

	expectedSHA, err := resolveLocalRevision(repoPath)
	require.NoError(t, err)

	// Test with the actual argoappv1.ApplicationSource type
	// Simulating a multi-source app where one source has a Ref field
	originalBranchName := "main"

	sources := []argoappv1.ApplicationSource{
		{
			RepoURL:        currentRepoURL,
			TargetRevision: originalBranchName, // Will be resolved to HEAD SHA
			Ref:            "values",
			Path:           "deploy/values",
		},
		{
			RepoURL:        "https://charts.example.com",
			TargetRevision: "2.0.0",
			Chart:          "example-chart",
		},
	}

	// Simulate first pass: resolve local revisions
	resolvedSources := make([]argoappv1.ApplicationSource, len(sources))
	for i, source := range sources {
		resolvedSources[i] = source

		isLocal, localPath, _ := isLocalRepository(source.RepoURL)
		if isLocal && source.Chart == "" {
			resolvedRevision, err := resolveLocalRevision(localPath)
			require.NoError(t, err)
			resolvedSources[i].TargetRevision = resolvedRevision
		}
	}

	// Build refSources using the actual function
	refSources := buildRefSources(resolvedSources)

	// Verify refSources contains the resolved SHA for the source with Ref
	require.Contains(t, refSources, "$values", "refSources should contain $values key")
	assert.Equal(t, expectedSHA, refSources["$values"].TargetRevision,
		"refSources[$values] should have resolved HEAD SHA, not original branch name")
	assert.NotEqual(t, originalBranchName, refSources["$values"].TargetRevision,
		"refSources[$values] should NOT contain original branch name '%s'", originalBranchName)

	// Verify the repository URL is correctly set
	assert.Equal(t, currentRepoURL, refSources["$values"].Repo.Repo,
		"refSources[$values] should have correct repository URL")
}
