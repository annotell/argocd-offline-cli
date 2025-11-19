package preview

import (
	"fmt"

	argocmd "github.com/argoproj/argo-cd/v3/cmd/argocd/commands"
	cmdutil "github.com/argoproj/argo-cd/v3/cmd/util"
	argoappv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
)

// loadApplications loads Applications from a YAML file
// Uses ArgoCD's ConstructApps utility function with minimal parameters
func loadApplications(filename string) []*argoappv1.Application {
	// Create empty FlagSet (required by ConstructApps)
	flags := pflag.NewFlagSet("", pflag.ContinueOnError)

	// Create empty AppOptions (no modifications to loaded YAML)
	appOpts := cmdutil.AppOptions{}

	// Call ConstructApps with minimal parameters
	apps, err := cmdutil.ConstructApps(
		filename,   // fileURL - path to YAML file
		"",         // appName - deprecated, leave empty
		[]string{}, // labels - no additional labels
		[]string{}, // annotations - no additional annotations
		[]string{}, // args - no command arguments
		appOpts,    // appOpts - empty/default options
		flags,      // flags - empty flag set
	)
	if err != nil {
		log.Fatal("failed to construct Application: ", err)
	}

	return apps
}

// PreviewApplication outputs the Application spec(s)
func PreviewApplication(filename string, appName string, output string) {
	apps := loadApplications(filename)

	switch output {
	case "name":
		fmt.Println("NAME")
		for _, app := range apps {
			if shouldMatch(appName) && app.Name != appName {
				continue
			}
			fmt.Printf("application/%s\n", app.Name)
		}
	case "json", "yaml":
		if shouldMatch(appName) {
			// Filter to specific app
			for _, app := range apps {
				if app.Name == appName {
					app.TypeMeta.APIVersion = applicationAPIVersion
					app.TypeMeta.Kind = applicationKind
					err := argocmd.PrintResource(app, output)
					if err != nil {
						log.Fatal(err)
					}
					return
				}
			}
		} else {
			// Print all applications
			err := argocmd.PrintResourceList(apps, output, false)
			if err != nil {
				log.Fatal(err)
			}
		}
	default:
		log.Fatalf("Unknown output format: %s", output)
	}
}

// PreviewApplicationResources generates and outputs Kubernetes manifests
func PreviewApplicationResources(filename string, resKind string, output string) {
	appPointers := loadApplications(filename)
	// Convert pointer slice to value slice for generateAndOutputManifests
	apps := make([]argoappv1.Application, len(appPointers))
	for i, app := range appPointers {
		apps[i] = *app
	}
	// Reuse the existing manifest generation logic
	generateAndOutputManifests(apps, "", resKind, output)
}
