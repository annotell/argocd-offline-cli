---
date: 2025-11-19T12:24:13+01:00
researcher: Claude
git_commit: 367d9d5e668218120279784c62b23427fab44e5b
branch: application
repository: argocd-offline-cli
topic: "Adding Multi-Source ArgoCD Application Support"
tags: [research, codebase, argocd, multi-source, applications]
status: complete
last_updated: 2025-11-19
last_updated_by: Claude
---

# Research: Adding Multi-Source ArgoCD Application Support

**Date**: 2025-11-19T12:24:13+01:00
**Researcher**: Claude
**Git Commit**: 367d9d5e668218120279784c62b23427fab44e5b
**Branch**: application
**Repository**: argocd-offline-cli

## Research Question

How can we add support for multi-source ArgoCD applications to the argocd-offline-cli? Is it easier if we assume the ArgoCD app is in the same repo as the other sources it points to? Do we need to add path logic to support it?

## Summary

Multi-source Application support can be added by iterating over each source in the `app.Spec.Sources` array and calling `GenerateManifest()` for each one, then combining the results. The existing repository service handles path resolution, so no additional path logic is needed. However, cross-source references (using `$ref/path` syntax) require special handling. Assuming all sources are in the same repository would significantly simplify implementation by avoiding the need to handle multiple repository clones and cross-repository references.

## Detailed Findings

### Current State: Multi-Source Applications Are Blocked

