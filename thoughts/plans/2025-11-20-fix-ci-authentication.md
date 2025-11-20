# Fix CI Authentication for Git Repositories Implementation Plan

## Overview

The argocd-offline-cli currently fails in CI environments when processing Applications with SSH Git URLs (`git@github.com:...`) because it uses `git.NoopCredsStore{}` which doesn't support SSH authentication. This plan implements two solutions: local repository detection for same-repo scenarios and HTTPS token authentication for general CI support.

## Current State Analysis

The CLI uses `git.NoopCredsStore{}` (preview/shared.go:60) which bypasses SSH agent authentication. Credentials are only supported via username/password through environment variables (`HELM_REPO_USERNAME`/`HELM_REPO_PASSWORD`), which work for Helm charts but not for SSH Git URLs.

**Error in CI:**
```
failed to list refs: error creating SSH agent: "SSH agent requested but SSH_AUTH_SOCK not-specified"
```

### Key Discoveries:
- Repository service initialized with `git.NoopCredsStore{}` at preview/shared.go:60
- Credentials resolved via `FindRepoUsername`/`FindRepoPassword` at preview/helm.go:26-40
- SSH URLs require SSH agent which isn't available in CI containers
- User's specific case: Running from same repository that Application references

## Desired End State

After implementation, the CLI should:
1. Automatically detect when running from the same repository and use local filesystem
2. Support HTTPS token authentication for Git repositories in CI
3. Provide clear error messages with remediation steps when authentication fails
4. Document CI authentication setup in README

### Verification:
- CLI works with SSH URLs when running from same repository
- CLI works with HTTPS URLs + tokens in CI environments
- Clear documentation for CI setup

## What We're NOT Doing

- Adding full SSH key support (requires significant changes to repository service)
- Implementing OAuth flows or other complex authentication methods
- Changing the existing Helm authentication mechanism
- Supporting different credentials per Git repository (only global tokens for now)

## Implementation Approach

We'll implement two complementary solutions:
1. **Phase 1**: Local repository detection - immediate fix for same-repo scenarios
2. **Phase 2**: SSH-to-HTTPS URL conversion with token support - general CI solution
3. **Phase 3**: Documentation and error handling improvements

## Phase 1: Local Repository Detection

### Overview
Detect when the Application's repository URL matches the current Git repository and use the local filesystem directly instead of cloning.

### Changes Required:

#### 1. Add Local Repository Detection
**File**: `preview/shared.go`
**Changes**: Add function to detect if we're in the target repository

```go
import (
    "os/exec"
    "strings"
)

// isLocalRepository checks if the given repoURL matches the current git repository
func isLocalRepository(repoURL string) (bool, string, error) {
    // Get current repository's remote URL
    cmd := exec.Command("git", "config", "--get", "remote.origin.url")
    output, err := cmd.Output()
    if err != nil {
        return false, "", nil // Not in a git repo or no origin
    }

    currentRepo := strings.TrimSpace(string(output))

    // Normalize both URLs for comparison
    normalizedCurrent := normalizeGitURL(currentRepo)
    normalizedTarget := normalizeGitURL(repoURL)

    if normalizedCurrent == normalizedTarget {
        // Get repository root directory
        cmd = exec.Command("git", "rev-parse", "--show-toplevel")
        rootDir, err := cmd.Output()
        if err != nil {
            return false, "", err
        }
        return true, strings.TrimSpace(string(rootDir)), nil
    }

    return false, "", nil
}

// normalizeGitURL converts various Git URL formats to a comparable form
func normalizeGitURL(url string) string {
    // Convert SSH to HTTPS format for comparison
    if strings.HasPrefix(url, "git@") {
        // git@github.com:owner/repo.git -> github.com/owner/repo
        url = strings.Replace(url, ":", "/", 1)
        url = strings.TrimPrefix(url, "git@")
    }

    // Remove protocol
    url = strings.TrimPrefix(url, "https://")
    url = strings.TrimPrefix(url, "http://")

    // Remove .git suffix
    url = strings.TrimSuffix(url, ".git")

    return strings.ToLower(url)
}
```

#### 2. Modify Repository Service to Support Local Paths
**File**: `preview/shared.go`
**Changes**: Update manifest generation to use local path when detected

```go
func generateSingleSourceManifests(app argoappv1.Application, ...) {
    // Check if this is a local repository
    isLocal, localPath, _ := isLocalRepository(app.Spec.Source.RepoURL)

    var repoOverride *argoappv1.Repository
    if isLocal {
        log.Infof("Detected local repository for %s, using path: %s", app.Name, localPath)
        // Convert to file:// URL for local access
        repoOverride = &argoappv1.Repository{
            Repo: "file://" + localPath,
            Type: "git",
        }
    } else {
        // Use existing credential resolution
        repoOverride = &argoappv1.Repository{
            Repo:     app.Spec.Source.RepoURL,
            Username: FindRepoUsername(app.Spec.Source.RepoURL),
            Password: FindRepoPassword(app.Spec.Source.RepoURL),
        }
    }

    response, err := repoService.GenerateManifest(context.Background(), &repoapiclient.ManifestRequest{
        ApplicationSource: app.Spec.Source,
        AppName:           app.Name,
        Namespace:         app.Spec.Destination.Namespace,
        NoCache:           true,
        Repo:              repoOverride,
        ProjectName:       "applications",
    })
}
```

