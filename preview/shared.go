package preview

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	argocmd "github.com/argoproj/argo-cd/v3/cmd/argocd/commands"
	argoappv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	repoapiclient "github.com/argoproj/argo-cd/v3/reposerver/apiclient"
	"github.com/argoproj/argo-cd/v3/reposerver/metrics"
	"github.com/argoproj/argo-cd/v3/reposerver/repository"
	"github.com/argoproj/argo-cd/v3/util/argo"
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

// shouldMatch returns true if the value is non-empty
func shouldMatch(v string) bool {
	return len(v) > 0
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
		argo.NewResourceTracking(),
		git.NoopCredsStore{},
		filepath.Join(os.TempDir(), "_argocd-offline-cli"),
	)
	if err := repoService.Init(); err != nil {
		log.Fatal("failed to initialize the repo service: ", err)
	}

	for _, app := range apps {
		if !shouldMatch(appName) || appName == app.Name {
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
				log.Fatal("failed to generate manifest: ", err)
			}

			resources := map[string][]unstructured.Unstructured{}
			for _, manifest := range response.Manifests {
				resource := unstructured.Unstructured{}
				err = json.Unmarshal([]byte(manifest), &resource)
				errors.CheckError(err)

				kind := strings.ToLower(resource.GetKind())
				if !shouldMatch(resKind) || resKind == kind {
					if _, ok := resources[kind]; !ok {
						resources[kind] = make([]unstructured.Unstructured, 0)
					}
					resources[kind] = append(resources[kind], resource)
				}
			}
			kinds := make([]string, 0)
			for kind := range resources {
				kinds = append(kinds, kind)
			}
			sort.Strings(kinds)
			switch output {
			case "name":
				printNewline := true
				for _, kind := range kinds {
					if printNewline {
						printNewline = false
					} else {
						fmt.Println()
					}
					fmt.Println("NAME")
					for _, resource := range resources[kind] {
						fmt.Printf("%s/%s\n", kind, resource.GetName())
					}
				}
			case "json", "yaml":
				for _, kind := range kinds {
					argocmd.PrintResourceList(resources[kind], output, false)
				}

			default:
				errors.CheckError(fmt.Errorf("unknown output format: %s", output))
			}
		}
	}
}
