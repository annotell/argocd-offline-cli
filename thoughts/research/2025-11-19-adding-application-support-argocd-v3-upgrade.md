---
date: 2025-11-19T10:13:57+0100
researcher: Claude Code
git_commit: d5de995db3ba154afac8b52547c8050ef6897d6e
branch: main
repository: argocd-offline-cli
topic: "Adding ArgoCD Application support and upgrading to ArgoCD v3"
tags: [research, codebase, argocd, applicationset, application, v3-upgrade]
status: complete
last_updated: 2025-11-19
last_updated_by: Claude Code
---

# Research: Adding ArgoCD Application Support and Upgrading to ArgoCD v3

**Date**: 2025-11-19T10:13:57+0100
**Researcher**: Claude Code
**Git Commit**: d5de995db3ba154afac8b52547c8050ef6897d6e
**Branch**: main
**Repository**: argocd-offline-cli

## Research Question

Today this CLI only supports ApplicationSets. I also want to be able to render ArgoCD Applications. What do we need to solve this? Does the ArgoCD utils package have anything useful? I would also like to bump it to using ArgoCD v3. Let's make that step as part 1 of the plan.

## Summary

The codebase currently implements ApplicationSet rendering by generating Applications from ApplicationSet templates and then rendering their Kubernetes manifests. Adding direct Application support would be straightforward as most of the infrastructure already exists - the repository service and manifest generation code currently used for ApplicationSets can be reused. The ArgoCD utils package provides useful utilities for resource tracking and git operations, though most Application-specific logic comes from other ArgoCD packages. Upgrading to ArgoCD v3 is a low-risk operation requiring primarily import path changes from `/v2/` to `/v3/` with minimal behavioral changes.

## Detailed Findings

### Current ApplicationSet Implementation

The codebase currently handles ApplicationSet rendering through a multi-stage process ([preview/applicationset.go](preview/applicationset.go)):