### Success Criteria:

#### Automated Verification:
- [ ] Unit tests pass: `go test ./preview/...`
- [ ] Integration test with local repo: `./argocd-offline-cli app preview-resources test-app.yaml`
- [ ] No compilation errors: `go build ./...`
- [ ] Linting passes: `golangci-lint run`

#### Manual Verification:
- [ ] CLI works with SSH URLs when running from same repository
- [ ] CLI still works with external repositories using HTTPS
- [ ] Performance improvement verified (no network cloning for local repos)

---

## Phase 2: SSH-to-HTTPS URL Conversion with Token Support

### Overview
Add support for converting SSH URLs to HTTPS and using GitHub/GitLab tokens for authentication in CI environments.

### Changes Required:

#### 1. Add SSH to HTTPS URL Converter
**File**: `preview/git_auth.go` (new file)
**Changes**: Create utilities for URL conversion and token authentication

```go
package preview

import (
    "fmt"
    "os"
    "strings"
)

// ConvertSSHToHTTPS converts a Git SSH URL to HTTPS format
func ConvertSSHToHTTPS(sshURL string) (string, error) {
    if !strings.HasPrefix(sshURL, "git@") {
        return sshURL, nil // Already HTTPS or other format
    }

    // git@github.com:owner/repo.git -> https://github.com/owner/repo.git
    parts := strings.SplitN(sshURL, ":", 2)
    if len(parts) != 2 {
        return "", fmt.Errorf("invalid SSH URL format: %s", sshURL)
    }

    host := strings.TrimPrefix(parts[0], "git@")
    path := parts[1]

    return fmt.Sprintf("https://%s/%s", host, path), nil
}

// FindGitToken returns the appropriate token for the repository host
func FindGitToken(repoURL string) string {
    // Check for specific provider tokens first
    if strings.Contains(repoURL, "github.com") {
        if token := os.Getenv("GITHUB_TOKEN"); token != "" {
            return token
        }
    }
    if strings.Contains(repoURL, "gitlab.com") {
        if token := os.Getenv("GITLAB_TOKEN"); token != "" {
            return token
        }
    }

    // Fall back to generic Git token
    return os.Getenv("GIT_TOKEN")
}

// PrepareGitCredentials prepares credentials for a Git repository
func PrepareGitCredentials(repoURL string) (string, string, string, error) {
    // Try to convert SSH to HTTPS if needed
    httpsURL, err := ConvertSSHToHTTPS(repoURL)
    if err != nil {
        return repoURL, "", "", err
    }

    // If conversion happened and we have a token, use it
    if httpsURL != repoURL {
        if token := FindGitToken(httpsURL); token != "" {
            log.Debugf("Using token authentication for %s", httpsURL)
            // For GitHub/GitLab, username can be anything with token auth
            return httpsURL, "git", token, nil
        }

        // No token available, return error with helpful message
        return repoURL, "", "", fmt.Errorf(
            "SSH URL detected but no token configured. " +
            "Set GITHUB_TOKEN, GITLAB_TOKEN, or GIT_TOKEN environment variable, " +
            "or convert repository URL to HTTPS format")
    }

    // For HTTPS URLs, use existing credential resolution
    return httpsURL, FindRepoUsername(httpsURL), FindRepoPassword(httpsURL), nil
}
```

#### 2. Update Manifest Generation to Use New Authentication
**File**: `preview/shared.go`
**Changes**: Integrate the new authentication logic

```go
func generateSingleSourceManifests(app argoappv1.Application, ...) {
    // First check for local repository
    isLocal, localPath, _ := isLocalRepository(app.Spec.Source.RepoURL)

    var repoOverride *argoappv1.Repository
    if isLocal {
        log.Infof("Using local repository at %s", localPath)
        repoOverride = &argoappv1.Repository{
            Repo: "file://" + localPath,
            Type: "git",
        }
    } else {
        // Use new credential preparation
        repoURL, username, password, err := PrepareGitCredentials(app.Spec.Source.RepoURL)
        if err != nil {
            log.Fatalf("Failed to prepare credentials for %s: %v", app.Name, err)
        }

        repoOverride = &argoappv1.Repository{
            Repo:     repoURL,
            Username: username,
            Password: password,
        }
    }

    response, err := repoService.GenerateManifest(context.Background(), &repoapiclient.ManifestRequest{
        ApplicationSource: app.Spec.Source,
        AppName:           app.Name,
        Namespace:         app.Spec.Destination.Namespace,
        NoCache:           true,
        Repo:              repoOverride,
        ProjectName:       "applications",
    })
}
```

