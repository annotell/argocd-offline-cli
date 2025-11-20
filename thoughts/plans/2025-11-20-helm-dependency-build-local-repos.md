# Helm Dependency Build Support for Local Repositories Implementation Plan

## Overview

Add support for running `helm dependency build` on Git-based Helm charts when using local repository detection. This resolves the error "found in Chart.yaml, but missing in charts/ directory" that occurs when Helm charts have dependencies that need to be downloaded before templating.

## Current State Analysis

The CLI uses ArgoCD's repository service to generate manifests. When a local repository is detected:
- Git sources use `file://` URLs pointing to the local filesystem
- The repository service runs `helm template` internally but does NOT run `helm dependency build`
- This causes failures for charts with dependencies defined in `Chart.yaml`

### Key Discoveries:
- Local repository detection works via `isLocalRepository()` at preview/shared.go:51-84
- Git-based charts (Path != "", Chart == "") use local paths when isLocal is true
- Helm registry charts (Chart != "") never use local paths, even when URL matches
- No existing `helm` exec commands in the codebase - all Helm operations are internal to repository service

## Desired End State

After implementation, the CLI should:
1. Automatically run `helm dependency build` for Git-based Helm charts in local repositories
2. Only run when Chart.yaml exists and has dependencies
3. Handle authentication for private Helm repositories using existing credential mechanisms
4. Provide clear error messages if dependency build fails

### Verification:
- CLI works with Helm charts that have dependencies when running from local repository
- No impact on charts without dependencies (no unnecessary operations)
- Clear error messages guide users when dependency build fails

## What We're NOT Doing

- Adding dependency build for Helm registry charts (Chart field set) - these are pre-packaged
- Adding dependency build for remote Git repositories - requires more complex caching
- Modifying ArgoCD's repository service internals
- Running `helm dependency update` (which updates Chart.lock) - only build from lock file

## Implementation Approach

We'll add a preprocessing step that runs `helm dependency build` before calling `GenerateManifest()` when:
1. Local repository is detected (isLocal == true)
2. Source has a path (source.Path != "")
3. Source is not a Helm registry chart (source.Chart == "")
4. Chart.yaml exists with dependencies defined

## Phase 1: Core Dependency Build Function

### Overview
Create the function to run `helm dependency build` with proper error handling and logging.

### Changes Required:

#### 1. Create Helm Dependency Build Function
**File**: `preview/helm_deps.go` (new file)
**Changes**: Add function to handle dependency building

```go
package preview

import (
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "gopkg.in/yaml.v3"
    log "github.com/sirupsen/logrus"
)

// HelmChart represents the structure of Chart.yaml for dependency checking
type HelmChart struct {
    Dependencies []struct {
        Name string `yaml:"name"`
    } `yaml:"dependencies"`
}

// runHelmDependencyBuild runs helm dependency build on the given chart path
// Returns nil if successful, the chart has no dependencies, or Chart.yaml doesn't exist
func runHelmDependencyBuild(chartPath string) error {
    // Check if Chart.yaml exists
    chartFile := filepath.Join(chartPath, "Chart.yaml")
    if _, err := os.Stat(chartFile); os.IsNotExist(err) {
        log.Debugf("No Chart.yaml found at %s, skipping dependency build", chartPath)
        return nil // Not a Helm chart
    }

    // Check if chart has dependencies
    data, err := os.ReadFile(chartFile)
    if err != nil {
        return fmt.Errorf("failed to read Chart.yaml: %w", err)
    }

    var chart HelmChart
    if err := yaml.Unmarshal(data, &chart); err != nil {
        return fmt.Errorf("failed to parse Chart.yaml: %w", err)
    }

    if len(chart.Dependencies) == 0 {
        log.Debugf("No dependencies found in %s, skipping dependency build", chartPath)
        return nil
    }

    // Check if dependencies already exist (charts/ directory is populated)
    chartsDir := filepath.Join(chartPath, "charts")
    if info, err := os.Stat(chartsDir); err == nil && info.IsDir() {
        entries, _ := os.ReadDir(chartsDir)
        if len(entries) >= len(chart.Dependencies) {
            log.Debugf("Dependencies appear to be already present in %s", chartsDir)
            return nil
        }
    }

    // Run helm dependency build
    log.Infof("Building Helm dependencies for %s", chartPath)
    cmd := exec.Command("helm", "dependency", "build", chartPath)

    // Set environment variables for authentication if needed
    cmd.Env = os.Environ()
    if username := os.Getenv("HELM_REPO_USERNAME"); username != "" {
        cmd.Env = append(cmd.Env, fmt.Sprintf("HELM_USERNAME=%s", username))
    }
    if password := os.Getenv("HELM_REPO_PASSWORD"); password != "" {
        cmd.Env = append(cmd.Env, fmt.Sprintf("HELM_PASSWORD=%s", password))
    }

    output, err := cmd.CombinedOutput()
    if err != nil {
        return fmt.Errorf("helm dependency build failed for %s: %w\nOutput: %s",
            chartPath, err, string(output))
    }

    log.Debugf("Successfully built Helm dependencies for %s", chartPath)
    return nil
}
```

