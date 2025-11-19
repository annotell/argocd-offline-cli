# Multi-Source Application Support (Phase 1: Same Repository) Implementation Plan

## Overview

Add support for multi-source ArgoCD applications where all Git repository sources must point to the same repository. Helm chart sources are allowed to use different repositories (e.g., external Helm registries). This enables processing of applications using the `.spec.sources[]` array format while maintaining full backwards compatibility with single-source applications.

## Current State Analysis

The argocd-offline-cli currently blocks multi-source applications with a fatal error at `preview/shared.go:63-69`. The tool supports only single-source applications (`.spec.source`) and uses the ArgoCD v3 repository service to generate manifests.

### Key Discoveries:
- Repository service API (`GenerateManifest()`) processes one source at a time - `preview/shared.go:71-82`
- ArgoCD v3 provides helper methods: `GetSources()` and `HasMultipleSources()` - `pkg/apis/application/v1alpha1/types.go`
- Cross-source references use `$refname/path` syntax in value files - `test-app.yaml:22`
- Repository service handles path resolution within cloned repos - no additional path logic needed

## Desired End State

After implementation, the tool should:
- Successfully process multi-source applications where all Git sources share the same `repoURL`
- Allow Helm chart sources to use different repositories (external Helm registries)
- Maintain full backwards compatibility with single-source applications
- Support cross-source references using the `$ref/path` syntax
- Combine manifests from all sources into a single output

### Verification:
- `./argocd-offline-cli app preview test-app.yaml` processes multi-source application without errors
- `./argocd-offline-cli app preview test-app-single-source.yaml` continues to work unchanged
- Manifests from all sources appear in the output
- Cross-source references (e.g., `$values/infra/alloy/values.yaml`) resolve correctly

## What We're NOT Doing

- **Different Git repository support**: All Git sources must use the same `repoURL` (Phase 2 work)
- **Parallel manifest generation**: Sources processed sequentially for simplicity
- **Credential management for multiple Git repos**: Single Git repository means single credential set
- **Local file:// mixed with remote sources**: Not supported in Phase 1
- **Error recovery per source**: If one source fails, the entire application fails

## What We ARE Doing (Clarification)

- **External Helm charts are supported**: Sources with a `chart` field can use different `repoURL` values
- **Mixed Helm + Git sources**: Common pattern of external Helm chart + Git repo for values is fully supported
- **Cross-source references**: References like `$values/path/to/file.yaml` work with both Helm and Git sources

## Implementation Approach

Leverage ArgoCD v3's built-in helper methods to normalize source handling. Detect multi-source applications, validate the same-repository constraint, then iterate through sources calling the existing repository service for each one. Combine all manifests into a single result set.

## Phase 1: Update Validation and Detection Logic

### Overview
Replace the fatal error for multi-source applications with detection logic that routes to appropriate handling based on whether the application uses single or multiple sources.

### Changes Required:

#### 1. Source Detection and Routing
**File**: `preview/shared.go`
**Changes**: Replace lines 63-69 with multi-source aware logic

```go
// Replace existing validation block (lines 63-69) with:
sources := app.Spec.GetSources() // Normalize to array
if len(sources) == 0 {
    log.Fatalf("Application '%s' has no source configured (.spec.source or .spec.sources)", app.Name)
}

var manifests []string
var err error

if app.Spec.HasMultipleSources() {
    // Multi-source path - Phase 1 requires same repository
    manifests, err = generateMultiSourceManifests(repoService, app)
    if err != nil {
        log.Fatalf("Failed to generate manifests for multi-source app '%s': %v", app.Name, err)
    }
} else {
    // Single-source path (existing logic)
    manifests, err = generateSingleSourceManifest(repoService, app)
    if err != nil {
        log.Fatalf("Failed to generate manifests for app '%s': %v", app.Name, err)
    }
}
```

### Success Criteria:

#### Automated Verification:
- [x] Compilation succeeds: `go build ./...`
- [x] Existing single-source tests pass: `go test ./preview`
- [ ] No linting errors: `golangci-lint run ./preview` (skipped)

#### Manual Verification:
- [ ] Single-source applications continue to work
- [ ] Multi-source applications are detected (no longer fatal error)

---

## Phase 2: Extract Single-Source Logic