The current implementation explicitly rejects multi-source applications at [preview/shared.go:63-69](preview/shared.go#L63-L69):

```go
if app.Spec.Source == nil || app.Spec.Source.RepoURL == "" {
    if len(app.Spec.Sources) > 0 {
        log.Fatalf("Application '%s' uses multi-source format (.spec.sources[]), which is not yet supported.", app.Name)
    }
}
```

This was a conscious decision documented in the implementation plan ([thoughts/plans/2025-11-19-argocd-application-support.md:36](thoughts/plans/2025-11-19-argocd-application-support.md#L36)) to keep the initial scope manageable.

### Multi-Source Application Structure

Multi-source applications use `.spec.sources[]` array instead of `.spec.source` ([pkg/apis/application/v1alpha1/types.go:94](https://pkg.go.dev/github.com/argoproj/argo-cd/v3@v3.0.0/pkg/apis/application/v1alpha1#ApplicationSpec)):

```yaml
spec:
  sources:  # Array of ApplicationSource objects
    - repoURL: https://grafana.github.io/helm-charts
      chart: alloy
      targetRevision: 1.0.3
      helm:
        valueFiles:
          - $values/infra/alloy/values.yaml  # References another source
    - repoURL: git@github.com:example/config.git
      targetRevision: master
      ref: values  # Named reference for other sources
```

Key fields for multi-source:
- `ref`: Names a source for cross-referencing
- `name`: Display name for UI
- Value files can reference other sources using `$refname/path` syntax

### Repository Service API Constraints

The `repository.GenerateManifest()` API ([preview/shared.go:71-82](preview/shared.go#L71-L82)) processes **one source at a time**:

```go
response, err := repoService.GenerateManifest(context.Background(), &repoapiclient.ManifestRequest{
    ApplicationSource: app.Spec.Source,  // Single ApplicationSource, not array
    // ...
})
```

The `ManifestRequest` structure has:
- `ApplicationSource` field (singular, not array)
- `HasMultipleSources` boolean flag
- `RefSources` map for cross-source references

### How ArgoCD Server Handles Multi-Source

ArgoCD's server implementation shows the pattern ([server/application/application.go:487-558](https://github.com/argoproj/argo-cd/blob/v3.0.0/server/application/application.go#L487-L558)):

1. **Normalize to array**: Convert single-source to array for uniform processing
2. **Build ref sources map**: Identify sources with `ref` field for cross-referencing
3. **Iterate and generate**: Call `GenerateManifest()` for each source
4. **Combine results**: Merge all manifest responses into single output

```go
// Simplified pattern from ArgoCD server
sources := app.Spec.GetSources()  // Returns array whether single or multi
refSources := argo.GetRefSources(sources, ...)  // Build ref map

manifestInfos := make([]*ManifestResponse, 0)
for _, source := range sources {
    manifestInfo, err := client.GenerateManifest(&ManifestRequest{
        ApplicationSource: &source,
        HasMultipleSources: true,
        RefSources: refSources,
        // ...
    })
    manifestInfos = append(manifestInfos, manifestInfo)
}

// Combine all manifests
manifests := &ManifestResponse{}
for _, info := range manifestInfos {
    manifests.Manifests = append(manifests.Manifests, info.Manifests...)
}
```

### Path and Repository Handling

Current implementation ([preview/shared.go:48-58](preview/shared.go#L48-L58), [preview/helm.go:26-40](preview/helm.go#L26-L40)):

1. **Repository service initialization**: Uses temp directory for cloning/extracting
2. **Credential resolution**: From environment variables or Helm config
3. **Path handling**: Embedded in ApplicationSource, handled by repository service
4. **No local vs remote distinction**: Repository service handles all URL schemes

The repository service handles:
- Repository cloning to `/tmp/_argocd-offline-cli`
- Path resolution within repositories
- Different repository types (git, helm, OCI)

## Architecture Documentation

### Required Changes for Multi-Source Support

#### 1. Update Validation Logic

Replace the fatal error in [preview/shared.go:63-69](preview/shared.go#L63-L69) with multi-source handling:

```go
func generateAndOutputManifests(apps []argoappv1.Application, ...) {
    for _, app := range apps {
        sources := app.Spec.GetSources()  // Use ArgoCD's helper

        if app.Spec.HasMultipleSources() {
            // Multi-source path
            refSources := buildRefSources(sources)
            manifests := generateMultiSourceManifests(sources, refSources, ...)
        } else {
            // Single-source path (existing code)
            manifests := generateSingleSourceManifest(app.Spec.Source, ...)
        }
    }
}
```

#### 2. Implement Multi-Source Generation

```go
func generateMultiSourceManifests(sources []ApplicationSource, refSources map[string]*RefTarget, ...) {
    var allManifests []string

    for _, source := range sources {
        response, err := repoService.GenerateManifest(&ManifestRequest{
            ApplicationSource: &source,
            HasMultipleSources: true,
            RefSources: refSources,
            // ...
        })
        allManifests = append(allManifests, response.Manifests...)
    }

    return allManifests
}
```

#### 3. Handle Cross-Source References

For sources referencing other sources (e.g., `$values/path/to/file`):

```go
func buildRefSources(sources []ApplicationSource) map[string]*RefTarget {
    refSources := make(map[string]*RefTarget)

    for _, source := range sources {
        if source.Ref != "" {
            refSources[source.Ref] = &RefTarget{
                Source: source,
                // Additional metadata
            }
        }
    }

    return refSources
}
```

### Complexity Analysis: Same-Repo vs Different-Repo Sources

#### Same Repository Assumption (Simpler)

If all sources point to the same repository:

**Advantages:**
- Single repository clone operation
- Direct file path resolution for cross-references
- No credential management for multiple repos
- Simpler `$ref/path` resolution - just filesystem paths

**Implementation:**
```go
// All sources share same RepoURL
baseRepo := sources[0].RepoURL
for _, source := range sources {
    if source.RepoURL != baseRepo {
        return error("All sources must be in same repository")
    }
}
```

#### Different Repository Support (Complex)

Supporting sources from different repositories:

**Additional Requirements:**
- Clone multiple repositories to different temp directories
- Map each `ref` to its repository location
- Handle credentials for each unique repository
- Resolve `$ref/path` across repository boundaries

**Implementation Complexity:**
```go
// Need to track repo -> temp directory mapping
repoTempDirs := make(map[string]string)
for _, source := range sources {
    if _, exists := repoTempDirs[source.RepoURL]; !exists {
        tempDir := cloneRepository(source.RepoURL)
        repoTempDirs[source.RepoURL] = tempDir
    }
}
```

## Historical Context (from thoughts/)

From the implementation plan ([thoughts/plans/2025-11-19-argocd-application-support.md:36](thoughts/plans/2025-11-19-argocd-application-support.md#L36)):
- Multi-source support was explicitly listed under "What We're NOT Doing"
- Marked as "can be added later"
- Prioritized getting single-source working first

From research document ([thoughts/research/2025-11-19-adding-application-support-argocd-v3-upgrade.md](thoughts/research/2025-11-19-adding-application-support-argocd-v3-upgrade.md)):
- Raised as open question: "ArgoCD v3 supports multi-source Applications - should we add this capability?"

## Code References

- `preview/shared.go:63-69` - Current multi-source detection and rejection
- `preview/shared.go:71-82` - Single-source manifest generation call
- `preview/helm.go:26-40` - Credential resolution functions
- `pkg/apis/application/v1alpha1/types.go:223-288` - ArgoCD helper methods for source handling
- `server/application/application.go:487-558` - ArgoCD server multi-source implementation pattern
- `test-app.yaml:15-25` - Example multi-source application
- `test-app-single-source.yaml:8-11` - Example single-source application

## Related Research

- `thoughts/plans/2025-11-19-argocd-application-support.md` - Original implementation plan deferring multi-source
- `thoughts/research/2025-11-19-adding-application-support-argocd-v3-upgrade.md` - Initial research raising multi-source question

## Open Questions

1. **Cross-source reference handling**: How should `$ref/path` references be resolved when sources are in different repositories?
2. **Performance**: Should manifest generation be parallelized for multiple sources?
3. **Error handling**: Should one source failing cause entire application to fail?
4. **Credential management**: How to handle different credentials for different source repositories?
5. **Local testing**: Should we support mixing local file:// sources with remote sources?

## Recommendations

### Phased Approach

**Phase 1: Same-Repository Multi-Source (Recommended Start)**
- Require all sources to use the same `repoURL`
- Simple path resolution for cross-references
- Minimal changes to existing code
- Covers common use case (Helm chart + values in same repo)

**Phase 2: Different-Repository Support**
- Add repository cloning management
- Implement cross-repository reference resolution
- Handle multiple credential sets
- More complex but full ArgoCD compatibility

### Implementation Estimate

**Same-Repository Only**:
- Modify validation logic: 1 hour
- Add iteration logic: 2 hours
- Test with examples: 1 hour
- Total: ~4 hours

**Full Multi-Repository Support**:
- All of above plus:
- Repository management: 3 hours
- Cross-repo references: 2 hours
- Credential handling: 1 hour
- Total: ~10 hours

### Answer to Specific Questions

1. **How can we add this?**
   - Iterate over `app.Spec.Sources` array
   - Call `GenerateManifest()` for each source
   - Combine results into single manifest set
   - Handle `RefSources` map for cross-references

2. **Is it easier if sources are in same repo?**
   - **Yes, significantly easier**
   - Avoids multiple repository clones
   - Simplifies `$ref/path` resolution to filesystem paths
   - Single credential set
   - Recommended as Phase 1 implementation

3. **Do we need path logic?**
   - **No additional path logic needed** for basic support
   - Repository service already handles paths
   - Only need to handle `$ref/path` syntax in value files
   - Same-repo makes this trivial (filesystem paths)
   - Different-repo requires mapping refs to temp directories