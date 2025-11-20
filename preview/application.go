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
// Returns a value slice for consistency with ApplicationSet's generateApplications
func loadApplications(filename string) []argoappv1.Application {
	// Create empty FlagSet (required by ConstructApps)
	flags := pflag.NewFlagSet("", pflag.ContinueOnError)

	// Create empty AppOptions (no modifications to loaded YAML)
	appOpts := cmdutil.AppOptions{}

	// Call ConstructApps with minimal parameters
	appPointers, err := cmdutil.ConstructApps(
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

	// Convert pointer slice to value slice for consistency with generateApplications pattern
	apps := make([]argoappv1.Application, len(appPointers))
	for i, app := range appPointers {
		apps[i] = *app
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
			if !shouldMatch(appName) || appName == app.Name {
				fmt.Printf("application/%s\n", app.Name)
			}
		}
	case "json", "yaml":
		if shouldMatch(appName) {
			// Filter to specific app
			found := false
			for _, app := range apps {
				if app.Name == appName {
					found = true
					app.APIVersion = applicationAPIVersion
					app.Kind = applicationKind
					err := argocmd.PrintResource(app, output)
					if err != nil {
						log.Fatal(err)
					}
					return
				}
			}
			if !found {
				log.Fatalf("Application '%s' not found in %s", appName, filename)
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
	apps := loadApplications(filename)
	generateAndOutputManifests(apps, "", resKind, output)
}