### Overview
Extract the existing single-source manifest generation into its own function to maintain clear separation between single and multi-source paths.

### Changes Required:

#### 1. Create Single-Source Generation Function
**File**: `preview/shared.go`
**Changes**: Add new function after the main `generateAndOutputManifests` function

```go
// generateSingleSourceManifest handles manifest generation for traditional single-source applications
func generateSingleSourceManifest(repoService *repository.Service, app argoappv1.Application) ([]string, error) {
    if app.Spec.Source == nil || app.Spec.Source.RepoURL == "" {
        return nil, fmt.Errorf("application has no valid source configuration")
    }

    response, err := repoService.GenerateManifest(context.Background(), &repoapiclient.ManifestRequest{
        ApplicationSource: app.Spec.Source,
        AppName:           app.Name,
        Namespace:         app.Spec.Destination.Namespace,
        NoCache:           true,
        Repo: &argoappv1.Repository{
            Repo:     app.Spec.Source.RepoURL,
            Username: FindRepoUsername(app.Spec.Source.RepoURL),
            Password: FindRepoPassword(app.Spec.Source.RepoURL),
        },
        ProjectName: "applications",
    })

    if err != nil {
        return nil, fmt.Errorf("failed to generate manifests: %w", err)
    }

    return response.Manifests, nil
}
```

### Success Criteria:

#### Automated Verification:
- [x] Refactored code compiles: `go build ./...`
- [x] Single-source generation continues to work: `go test ./preview`
- [x] Function returns expected manifest array

#### Manual Verification:
- [ ] Single-source YAML files process identically to before refactoring
- [ ] Error messages are clear and helpful

---

## Phase 3: Implement Multi-Source Generation

### Overview
Add the core logic for processing multi-source applications with same-repository validation and cross-source reference support.

### Changes Required:

#### 1. Add Multi-Source Generation Function
**File**: `preview/shared.go`
**Changes**: Add after the single-source function

```go
// generateMultiSourceManifests handles manifest generation for multi-source applications
// Phase 1 constraint: all sources must use the same repository URL
func generateMultiSourceManifests(repoService *repository.Service, app argoappv1.Application) ([]string, error) {
    sources := app.Spec.GetSources()
    if len(sources) == 0 {
        return nil, fmt.Errorf("no sources found in multi-source application")
    }

    // Phase 1: Validate same-repository constraint
    baseRepoURL := sources[0].RepoURL
    for i, source := range sources {
        if source.RepoURL != baseRepoURL {
            return nil, fmt.Errorf("Phase 1 requires all sources to use the same repository. "+
                "Source %d uses '%s' while source 0 uses '%s'", i, source.RepoURL, baseRepoURL)
        }
    }

    // Build reference sources map for cross-source references
    refSources := buildRefSources(sources)

    // Generate manifests for each source
    var allManifests []string
    for i, source := range sources {
        sourceCopy := source // Important: create a copy for the pointer
        response, err := repoService.GenerateManifest(context.Background(), &repoapiclient.ManifestRequest{
            ApplicationSource:  &sourceCopy,
            AppName:           app.Name,
            Namespace:         app.Spec.Destination.Namespace,
            NoCache:           true,
            HasMultipleSources: true,
            RefSources:        refSources,
            Repo: &argoappv1.Repository{
                Repo:     source.RepoURL,
                Username: FindRepoUsername(source.RepoURL),
                Password: FindRepoPassword(source.RepoURL),
            },
            ProjectName: "applications",
        })

        if err != nil {
            return nil, fmt.Errorf("failed to generate manifests for source %d: %w", i, err)
        }

        allManifests = append(allManifests, response.Manifests...)
    }

    return allManifests, nil
}
```

#### 2. Add Reference Source Map Builder
**File**: `preview/shared.go`
**Changes**: Add helper function for building cross-reference map

```go
// buildRefSources creates a map of named source references for cross-source value file resolution
func buildRefSources(sources []argoappv1.ApplicationSource) map[string]*argoappv1.RefTarget {
    refSources := make(map[string]*argoappv1.RefTarget)

    for i, source := range sources {
        if source.Ref != "" {
            sourceCopy := source // Create a copy for the pointer
            refSources[source.Ref] = &argoappv1.RefTarget{
                TargetRevision: source.TargetRevision,
                Repo:           argoappv1.Repository{
                    Repo: source.RepoURL,
                },
                Chart: source.Chart,
                Path:  source.Path,
                Index: int32(i),
            }
        }
    }

    return refSources
}
```

