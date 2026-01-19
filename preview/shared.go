package preview

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	argocmd "github.com/argoproj/argo-cd/v3/cmd/argocd/commands"
	argoappv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	repoapiclient "github.com/argoproj/argo-cd/v3/reposerver/apiclient"
	"github.com/argoproj/argo-cd/v3/reposerver/metrics"
	"github.com/argoproj/argo-cd/v3/reposerver/repository"
	"github.com/argoproj/argo-cd/v3/util/git"
	"github.com/argoproj/pkg/errors"
	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	applicationAPIVersion = "argoproj.io/v1alpha1"
	applicationKind       = "Application"
)

// normalizeGitURL converts various Git URL formats to a comparable form
// This allows comparison of SSH and HTTPS URLs for the same repository
func normalizeGitURL(url string) string {
	// Convert SSH to HTTPS format for comparison
	if strings.HasPrefix(url, "git@") {
		// git@github.com:owner/repo.git -> github.com/owner/repo
		url = strings.TrimPrefix(url, "git@")
		url = strings.Replace(url, ":", "/", 1)
	}

	// Remove protocol
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")

	// Remove .git suffix
	url = strings.TrimSuffix(url, ".git")

	return strings.ToLower(url)
}

// isLocalRepository checks if the given repoURL matches the current git repository
// Returns: (isLocal bool, localPath string, error)
//
// Return value combinations:
// - (true, "/path/to/repo", nil): repoURL matches current repo, use local path
// - (false, "", nil): repoURL does not match, or not in a git repo, or no origin configured
// - (false, "", error): matched but failed to get repo root (unexpected error)
func isLocalRepository(repoURL string) (bool, string, error) {
	// Get current repository's remote URL
	cmd := exec.Command("git", "config", "--get", "remote.origin.url")
	output, err := cmd.Output()
	if err != nil {
		// Not in a git repo or no origin - this is not an error condition
		return false, "", nil
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

// resolveLocalRevision resolves a git revision to HEAD SHA for local repositories
// This ensures ArgoCD uses the current working directory content
func resolveLocalRevision(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to resolve HEAD in %s: %w", repoPath, err)
	}
	return strings.TrimSpace(string(output)), nil
}

// shouldMatch returns true if the value is non-empty
func shouldMatch(v string) bool {
	return len(v) > 0
}

// getCacheDir returns the cache directory for repositories and helm charts.
// Uses the system temp directory to avoid cross-device link errors when
// ArgoCD needs to move files between directories.
func getCacheDir() string {
	return filepath.Join(os.TempDir(), "_argocd-offline-cli")
}

// generateAndOutputManifests generates manifests for Applications and outputs them
func generateAndOutputManifests(apps []argoappv1.Application, appName string, resKind string, output string) {
	max, err := resource.ParseQuantity("100G")
	errors.CheckError(err)
	maxValue := max.ToDec().Value()
	initConstants := repository.RepoServerInitConstants{
		HelmManifestMaxExtractedSize:      maxValue,
		HelmRegistryMaxIndexSize:          maxValue,
		MaxCombinedDirectoryManifestsSize: max,
		StreamedManifestMaxExtractedSize:  maxValue,
		StreamedManifestMaxTarSize:        maxValue,
	}

	repoService := repository.NewService(
		metrics.NewMetricsServer(),
		NewNoopCache(),
		initConstants,
		git.NoopCredsStore{},
		getCacheDir(),
	)
	if err := repoService.Init(); err != nil {
		log.Fatal("failed to initialize the repo service: ", err)
	}

	for _, app := range apps {
		// Skip apps that don't match the filter
		if shouldMatch(appName) && appName != app.Name {
			continue
		}

		manifests := generateAppManifests(repoService, app)
		resources := filterResources(manifests, resKind)
		printResources(resources, output)
	}
}

// generateAppManifests generates manifests for a single application
func generateAppManifests(repoService *repository.Service, app argoappv1.Application) []string {
	// Normalize source handling using ArgoCD v3 helper methods
	sources := app.Spec.GetSources() // Normalize to array
	if len(sources) == 0 {
		log.Fatalf("Application '%s' has no source configured (.spec.source or .spec.sources)", app.Name)
	}

	var manifests []string
	var err error

	if app.Spec.HasMultipleSources() {
		// Multi-source path
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

	return manifests
}

// filterResources parses manifests and filters by resource kind
func filterResources(manifests []string, resKind string) map[string][]unstructured.Unstructured {
	resources := map[string][]unstructured.Unstructured{}

	for _, manifest := range manifests {
		resource := unstructured.Unstructured{}
		err := json.Unmarshal([]byte(manifest), &resource)
		errors.CheckError(err)

		kind := strings.ToLower(resource.GetKind())
		if shouldMatch(resKind) && resKind != kind {
			continue
		}

		if _, ok := resources[kind]; !ok {
			resources[kind] = make([]unstructured.Unstructured, 0)
		}
		resources[kind] = append(resources[kind], resource)
	}

	return resources
}

// printResources outputs resources in the specified format
func printResources(resources map[string][]unstructured.Unstructured, output string) {
	kinds := make([]string, 0, len(resources))
	for kind := range resources {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)

	switch output {
	case "name":
		printResourceNames(kinds, resources)
	case "json", "yaml":
		for _, kind := range kinds {
			if err := argocmd.PrintResourceList(resources[kind], output, false); err != nil {
				log.Fatal(err)
			}
		}
	default:
		errors.CheckError(fmt.Errorf("unknown output format: %s", output))
	}
}

// printResourceNames prints resources in name format
func printResourceNames(kinds []string, resources map[string][]unstructured.Unstructured) {
	for i, kind := range kinds {
		if i > 0 {
			fmt.Println()
		}
		fmt.Println("NAME")
		for _, resource := range resources[kind] {
			fmt.Printf("%s/%s\n", kind, resource.GetName())
		}
	}
}

// generateSingleSourceManifest handles manifest generation for traditional single-source applications
func generateSingleSourceManifest(repoService *repository.Service, app argoappv1.Application) ([]string, error) {
	if app.Spec.Source == nil || app.Spec.Source.RepoURL == "" {
		return nil, fmt.Errorf("application has no valid source configuration")
	}

	// Check if this is a local repository
	var repoOverride *argoappv1.Repository
	// Use app.Spec.Source by default, may be replaced with modified copy for local repos
	applicationSource := app.Spec.Source

	isLocal, localPath, _ := isLocalRepository(app.Spec.Source.RepoURL)
	if isLocal {
		log.Infof("Detected local repository for %s, using path: %s", app.Name, localPath)

		// Resolve to HEAD for local repositories
		resolvedRevision, err := resolveLocalRevision(localPath)
		if err != nil {
			// Intentionally use original value when resolution fails to allow
			// graceful fallback for edge cases
			log.Warnf("Failed to resolve local revision: %v, using original", err)
		} else {
			log.Debugf("Resolved targetRevision to HEAD: %s", resolvedRevision)
			// Create a copy with resolved revision to avoid modifying original
			sourceCopy := app.Spec.Source.DeepCopy()
			sourceCopy.TargetRevision = resolvedRevision
			applicationSource = sourceCopy
		}

		// localPath is from git rev-parse --show-toplevel and is therefore trusted
		repoOverride = &argoappv1.Repository{
			Repo: "file://" + filepath.ToSlash(localPath),
			Type: "git",
		}
	} else {
		// Use existing credential resolution
		log.Debugf("Using remote repository for %s: %s", app.Name, app.Spec.Source.RepoURL)
		repoOverride = &argoappv1.Repository{
			Repo:     app.Spec.Source.RepoURL,
			Username: FindRepoUsername(app.Spec.Source.RepoURL),
			Password: FindRepoPassword(app.Spec.Source.RepoURL),
		}
	}

	response, err := repoService.GenerateManifest(context.Background(), &repoapiclient.ManifestRequest{
		ApplicationSource: applicationSource,
		AppName:           app.Name,
		Namespace:         app.Spec.Destination.Namespace,
		NoCache:           true,
		Repo:              repoOverride,
		ProjectName:       "applications",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate manifests: %w", err)
	}

	return response.Manifests, nil
}

// generateMultiSourceManifests handles manifest generation for multi-source applications
// validateGitSourcesConstraint validates that all Git sources use the same repository URL
// Helm chart sources (with Chart field set) are allowed to use different repositories
func validateGitSourcesConstraint(sources []argoappv1.ApplicationSource) error {
	var baseGitRepoURL string
	firstGitSourceIndex := -1

	for i, source := range sources {
		if source.RepoURL == "" {
			return fmt.Errorf("source at index %d has empty repoURL", i)
		}

		// Skip Helm chart sources - they're allowed to be from different repos
		if source.Chart != "" {
			continue
		}

		// For Git sources, ensure they all use the same repo
		if baseGitRepoURL == "" {
			baseGitRepoURL = source.RepoURL
			firstGitSourceIndex = i
		} else if source.RepoURL != baseGitRepoURL {
			return fmt.Errorf("all Git repository sources must use the same repository. "+
				"Source at index %d uses '%s' but source at index %d (first Git source) uses '%s'",
				i, source.RepoURL, firstGitSourceIndex, baseGitRepoURL)
		}
	}

	return nil
}

// resolveLocalRevisions resolves targetRevision to HEAD for local repositories
// Returns the resolved sources and their local paths
func resolveLocalRevisions(
	sources []argoappv1.ApplicationSource,
	appName string,
) ([]argoappv1.ApplicationSource, []string) {
	resolvedSources := make([]argoappv1.ApplicationSource, len(sources))
	localPaths := make([]string, len(sources))

	for i, source := range sources {
		resolvedSources[i] = source

		isLocal, localPath, _ := isLocalRepository(source.RepoURL)
		if !isLocal || source.Chart != "" {
			continue
		}

		// Only resolve for Git sources, not Helm charts
		log.Infof("Detected local repository for source %d in %s, using path: %s", i, appName, localPath)
		localPaths[i] = localPath

		resolvedRevision, err := resolveLocalRevision(localPath)
		if err != nil {
			// Intentionally use original value when resolution fails to allow graceful fallback
			log.Warnf("Failed to resolve local revision: %v, using original", err)
			continue
		}

		log.Debugf("Resolved targetRevision to HEAD: %s", resolvedRevision)
		resolvedSources[i].TargetRevision = resolvedRevision
	}

	return resolvedSources, localPaths
}

// createRepoOverride creates a repository override for a source
func createRepoOverride(
	sourceCopy argoappv1.ApplicationSource,
	localPath string,
	sourceIndex int,
	appName string,
) *argoappv1.Repository {
	if localPath != "" {
		// localPath is from git rev-parse --show-toplevel and is therefore trusted
		return &argoappv1.Repository{
			Repo: "file://" + filepath.ToSlash(localPath),
			Type: "git",
		}
	}

	// Repository credentials are resolved per-source using the source's repoURL
	log.Debugf("Using remote repository for source %d in %s: %s", sourceIndex, appName, sourceCopy.RepoURL)
	return &argoappv1.Repository{
		Repo:     sourceCopy.RepoURL,
		Username: FindRepoUsername(sourceCopy.RepoURL),
		Password: FindRepoPassword(sourceCopy.RepoURL),
	}
}

// Constraint: all Git repository sources must use the same repository URL
// Helm chart sources (with Chart field set) are allowed to use different repositories
func generateMultiSourceManifests(repoService *repository.Service, app argoappv1.Application) ([]string, error) {
	sources := app.Spec.GetSources()
	if len(sources) == 0 {
		return nil, fmt.Errorf("no sources found in multi-source application")
	}

	if err := validateGitSourcesConstraint(sources); err != nil {
		return nil, err
	}

	// Resolve local revisions and build refSources with resolved values
	resolvedSources, localPaths := resolveLocalRevisions(sources, app.Name)
	refSources := buildRefSources(resolvedSources)

	// Generate manifests for each source
	var allManifests []string
	for i := range sources {
		sourceCopy := resolvedSources[i]
		repoOverride := createRepoOverride(sourceCopy, localPaths[i], i, app.Name)

		response, err := repoService.GenerateManifest(context.Background(), &repoapiclient.ManifestRequest{
			ApplicationSource:  &sourceCopy,
			AppName:            app.Name,
			Namespace:          app.Spec.Destination.Namespace,
			NoCache:            true,
			HasMultipleSources: true,
			RefSources:         refSources,
			Repo:               repoOverride,
			ProjectName:        "applications",
		})
		if err != nil {
			return nil, fmt.Errorf("failed to generate manifests for source %d: %w", i, err)
		}

		allManifests = append(allManifests, response.Manifests...)
	}

	return allManifests, nil
}

// buildRefSources creates a map of named source references for cross-source value file resolution
// The map keys use the "$ref" format (e.g., "$values") to match ArgoCD's cross-source reference syntax
//
// Note: RefTarget intentionally does NOT include the Path field from ApplicationSource.
// This is by design in ArgoCD v3's API. The Path is used during manifest generation, but
// the RefTarget only needs to identify the repository, revision, and chart (if Helm).
// The actual path resolution happens during the GenerateManifest call for each source.
func buildRefSources(sources []argoappv1.ApplicationSource) map[string]*argoappv1.RefTarget {
	refSources := make(map[string]*argoappv1.RefTarget)

	for _, source := range sources {
		if source.Ref != "" {
			// Add "$" prefix to match ArgoCD's reference syntax
			refKey := "$" + source.Ref
			refSources[refKey] = &argoappv1.RefTarget{
				TargetRevision: source.TargetRevision,
				Repo: argoappv1.Repository{
					Repo: source.RepoURL,
				},
				Chart: source.Chart,
			}
		}
	}

	return refSources
}
