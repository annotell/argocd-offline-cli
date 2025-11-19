package preview

import (
	"fmt"
	"os"

	appsettemplate "github.com/argoproj/argo-cd/v3/applicationset/controllers/template"
	"github.com/argoproj/argo-cd/v3/applicationset/generators"
	appsetutils "github.com/argoproj/argo-cd/v3/applicationset/utils"
	argocmd "github.com/argoproj/argo-cd/v3/cmd/argocd/commands"
	cmdutil "github.com/argoproj/argo-cd/v3/cmd/util"
	argoappv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/argoproj/pkg/errors"
	"github.com/spf13/cobra"

	log "github.com/sirupsen/logrus"
)

var logger *log.Logger

func init() {
	cobra.OnInitialize(initConfig)
}

func initConfig() {
	// set warn log level to avoid standard argocd info logging
	os.Setenv("ARGOCD_LOG_LEVEL", "WARN")
	cmdutil.LogLevel = "WARN"
	logger = log.StandardLogger()
	logger.SetLevel(log.WarnLevel)
}

func PreviewApplications(filename string, appName string, output string) {
	apps := generateApplications(filename)
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
			for _, app := range apps {
				if appName == app.Name {
					app.TypeMeta.APIVersion = applicationAPIVersion
					app.TypeMeta.Kind = applicationKind
					argocmd.PrintResource(app, output)
					break
				}
			}
		} else {
			argocmd.PrintResourceList(apps, output, false)
		}
	default:
		errors.CheckError(fmt.Errorf("unknown output format: %s", output))
	}
}

func PreviewResources(filename string, appName string, resKind string, output string) {
	apps := generateApplications(filename)
	generateAndOutputManifests(apps, appName, resKind, output)
}

func generateApplications(filename string) []argoappv1.Application {
	appSets, err := cmdutil.ConstructApplicationSet(filename)
	if err != nil {
		log.Fatal("failed to construct ApplicationSet: ", err)
	}
	if len(appSets) > 1 {
		log.Warnf("found %d ApplicationSets, only previewing the first entry", len(appSets))
	}
	appSet := appSets[0]
	appSetGenerators := getAppSetGenerators()
	apps, _, err := appsettemplate.GenerateApplications(log.NewEntry(logger), *appSet, appSetGenerators, &appsetutils.Render{}, nil)
	if err != nil {
		log.Fatal("failed to generate Application(s): ", err)
	}
	return apps
}

func getAppSetGenerators() map[string]generators.Generator {
	terminalGenerators := map[string]generators.Generator{
		"List": generators.NewListGenerator(),
	}
	nestedGenerators := map[string]generators.Generator{
		"List":   terminalGenerators["List"],
		"Matrix": generators.NewMatrixGenerator(terminalGenerators),
		"Merge":  generators.NewMergeGenerator(terminalGenerators),
	}
	topLevelGenerators := map[string]generators.Generator{
		"List":   terminalGenerators["List"],
		"Matrix": generators.NewMatrixGenerator(nestedGenerators),
		"Merge":  generators.NewMergeGenerator(nestedGenerators),
	}

	return topLevelGenerators
}