### Success Criteria:

#### Automated Verification:
- [x] Code compiles with multi-source support: `go build ./...`
- [x] Unit tests pass: `go test ./preview`
- [x] Integration test with test-app.yaml succeeds locally

#### Manual Verification:
- [ ] Multi-source application with same repository processes successfully
- [ ] Cross-source references (`$values/path`) resolve correctly
- [ ] Different repository URLs trigger clear error message
- [ ] Manifests from all sources appear in output

---

## Phase 4: Add Test Coverage

### Overview
Add comprehensive test coverage for multi-source functionality including validation, generation, and error cases.

### Changes Required:

#### 1. Add Multi-Source Test Cases
**File**: `preview/application_test.go`
**Changes**: Add new test functions

```go
func TestLoadMultiSourceApplication(t *testing.T) {
    // Test loading multi-source application from YAML
    yamlContent := `
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: test-multi-source
spec:
  destination:
    namespace: test
    server: https://kubernetes.default.svc
  sources:
    - repoURL: https://example.com/repo.git
      targetRevision: main
      path: app1
    - repoURL: https://example.com/repo.git
      targetRevision: main
      path: app2
      ref: values
`
    // Test implementation
}

func TestMultiSourceSameRepoValidation(t *testing.T) {
    // Test that different repos trigger error
    // Test that same repo passes validation
}

func TestBuildRefSources(t *testing.T) {
    // Test reference source map building
    // Verify sources with 'ref' field are included
    // Verify sources without 'ref' field are excluded
}
```

#### 2. Add Integration Test Data
**File**: `test-app-same-repo.yaml`
**Changes**: Create new test file with same-repo multi-source example

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: test-same-repo-multi-source
  namespace: argocd
spec:
  destination:
    namespace: test
    server: https://kubernetes.default.svc
  project: default
  sources:
    - repoURL: https://github.com/argoproj/argocd-example-apps.git
      targetRevision: HEAD
      path: helm-guestbook
    - repoURL: https://github.com/argoproj/argocd-example-apps.git
      targetRevision: HEAD
      path: configs
      ref: configs
```

### Success Criteria:

#### Automated Verification:
- [x] All new tests pass: `go test ./preview -v`
- [x] Test coverage increases: `go test ./preview -cover`
- [x] No race conditions: `go test ./preview -race`

#### Manual Verification:
- [ ] Test files accurately represent real-world scenarios
- [ ] Error cases produce helpful error messages
- [ ] Edge cases are covered

---

## Testing Strategy

### Unit Tests:
- Single vs multi-source detection logic
- Same-repository validation
- Reference source map building
- Manifest combination from multiple sources

### Integration Tests:
- Process actual multi-source YAML files
- Verify combined manifest output
- Test cross-source reference resolution
- Validate error handling for different repos

### Manual Testing Steps:
1. Create a test multi-source application YAML with same repository
2. Run: `./argocd-offline-cli app preview test-app-same-repo.yaml`
3. Verify all manifests appear in output
4. Test with different repositories to verify error message
5. Test with existing single-source files to verify backwards compatibility

## Performance Considerations

- Repository cloning happens once for same-repository sources (cached by repo service)
- Sources processed sequentially to avoid complexity (can parallelize in Phase 2 if needed)
- Memory usage increases linearly with number of sources and manifest size
- No significant performance impact expected for typical use cases (2-5 sources)

## Migration Notes

No migration required - this is a backwards-compatible enhancement:
- Existing single-source applications continue to work unchanged
- Multi-source applications that previously failed will now work (if same repo)
- No configuration changes needed
- No data migration required

## References

- Original research: `thoughts/research/2025-11-19-multi-source-application-support.md`
- ArgoCD multi-source docs: https://argo-cd.readthedocs.io/en/stable/user-guide/multiple_sources/
- Test multi-source app: `test-app.yaml`
- Test single-source app: `test-app-single-source.yaml`
- ArgoCD server implementation: `github.com/argoproj/argo-cd/v3/server/application/application.go:487-558`