1. **ApplicationSet Loading** ([preview/applicationset.go:166](preview/applicationset.go#L166))
   - Uses `cmdutil.ConstructApplicationSet()` to parse YAML files
   - Returns slice of `argoappv1.Application` objects

2. **Application Generation** ([preview/applicationset.go:175](preview/applicationset.go#L175))
   - Uses `appsettemplate.GenerateApplications()` to expand templates
   - Supports List, Matrix, and Merge generators ([preview/applicationset.go:182-197](preview/applicationset.go#L182-L197))
   - Produces Application objects from ApplicationSet templates

3. **Manifest Generation** ([preview/applicationset.go:78-160](preview/applicationset.go#L78-L160))
   - Creates repository service with ArgoCD utilities
   - Generates Kubernetes manifests from each Application
   - Uses Helm repository credentials from environment or config

### ArgoCD Utils Package Capabilities

The codebase uses three ArgoCD util packages ([preview/applicationset.go:21-22](preview/applicationset.go#L21-L22), [preview/cache.go:8](preview/cache.go#L8)):

1. **`util/argo`** - Provides `NewResourceTracking()` for resource management
2. **`util/git`** - Provides `NoopCredsStore{}` for offline operation
3. **`util/cache`** - Provides cache types for no-op cache implementation

Additional ArgoCD packages provide Application-specific functionality:
- `cmd/util` - Contains `ConstructApplicationSet()` and potentially `ConstructApplication()`
- `cmd/argocd/commands` - Provides `PrintResource()` and `PrintResourceList()` for output
- `reposerver/repository` - Service for generating manifests from Applications

### What's Needed for Application Support

To add Application rendering support, we need:

1. **Application Loading Function**
   - Similar to `cmdutil.ConstructApplicationSet()`, we need a function to load Application YAML
   - The ArgoCD `cmd/util` package likely has `ConstructApplication()` or similar

2. **New CLI Commands**
   - `app preview` - Render Application spec (similar to current `appset preview-apps`)
   - `app preview-resources` - Generate Kubernetes manifests (reuse existing manifest generation)

3. **Application Processing Logic**
   - Skip the ApplicationSet template generation step
   - Directly pass loaded Applications to the existing manifest generation code
   - Reuse the repository service setup from `PreviewResources()`

### Current ArgoCD v2 Dependencies

The codebase uses ([go.mod:6](go.mod#L6)):
- ArgoCD v2.13.0 as primary dependency
- Go 1.24 requirement
- Multiple ArgoCD v2 packages imported across the codebase

All imports use the `/v2/` path:
- `github.com/argoproj/argo-cd/v2/applicationset/...`
- `github.com/argoproj/argo-cd/v2/cmd/...`
- `github.com/argoproj/argo-cd/v2/pkg/...`
- `github.com/argoproj/argo-cd/v2/reposerver/...`
- `github.com/argoproj/argo-cd/v2/util/...`

### ArgoCD v3 Migration Requirements

Based on external research, upgrading to ArgoCD v3 involves:

1. **Module Path Changes** (Primary change for libraries)
   - Change all imports from `github.com/argoproj/argo-cd/v2` to `github.com/argoproj/argo-cd/v3`
   - Update `go.mod` dependency to ArgoCD v3.x.x

2. **Behavioral Changes** (Minimal impact for offline CLI)
   - Resource tracking defaults to annotation-based (was label-based)
   - ApplicationSet `applyNestedSelectors` field ignored (always applies)
   - No breaking changes to Application/ApplicationSet CRD schemas (still v1alpha1)

3. **New Features Available in v3**
   - Parallel Helm manifest generation (performance improvement)
   - Enhanced ApplicationSet utils with better template rendering
   - Improved generator support

## Implementation Plan

### Part 1: Upgrade to ArgoCD v3

1. **Update Dependencies**
   - Change `go.mod` to use `github.com/argoproj/argo-cd/v3 v3.0.0` (or latest)
   - Run `go mod tidy` to update transitive dependencies

2. **Update All Import Paths**
   - Files to update:
     - [preview/applicationset.go](preview/applicationset.go#L12-L22)
     - [preview/cache.go](preview/cache.go#L7-L8)
   - Replace all `/v2/` with `/v3/` in import paths

3. **Test Existing Functionality**
   - Verify ApplicationSet rendering still works
   - Check that manifest generation produces same output

### Part 2: Add Application Support

1. **Create Application Loading Function**
   ```go
   func loadApplications(filename string) []argoappv1.Application {
       // Check if cmdutil has ConstructApplication()
       // If not, implement YAML unmarshaling directly
   }
   ```

2. **Add Application Preview Functions**
   ```go
   func PreviewApplication(filename string, output string) {
       // Load Application YAML
       // Output using argocmd.PrintResource()
   }

   func PreviewApplicationResources(filename string, resKind string, output string) {
       // Load Application
       // Reuse existing manifest generation from PreviewResources()
   }
   ```

3. **Add CLI Commands** ([cmd/commands/root.go](cmd/commands/root.go))
   ```go
   func AppCommand() *cobra.Command {
       command := &cobra.Command{
           Use:   "app",
           Short: "Preview Applications",
       }
       command.AddCommand(PreviewApplicationCommand())
       command.AddCommand(PreviewApplicationResourcesCommand())
       return command
   }
   ```

4. **Update Root Command**
   - Add `AppCommand()` to root command structure

## Code References

### Key Files
- [preview/applicationset.go](preview/applicationset.go) - ApplicationSet rendering logic
- [preview/helm.go](preview/helm.go) - Helm repository credential management
- [preview/cache.go](preview/cache.go) - No-op cache implementation
- [cmd/commands/root.go](cmd/commands/root.go) - CLI command definitions
- [go.mod](go.mod) - Dependency management

### Key Functions
- `preview.generateApplications()` [preview/applicationset.go:165](preview/applicationset.go#L165) - Generates Applications from ApplicationSet
- `preview.PreviewResources()` [preview/applicationset.go:78](preview/applicationset.go#L78) - Generates Kubernetes manifests
- `repository.NewService()` [preview/applicationset.go:90](preview/applicationset.go#L90) - Creates manifest generation service
- `cmdutil.ConstructApplicationSet()` [preview/applicationset.go:166](preview/applicationset.go#L166) - Loads ApplicationSet YAML

## Architecture Documentation

### Current Flow (ApplicationSet Only)
1. User provides ApplicationSet YAML file
2. CLI loads and parses ApplicationSet
3. Templates expanded to generate Applications
4. Each Application processed to generate manifests
5. Manifests formatted and output

### Proposed Flow (With Application Support)
1. User provides Application OR ApplicationSet YAML
2. CLI detects resource type
3. If Application: Load directly
4. If ApplicationSet: Generate Applications (existing flow)
5. Process Applications to generate manifests (shared code)
6. Format and output manifests

### Shared Components
- Repository service initialization
- Manifest generation logic
- Helm credential management
- Output formatting utilities

## Related Research

The ArgoCD utils package structure remains consistent between v2 and v3, with the primary change being import paths. The applicationset/utils package in v3 provides enhanced template rendering capabilities that could be useful for future features, though not strictly necessary for basic Application support.

## Open Questions

1. **Does `cmd/util` have a `ConstructApplication()` function?**
   - Need to verify by examining ArgoCD v3 source or testing imports

2. **Should we auto-detect Application vs ApplicationSet?**
   - Could examine `kind` field in YAML to route to appropriate handler

3. **Resource filtering in Application preview?**
   - Should `app preview-resources` support the same `-k` filtering as ApplicationSet?

4. **Multi-source Application support?**
   - ArgoCD v3 supports multi-source Applications - should we add this capability?