### Success Criteria:

#### Automated Verification:
- [ ] Unit tests for URL conversion: `go test ./preview/git_auth_test.go`
- [ ] Integration test with HTTPS + token: `GIT_TOKEN=xxx ./argocd-offline-cli app preview-resources test-app.yaml`
- [ ] Existing tests still pass: `go test ./...`
- [ ] Build succeeds: `go build ./...`

#### Manual Verification:
- [ ] SSH URLs are converted to HTTPS when token is available
- [ ] GitHub token authentication works in CI
- [ ] Error messages are helpful when authentication fails

---

## Phase 3: Documentation and Error Handling

### Overview
Update documentation and improve error messages to help users configure CI authentication correctly.

### Changes Required:

#### 1. Update README
**File**: `README.md`
**Changes**: Add CI authentication section

```markdown
## CI/CD Authentication

The argocd-offline-cli supports multiple authentication methods for CI environments:

### Local Repository Detection
When running from within the same Git repository that your Application references, the CLI automatically uses the local filesystem, avoiding authentication entirely. This is the fastest and simplest option for CI pipelines.

### Token Authentication for Git Repositories
For external Git repositories, configure authentication using tokens:

#### GitHub Repositories
```bash
export GITHUB_TOKEN="your-github-token"
# Or use the generic token variable
export GIT_TOKEN="your-token"
```

#### GitLab Repositories
```bash
export GITLAB_TOKEN="your-gitlab-token"
# Or use the generic token variable
export GIT_TOKEN="your-token"
```

#### SSH URL Support
The CLI automatically converts SSH URLs (`git@github.com:owner/repo.git`) to HTTPS when a token is available. No URL changes needed in your Application manifests.

### Helm Repository Authentication
For Helm chart repositories, use:
```bash
export HELM_REPO_USERNAME="your-username"
export HELM_REPO_PASSWORD="your-password-or-token"
```

### Troubleshooting

**Error: "SSH agent requested but SSH_AUTH_SOCK not-specified"**
- Solution 1: Run the CLI from within the repository (local detection)
- Solution 2: Set `GITHUB_TOKEN` or `GIT_TOKEN` environment variable
- Solution 3: Convert your Application to use HTTPS URLs

**Error: "Failed to prepare credentials"**
- Check that your token environment variable is set
- Verify the token has repository read access
- For private repositories, ensure token has appropriate scopes
```

#### 2. Improve Error Messages
**File**: `preview/shared.go`
**Changes**: Add helpful error messages

```go
func handleAuthenticationError(err error, repoURL string) error {
    if strings.Contains(err.Error(), "SSH_AUTH_SOCK") {
        return fmt.Errorf(
            "SSH authentication failed for %s\n" +
            "Solutions:\n" +
            "  1. Run from within the repository to use local detection\n" +
            "  2. Set GITHUB_TOKEN or GIT_TOKEN environment variable\n" +
            "  3. Convert Application to use HTTPS URL instead of SSH\n" +
            "See README.md for detailed CI authentication setup",
            repoURL)
    }
    return err
}
```

### Success Criteria:

#### Automated Verification:
- [ ] Documentation builds without errors: `markdownlint README.md`
- [ ] All code compiles: `go build ./...`
- [ ] Tests pass: `go test ./...`

#### Manual Verification:
- [ ] README clearly explains CI authentication options
- [ ] Error messages provide actionable solutions
- [ ] Examples work when followed step-by-step

---

## Testing Strategy

### Unit Tests:
- Test SSH to HTTPS URL conversion with various formats
- Test token resolution from environment variables
- Test local repository detection logic
- Test git URL normalization

### Integration Tests:
- Test with local repository (file:// URL)
- Test with HTTPS URL + token authentication
- Test with SSH URL that gets converted
- Test multi-source applications with mixed authentication

### Manual Testing Steps:
1. Create test Application with SSH URL
2. Run CLI from same repository - verify local detection works
3. Run CLI from different location with GITHUB_TOKEN set - verify conversion works
4. Run CLI without token - verify helpful error message
5. Test with multi-source application mixing Git and Helm sources

## Performance Considerations

- Local repository detection eliminates network cloning (significant speedup)
- URL conversion is negligible overhead (string manipulation only)
- Token lookup is fast (environment variable access)

## Migration Notes

- Existing users with HTTPS URLs see no change
- SSH URL users get automatic benefits when tokens configured
- No breaking changes to existing authentication methods

## References

- Original research: `thoughts/research/2025-11-19-multi-source-application-support.md`
- Current implementation: `preview/shared.go:55-244`
- Credential resolution: `preview/helm.go:26-52`
- ArgoCD repository service: `github.com/argoproj/argo-cd/v3/reposerver/repository`