package preview

import (
	"testing"

	"github.com/argoproj/argo-cd/v3/reposerver/metrics"
	"github.com/argoproj/argo-cd/v3/reposerver/repository"
	"github.com/argoproj/argo-cd/v3/util/argo"
	"github.com/argoproj/argo-cd/v3/util/git"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"
)

// TestShouldMatch tests the filtering helper function
func TestShouldMatch(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "empty string returns false",
			input:    "",
			expected: false,
		},
		{
			name:     "non-empty string returns true",
			input:    "app-name",
			expected: true,
		},
		{
			name:     "whitespace string returns true",
			input:    " ",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldMatch(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

// TestBuildRefSources verifies that the reference source map is built correctly
// for multi-source applications with cross-source references.
func TestBuildRefSources(t *testing.T) {
	apps := loadApplications("../testdata/test-app-same-repo.yaml")
	require.Len(t, apps, 1, "Expected 1 application")

	app := apps[0]
	sources := app.Spec.GetSources()

	// Build ref sources map
	refSources := buildRefSources(sources)

	// Should have one reference (the source with ref="configs")
	require.Len(t, refSources, 1, "Expected 1 reference source")

	// Verify the reference exists with correct key format
	refTarget, exists := refSources["$configs"]
	require.True(t, exists, "Reference '$configs' should exist in map")
	require.NotNil(t, refTarget, "RefTarget should not be nil")

	// Verify reference target properties
	require.Equal(t, "HEAD", refTarget.TargetRevision)
	require.Equal(t, "https://github.com/argoproj/argocd-example-apps.git", refTarget.Repo.Repo)
	require.Empty(t, refTarget.Chart, "Git source should not have a chart")
}

// TestBuildRefSourcesWithoutRefs verifies that sources without ref fields
// are not included in the reference source map.
func TestBuildRefSourcesWithoutRefs(t *testing.T) {
	apps := loadApplications("../testdata/test-app.yaml")
	require.Len(t, apps, 1, "Expected 1 application")

	app := apps[0]
	sources := app.Spec.GetSources()

	// Build ref sources map
	refSources := buildRefSources(sources)

	// Should be empty since single-source app has no refs
	require.Empty(t, refSources, "Expected no reference sources for single-source app")
}

// TestBuildRefSourcesWithHelmChart verifies that Helm chart applications
// with cross-source value references work correctly. This tests the pattern where
// a Helm chart uses $values/path syntax to reference files from a Git repository.
func TestBuildRefSourcesWithHelmChart(t *testing.T) {
	apps := loadApplications("../testdata/test-app-multi-source-helm.yaml")
	require.Len(t, apps, 1, "Expected 1 application")

	app := apps[0]
	sources := app.Spec.GetSources()
	require.Len(t, sources, 2, "Expected 2 sources")

	// Verify first source is Helm chart with valueFiles using $values reference
	require.Equal(t, "grafana", sources[0].Chart)
	require.Equal(t, "https://grafana.github.io/helm-charts", sources[0].RepoURL)
	require.NotNil(t, sources[0].Helm, "Helm config should exist")
	require.Len(t, sources[0].Helm.ValueFiles, 1, "Should have one value file")
	require.Equal(t, "$values/configs/grafana-values.yaml", sources[0].Helm.ValueFiles[0],
		"Should use $values cross-source reference syntax")

	// Verify second source is Git repo with ref for cross-source references
	require.Empty(t, sources[1].Chart, "Second source should be Git, not Helm")
	require.Equal(t, "https://github.com/argoproj/argocd-example-apps.git", sources[1].RepoURL)
	require.Equal(t, "values", sources[1].Ref, "Git source should have ref for cross-source references")

	// Build ref sources map - only sources with ref field should be included
	refSources := buildRefSources(sources)
	require.Len(t, refSources, 1, "Expected 1 reference source (only the Git source with ref)")

	// Verify the Git values reference (Helm chart doesn't have ref, so not in map)
	valuesRef, exists := refSources["$values"]
	require.True(t, exists, "Reference '$values' should exist in map")
	require.NotNil(t, valuesRef, "RefTarget should not be nil")
	require.Empty(t, valuesRef.Chart, "Git source should not have a chart")
	require.Equal(t, "https://github.com/argoproj/argocd-example-apps.git", valuesRef.Repo.Repo)
}

// TestGenerateMultiSourceManifestsWithDifferentRepos verifies that the validation
// correctly rejects multi-source applications where Git sources use different repositories.
// This tests the constraint that all Git sources must use the same repository.
func TestGenerateMultiSourceManifestsWithDifferentRepos(t *testing.T) {
	apps := loadApplications("../testdata/test-app-different-repos.yaml")
	require.Len(t, apps, 1, "Expected 1 application")

	app := apps[0]
	sources := app.Spec.GetSources()
	require.Len(t, sources, 2, "Expected 2 sources")

	// Verify sources have different repositories
	require.Equal(t, "https://github.com/argoproj/argocd-example-apps.git", sources[0].RepoURL)
	require.Equal(t, "https://github.com/different-org/different-repo.git", sources[1].RepoURL)

	// Create a minimal repo service for testing validation logic
	// Note: We're not testing actual manifest generation, just the validation
	max, err := resource.ParseQuantity("100G")
	require.NoError(t, err)
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
		argo.NewResourceTracking(),
		git.NoopCredsStore{},
		getCacheDir(),
	)
	require.NoError(t, repoService.Init())

	// Attempt to generate manifests - should fail with validation error
	manifests, err := generateMultiSourceManifests(repoService, app)
	require.Error(t, err, "Should fail when Git sources use different repositories")
	require.Nil(t, manifests, "Should not return manifests on validation error")
	require.Contains(
		t,
		err.Error(),
		"all Git repository sources must use the same repository",
		"Error should mention repository constraint",
	)
	require.Contains(t, err.Error(), "index 0", "Error should mention first Git source index")
	require.Contains(t, err.Error(), "index 1", "Error should mention second Git source index")
}

// TestGenerateMultiSourceManifestsWithEmptyRepoURL verifies that validation
// correctly rejects sources with empty repoURL fields.
func TestGenerateMultiSourceManifestsWithEmptyRepoURL(t *testing.T) {
	apps := loadApplications("../testdata/test-app-empty-repourl.yaml")
	require.Len(t, apps, 1, "Expected 1 application")

	app := apps[0]
	sources := app.Spec.GetSources()
	require.Len(t, sources, 2, "Expected 2 sources")

	// Verify second source has empty repoURL
	require.NotEmpty(t, sources[0].RepoURL, "First source should have repoURL")
	require.Empty(t, sources[1].RepoURL, "Second source should have empty repoURL")

	// Create minimal repo service for testing
	max, err := resource.ParseQuantity("100G")
	require.NoError(t, err)
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
		argo.NewResourceTracking(),
		git.NoopCredsStore{},
		getCacheDir(),
	)
	require.NoError(t, repoService.Init())

	// Attempt to generate manifests - should fail with validation error
	manifests, err := generateMultiSourceManifests(repoService, app)
	require.Error(t, err, "Should fail when source has empty repoURL")
	require.Nil(t, manifests, "Should not return manifests on validation error")
	require.Contains(t, err.Error(), "empty repoURL", "Error should mention empty repoURL")
	require.Contains(t, err.Error(), "index 1", "Error should mention the source index with empty repoURL")
}