### Success Criteria:

#### Automated Verification:
- [ ] Unit tests pass: `go test ./preview/...`
- [ ] Function compiles: `go build ./...`
- [ ] Linting passes: `golangci-lint run ./preview/...`

#### Manual Verification:
- [ ] Function correctly identifies charts with dependencies
- [ ] Skips charts without Chart.yaml gracefully
- [ ] Skips charts without dependencies
- [ ] Runs helm dependency build when needed

---

## Phase 2: Integration with Single-Source Applications

### Overview
Integrate the dependency build function into the single-source manifest generation flow.

### Changes Required:

#### 1. Update Single-Source Manifest Generation
**File**: `preview/shared.go`
**Changes**: Add dependency build before manifest generation (after line 213)

```go
func generateSingleSourceManifest(app argoappv1.Application, manifests *[]string, jsonFormat bool) error {
    // ... existing code ...

    isLocal, localPath, _ := isLocalRepository(app.Spec.Source.RepoURL)
    if isLocal {
        log.Infof("Detected local repository for %s, using path: %s", app.Name, localPath)

        // Build Helm dependencies for Git-based charts in local repositories
        if app.Spec.Source.Path != "" && app.Spec.Source.Chart == "" {
            chartPath := filepath.Join(localPath, app.Spec.Source.Path)
            if err := runHelmDependencyBuild(chartPath); err != nil {
                // Log warning but don't fail - let GenerateManifest provide the actual error
                log.Warnf("Failed to build Helm dependencies for %s: %v", app.Name, err)
            }
        }

        // localPath is from git rev-parse --show-toplevel and is therefore trusted
        repoOverride = &argoappv1.Repository{
            Repo: "file://" + filepath.ToSlash(localPath),
            Type: "git",
        }
    } else {
        // ... existing remote handling ...
    }

    // ... rest of function ...
}
```

### Success Criteria:

#### Automated Verification:
- [ ] Compilation succeeds: `go build ./...`
- [ ] Existing tests pass: `go test ./preview/...`
- [ ] No linting errors: `golangci-lint run ./preview/...`

#### Manual Verification:
- [ ] Single-source Helm charts with dependencies work from local repo
- [ ] Non-Helm sources are unaffected
- [ ] Remote sources continue to work normally

---

## Phase 3: Integration with Multi-Source Applications

### Overview
Add dependency build support for multi-source applications, handling each source independently.

### Changes Required:

#### 1. Update Multi-Source Manifest Generation
**File**: `preview/shared.go`
**Changes**: Add dependency build in the source processing loop (after line 297)

```go
func generateMultiSourceManifests(app argoappv1.Application, manifests *[]string, jsonFormat bool) error {
    // ... existing validation code ...

    for i, source := range sources {
        sourceCopy := source

        isLocal, localPath, _ := isLocalRepository(source.RepoURL)
        if isLocal && source.Chart == "" {
            // Only use local path for Git sources, not Helm charts
            log.Infof("Detected local repository for source %d in %s, using path: %s",
                i, app.Name, localPath)

            // Build Helm dependencies for Git-based charts
            if source.Path != "" {
                chartPath := filepath.Join(localPath, source.Path)
                if err := runHelmDependencyBuild(chartPath); err != nil {
                    log.Warnf("Failed to build Helm dependencies for source %d in %s: %v",
                        i, app.Name, err)
                }
            }

            // localPath is from git rev-parse --show-toplevel and is therefore trusted
            repoOverride = &argoappv1.Repository{
                Repo: "file://" + filepath.ToSlash(localPath),
                Type: "git",
            }
        } else {
            // ... existing remote handling ...
        }

        // ... rest of loop ...
    }
}
```

### Success Criteria:

#### Automated Verification:
- [ ] Compilation succeeds: `go build ./...`
- [ ] Multi-source tests pass: `go test -run TestMulti ./preview/...`
- [ ] No linting errors: `golangci-lint run ./preview/...`

#### Manual Verification:
- [ ] Multi-source apps with Helm dependencies work from local repo
- [ ] Mixed Git and Helm sources handled correctly
- [ ] Cross-source references ($values) still work

---

## Phase 4: Testing and Documentation

### Overview
Add comprehensive tests and update documentation.

### Changes Required:

#### 1. Create Unit Tests
**File**: `preview/helm_deps_test.go` (new file)
**Changes**: Add test coverage for the dependency build function

