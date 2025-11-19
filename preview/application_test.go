package preview

import (
	"testing"

	"github.com/stretchr/testify/require"
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

// TestLoadApplications verifies that applications are loaded correctly
// and that the pointer-to-value slice conversion works properly.
// Note: This is primarily an integration test with cmdutil.ConstructApps,
// but it verifies our conversion logic and serves as a smoke test.
func TestLoadApplications(t *testing.T) {
	apps := loadApplications("../testdata/test-app.yaml")
	require.Len(t, apps, 1, "Expected 1 application")

	// Verify we have value types, not pointers
	require.Equal(t, "test-app", apps[0].Name)
	require.Equal(t, "default", apps[0].Spec.Project)
	require.Equal(t, "https://github.com/argoproj/argocd-example-apps", apps[0].Spec.Source.RepoURL)
	require.Equal(t, "guestbook", apps[0].Spec.Source.Path)
	require.Equal(t, "HEAD", apps[0].Spec.Source.TargetRevision)
}

// TestLoadMultipleApplications verifies that multiple applications in a single
// YAML file are all loaded and properly converted from pointer to value slices.
func TestLoadMultipleApplications(t *testing.T) {
	apps := loadApplications("../testdata/test-apps-multiple.yaml")
	require.Len(t, apps, 2, "Expected 2 applications")

	// Verify first app
	require.Equal(t, "app-one", apps[0].Name)
	require.Equal(t, "default", apps[0].Spec.Project)
	require.Equal(t, "https://example.com/repo", apps[0].Spec.Source.RepoURL)
	require.Equal(t, "app1", apps[0].Spec.Source.Path)

	// Verify second app
	require.Equal(t, "app-two", apps[1].Name)
	require.Equal(t, "default", apps[1].Spec.Project)
	require.Equal(t, "https://example.com/repo", apps[1].Spec.Source.RepoURL)
	require.Equal(t, "app2", apps[1].Spec.Source.Path)
}