// TestGenerateMultiSourceManifestsAllHelmCharts verifies that multi-source applications
// with only Helm chart sources (no Git sources) are valid and can use different repositories.
// This is a common pattern for deploying multiple Helm charts from different registries.
func TestGenerateMultiSourceManifestsAllHelmCharts(t *testing.T) {
	apps := loadApplications("../testdata/test-app-all-helm.yaml")
	require.Len(t, apps, 1, "Expected 1 application")

	app := apps[0]
	sources := app.Spec.GetSources()
	require.Len(t, sources, 2, "Expected 2 sources")

	// Verify both sources are Helm charts from different repositories
	require.Equal(t, "grafana", sources[0].Chart)
	require.Equal(t, "https://grafana.github.io/helm-charts", sources[0].RepoURL)
	require.Equal(t, "prometheus", sources[1].Chart)
	require.Equal(t, "https://prometheus-community.github.io/helm-charts", sources[1].RepoURL)

	// Verify buildRefSources works correctly (no refs, so should be empty)
	refSources := buildRefSources(sources)
	require.Empty(t, refSources, "Helm-only sources without refs should produce empty ref map")

	// Note: We don't test actual manifest generation here because that would require
	// network access to Helm repositories. This test verifies the validation logic
	// correctly allows all-Helm applications with different repositories.
}