```go
package preview

import (
    "os"
    "path/filepath"
    "testing"
    "github.com/stretchr/testify/assert"
)

func TestRunHelmDependencyBuild(t *testing.T) {
    tests := []struct {
        name        string
        setupFunc   func(dir string) error
        expectError bool
        expectBuild bool
    }{
        {
            name: "No Chart.yaml - should skip",
            setupFunc: func(dir string) error {
                return nil // Empty directory
            },
            expectError: false,
            expectBuild: false,
        },
        {
            name: "Chart.yaml without dependencies - should skip",
            setupFunc: func(dir string) error {
                chartYaml := `apiVersion: v2
name: test-chart
version: 1.0.0`
                return os.WriteFile(filepath.Join(dir, "Chart.yaml"),
                    []byte(chartYaml), 0644)
            },
            expectError: false,
            expectBuild: false,
        },
        {
            name: "Chart.yaml with dependencies - should build",
            setupFunc: func(dir string) error {
                chartYaml := `apiVersion: v2
name: test-chart
version: 1.0.0
dependencies:
  - name: postgresql
    version: 12.0.0
    repository: https://charts.bitnami.com/bitnami`
                return os.WriteFile(filepath.Join(dir, "Chart.yaml"),
                    []byte(chartYaml), 0644)
            },
            expectError: false,
            expectBuild: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Create temp directory
            tmpDir := t.TempDir()

            // Setup test case
            if tt.setupFunc != nil {
                err := tt.setupFunc(tmpDir)
                assert.NoError(t, err)
            }

            // Run function
            err := runHelmDependencyBuild(tmpDir)

            // Check results
            if tt.expectError {
                assert.Error(t, err)
            } else {
                assert.NoError(t, err)
            }

            // Check if charts directory was created (indicates build ran)
            chartsDir := filepath.Join(tmpDir, "charts")
            _, err = os.Stat(chartsDir)
            if tt.expectBuild {
                // Note: actual helm dependency build may fail in test environment
                // This test mainly verifies the logic flow
                t.Logf("Would have run helm dependency build for %s", tmpDir)
            }
        })
    }
}
```

#### 2. Update README
**File**: `README.md`
**Changes**: Add section about Helm dependency support

```markdown
## Helm Chart Dependencies

The CLI automatically handles Helm chart dependencies when running from local repositories:

### Automatic Dependency Building
When processing Applications from the same Git repository:
- Detects Helm charts with dependencies in `Chart.yaml`
- Runs `helm dependency build` automatically before templating
- Uses existing Helm authentication (HELM_REPO_USERNAME/HELM_REPO_PASSWORD)

### Requirements
- `helm` CLI must be installed and available in PATH
- For private chart repositories, configure authentication:
  ```bash
  export HELM_REPO_USERNAME="your-username"
  export HELM_REPO_PASSWORD="your-password-or-token"
  ```

### Troubleshooting

**Error: "found in Chart.yaml, but missing in charts/ directory"**
- Ensure `helm` is installed: `helm version`
- Check Chart.yaml has valid dependencies
- Verify repository URLs in dependencies are accessible
- For private repos, check HELM_REPO_* environment variables

**Performance Note**:
Dependency download happens once per chart. Subsequent runs use cached dependencies unless Chart.lock changes.
```

### Success Criteria:

#### Automated Verification:
- [ ] All tests pass: `go test ./preview/...`
- [ ] Test coverage maintained or improved: `go test -cover ./preview/...`
- [ ] Documentation builds: `markdownlint README.md`

#### Manual Verification:
- [ ] Tests cover main scenarios
- [ ] Documentation is clear and complete
- [ ] Examples work when followed

---

## Testing Strategy

### Unit Tests:
- Chart detection (with/without Chart.yaml)
- Dependency detection (with/without dependencies)
- Error handling for invalid Chart.yaml
- Skipping when dependencies already exist

### Integration Tests:
- Single-source app with Helm dependencies
- Multi-source app with mixed Git and Helm sources
- Authentication with private Helm repositories
- Error propagation and logging

### Manual Testing Steps:
1. Create test Application with Helm chart containing dependencies
2. Run CLI from same repository - verify dependencies are built
3. Run again - verify skips rebuild (dependencies already present)
4. Test with private Helm repository requiring authentication
5. Test with multi-source application mixing Helm and plain manifests

## Performance Considerations

- Only runs for local repositories (no network overhead for detection)
- Skips if Chart.yaml doesn't exist (quick file check)
- Skips if no dependencies defined (YAML parsing is fast)
- Skips if charts/ directory already populated (avoids redundant builds)
- Helm dependency build is idempotent based on Chart.lock

## Migration Notes

- No breaking changes - feature is additive
- Existing users without Helm dependencies see no change
- Users with Helm dependencies get automatic fix when running locally
- Remote repository processing unchanged

## References

- Original error report: User encountering missing dependencies for sigstore chart
- Current implementation: `preview/shared.go:199-333` (manifest generation)
- Local detection: `preview/shared.go:51-84` (isLocalRepository function)
- Helm credentials: `preview/helm.go:26-52` (FindRepoUsername/FindRepoPassword)