// Copyright © 2018 Microsoft Corporation and contributors
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/profiles/latest/resources/mgmt/subscriptions"
	"github.com/Azure/azure-sdk-for-go/services/resources/mgmt/2017-05-10/resources"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/adal"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/gobuffalo/buffalo/meta"
	"github.com/gobuffalo/pop"
	"github.com/joho/godotenv"
	"github.com/marstr/randname"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// clientID is used to identify this application during a Device Auth flow.
// We have deliberately spoofed as the Azure CLI 2.0 at least temporarily.
const deviceClientID = "04b07795-8ddb-461a-bbee-02f9e1bf7b46"

var provisionConfig = viper.New()

var userAgent string

// These constants define a parameter which allows control of the name of the site that is to be created.
const (
	SiteName           = "site-name"
	SiteShorthand      = "n"
	siteDefaultPrefix  = "buffalo-app"
	siteDefaultMessage = siteDefaultPrefix + "-<random>"
	siteUsage          = "The name of the site that will be deployed to Azure."
)

// These constants define a parameter which gives control over the Azure Resource Group that should be used to hold
// the created assets.
const (
	ResoureGroupName       = "resource-group"
	ResourceGroupShorthand = "g"
	ResourceGroupDefault   = "<site name>"
	resourceGroupUsage     = "The name of the Resource Group that should hold the resources created."
)

// These constants define a parameter which allows control over the Azure Region that should be used when creating a
// resource group. If the specified resource group already exists, its location is used and this parameter is discarded.
const (
	LocationName        = "location"
	LocationShorthand   = "l"
	LocationDefault     = "centralus"
	LocationDefaultText = "<resource group location>, or " + LocationDefault
	locationUsage       = "The Azure Region that should be used when creating a resource group."
)

// These constants define a parameter that allows control of the type of database to be provisioned. This is largely an
// escape hatch to use if Buffalo-Azure is incorrectly identifying the flavor of database to use after reading your
// application.
//
// Supported flavors:
//  - None
//  - Postgresql
//  - MySQL
const (
	DatabaseTypeName  = "db-type"
	DatabaseShorthand = "d"
	databaseUsage     = "The type of database to provision."
)

// These constants define a parameter which gives control of the name of the database to be connected to by the
// Buffalo application.
const (
	DatabaseNameName  = "db-name"
	databaseNameUsage = "The name of the database to be connected to by the Buffalo application once its deployed."
)

// These constants defeine a parameter which controls the password of the
const (
	DatabaseAdminName    = "db-admin"
	DatabaseAdminDefault = "buffaloAdmin"
	databaseAdminUsage   = "The user handle of the administrator account."
)

// These constants define a parameter which controls the password of the database provisioned. For marginal security,
// it is randomly generated each time `buffalo azure provision` is run. It will be visibile in the "connection strings"
// of your App Service.
const (
	DatabasePasswordName      = "db-password"
	DatabasePasswordShorthand = "w"
	DatabasePasswordDefault   = "<randomly generated>"
	databasePasswordUsage     = "The administrator password for the database created. It is recommended you read this from a file instead of typing it in from your terminal."
	DatabasePasswordEnvVar    = "BUFFALO_AZURE_DATABASE_PASSWORD"
)

// These constants define a parameter which allows control over the particular Azure cloud which should be used for
// deployment.
// Some examples of Azure environments by name include:
// - AzurePublicCloud (most popular)
// - AzureChinaCloud
// - AzureGermanCloud
// - AzureUSGovernmentCloud
const (
	EnvironmentName      = "az-env"
	EnvironmentShorthand = "a"
	EnvironmentDefault   = "AzurePublicCloud"
	environmentUsage     = "The Azure environment that will be targeted for deployment."
)

var environment azure.Environment

// These constants define a parameter which will control which container image is used to
const (
	// ImageName is the full parameter name of the argument that controls which container image will be used
	// when the Web App for Containers is provisioned.
	ImageName = "image"

	// ImageShorthand is the abbreviated means of using ImageName.
	ImageShorthand = "i"

	// ImageDefault is the container image that will be deployed if you didn't specify one.
	ImageDefault = "appsvc/sample-hello-world:latest"

	imageUsage = "The container image that defines this project."
)

// These constants define a parameter that allows control of the Azure Resource Management (ARM) template that should be
// used to provision infrastructure. This tool is not designed to deploy arbitrary ARM templates, rather this parameter
// is intended to give you the flexibility to lock to a known version of the gobuffalo quick start template, or tweak
// that template a little for your own usage.
//
// To prevent live-site incidents, a local copy of the default template is stored in this executable. If this parameter
// is NOT specified, this program will attempt to acquire the remote version of the default-template. Should that fail,
// the locally cached copy will be used. If the parameter is specified, this program will attempt to acquire the remote
// version. If that operation fails, the program does NOT use the cached template, and terminates with a non-zero exit
// status.
const (
	// TemplateName is the full parameter name of the argument providing a URL where the ARM template to bue used can
	// be found.
	TemplateName = "rm-template"

	// TemplateShorthand is the abbreviated means of using TemplateName.
	TemplateShorthand = "t"

	// TemplateDefault is the name of the Template to use if no value was provided.
	TemplateDefault = "./azuredeploy.json"

	// TemplateDefaultLink defines the link that will be used if no local rm-template is found, and a link wasn't
	// provided.
	TemplateDefaultLink = "https://aka.ms/buffalo-template"
	templateUsage       = "The Azure Resource Management template which specifies the resources to provision."
)

// These constants define a parameter which allows control of the ARM template parameters that should be used during
// deployment.
const (
	TemplateParametersName      = "rm-template-params"
	TemplateParametersShorthand = "p"
	TemplateParametersDefault   = "./azuredeploy.parameters.json"
	templateParametersUsage     = "The parameters that should be provided when creating a deployment."
)

// These constants define a parameter that Azure subscription to own the resources created.
//
// This can also be specified with the environment variable AZURE_SUBSCRIPTION_ID or AZ_SUBSCRIPTION_ID.
const (
	SubscriptionName      = "subscription-id"
	SubscriptionShorthand = "s"
	subscriptionUsage     = "The ID (in UUID format) of the Azure subscription which should host the provisioned resources."
)

// These constants define a parameter which will control the profile being used for the sake of connections.
const (
	ProfileName      = "buffalo-env"
	ProfileShorthand = "b"
	profileUsage     = "The buffalo environment that should be used."
)

// These constants define a parameter which allows specification of a Service Principal for authentication.
// This should always be used in tandem with `--client-secret`.
//
// This can also be specified with the environment variable AZURE_CLIENT_ID or AZ_CLIENT_ID.
//
// To learn more about getting started with Service Principals you can look here:
// - Using the Azure CLI 2.0: [https://docs.microsoft.com/en-us/cli/azure/create-an-azure-service-principal-azure-cli](https://docs.microsoft.com/en-us/cli/azure/create-an-azure-service-principal-azure-cli?toc=%2Fazure%2Fazure-resource-manager%2Ftoc.json&view=azure-cli-latest)
// - Using Azure PowerShell: [https://docs.microsoft.com/en-us/azure/azure-resource-manager/resource-group-authenticate-service-principal](https://docs.microsoft.com/en-us/azure/azure-resource-manager/resource-group-authenticate-service-principal?view=azure-cli-latest)
// - Using the Azure Portal: [https://docs.microsoft.com/en-us/azure/azure-resource-manager/resource-group-create-service-principal-portal](https://docs.microsoft.com/en-us/azure/azure-resource-manager/resource-group-create-service-principal-portal?view=azure-cli-latest)
const (
	ClientIDName  = "client-id"
	clientIDUsage = "The Application ID of the App Registration being used to authenticate."
)

// These constants define a parameter which allows specification of a Service Principal for authentication.
// This should always be used in tandem with `--client-id`.
//
// This can also be specified with the environment variable AZURE_CLIENT_SECRET or AZ_CLIENT_SECRET.
const (
	ClientSecretName  = "client-secret"
	clientSecretUsage = "The Key associated with the App Registration being used to authenticate."
)

// These constants define a parameter which provides the organization that should be used during authentication.
// Providing the tenant-id explicitly can help speed up execution, but by default this program will traverse all tenants
// available to the authenticated identity (service principal or user), and find the one containing the subscription
// provided. This traversal may involve several HTTP requests, and is therefore somewhat latent.
//
// This can also be specified with the environment variable AZURE_TENANT_ID or AZ_TENANT_ID.
const (
	TenantIDName = "tenant-id"
	tenantUsage  = "The ID (in form of a UUID) of the organization that the identity being used belongs to. "
)

// These constants define a parameter which forces this program to ignore any ambient Azure settings available as
// environment variables, and instead forces us to use Device Auth instead.
const (
	DeviceAuthName  = "use-device-auth"
	deviceAuthUsage = "Ignore --client-id and --client-secret, interactively authenticate instead."
)

// These constants define a parameter which toggles whether or not status information will be printed as this program
// executes.
const (
	VerboseName      = "verbose"
	VerboseShortname = "v"
	verboseUsage     = "Print out status information as this program executes."
)

// These constants define a parameter which toggles whether or not to save the template used for deployment to disk.
const (
	SkipTemplateCacheName      = "skip-template-cache"
	SkipTemplateCacheShorthand = "z"
	skipTemplateCacheUsage     = "After downloading the default template, do NOT save it in the working directory."
)

// These constants define a parameter which toggles whether or not to save the parameters used for deployment to disk.
const (
	SkipParameterCacheName      = "skip-parameters-cache"
	SkipParameterCacheShorthand = "y"
	skipParameterCacheUsage     = "After creating deploying the site, do NOT save the parameters that were used for deployment."
)

// These constants define a parameter which toggles whether or not a deployment is actually started.
const (
	SkipDeploymentName      = "skip-deployment"
	SkipDeploymentShorthand = "x"
	skipDeploymentUsage     = "Do not create an Azure deployment, do just the meta tasks."
)

// These constants define a parameter which controls the Docker registry that will be searched for the image provided.
const (
	DockerRegistryURLName  = "docker-registry-url"
	dockerRegistryURLUsage = "The URL for a private Docker Registry containing the image definition."
)

// These constants define a parameter which allows the username to be set for Docker authentication.
const (
	DockerRegistryUsernameName  = "docker-registry-username"
	dockerRegistryUsernameUsage = "The user handle to allow access to a private docker registry."
)

// These constants define a parameter which allows the password to be set for Docker authentication.
const (
	DockerRegistryPasswordName   = "docker-registry-password"
	dockerRegistryPasswordUsage  = "The user password to allow access to a private docker registry."
	DockerRegistryPasswordEnvVar = "BUFFALO_AZURE_DOCKER_PASSWORD"
)

// DockerAccess is an enum that contains either "private" or "public"
type DockerAccess string

// All (currently) possible value of DockerAccess
const (
	DockerAccessPublic  = "public"
	DockerAccessPrivate = "private"
)

// These constants define a parameter which informs buffalo-azure whether the registry is public or
// private.
const (
	DockerRegistryAccessName    = "docker-registry-access"
	DockerRegistryAccessDefault = DockerAccessPublic
	dockerRegistryAccessUsage   = "Specifies whether the Docker registry targeted is " + DockerAccessPublic + " or " + DockerAccessPrivate
)

var log = logrus.New()

var debug string

var deployParams *DeploymentParameters

// provisionCmd represents the provision command
var provisionCmd = &cobra.Command{
	Aliases: []string{"p"},
	Use:     "provision",
	Short:   "Create the infrastructure necessary to run a buffalo app on Azure.",
	Run: func(cmd *cobra.Command, args []string) {
		// Authenticate and setup clients
		subscriptionID := provisionConfig.GetString(SubscriptionName)
		clientID := provisionConfig.GetString(ClientIDName)
		clientSecret := provisionConfig.GetString(ClientSecretName)
		templateLocation := provisionConfig.GetString(TemplateName)
		image := provisionConfig.GetString(ImageName)
		databaseType := provisionConfig.GetString(DatabaseTypeName)
		databaseName := provisionConfig.GetString(DatabaseNameName)
		databaseAdmin := provisionConfig.GetString(DatabaseAdminName)
		siteName := provisionConfig.GetString(SiteName)

		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()

		var err error
		var auth autorest.Authorizer

		if !provisionConfig.GetBool(SkipDeploymentName) {
			auth, err = getAuthorizer(ctx, subscriptionID, clientID, clientSecret, provisionConfig.GetString(TenantIDName))
			if err != nil {
				log.Error("unable to authenticate: ", err)
				return
			}
		}

		log.Debug(TenantIDName+" selected: ", provisionConfig.GetString(TenantIDName))
		log.Debug(SubscriptionName+" selected: ", subscriptionID)
		log.Debug(TemplateName+" selected: ", templateLocation)
		log.Debug(DatabaseTypeName+" selected: ", databaseType)
		log.Debug(DatabaseNameName+" selected: ", databaseName)
		log.Debug(DatabaseAdminName+" selected: ", databaseAdmin)

		if usingDB, dbPassword := !strings.EqualFold(provisionConfig.GetString(DatabaseTypeName), "none"), provisionConfig.GetString(DatabasePasswordName); usingDB && dbPassword == DatabasePasswordDefault {
			newPass := randname.GenerateWithPrefix("MSFT+Buffalo-", 20)
			provisionConfig.Set(DatabasePasswordName, newPass)
			log.Debug("generated database password")
		} else if usingDB {
			log.Debug("using provided password")
		}

		var envMap map[string]string
		const envFileLoc = "./.env"
		if envMap, err = godotenv.Read(envFileLoc); err != nil {
			envMap = make(map[string]string, 1)
		}

		envMap[DatabasePasswordEnvVar] = provisionConfig.GetString(DatabasePasswordName)
		envMap[DockerRegistryPasswordEnvVar] = provisionConfig.GetString(DockerRegistryPasswordName)

		if err := godotenv.Write(envMap, envFileLoc); err == nil {
			log.Debugf("wrote passwords to %q", envFileLoc)
		} else {
			log.Errorf("unable to passwords to %q", envFileLoc)
		}

		log.Debug(ImageName+" selected: ", image)

		// Provision the necessary assets.

		deployParams.Parameters["name"] = DeploymentParameter{siteName}
		deployParams.Parameters["database"] = DeploymentParameter{strings.ToLower(databaseType)}
		deployParams.Parameters["databaseName"] = DeploymentParameter{databaseName}
		deployParams.Parameters["imageName"] = DeploymentParameter{image}
		deployParams.Parameters["databaseAdministratorLogin"] = DeploymentParameter{databaseAdmin}
		deployParams.Parameters["databaseAdministratorLoginPassword"] = DeploymentParameter{provisionConfig.GetString(DatabasePasswordName)}
		deployParams.Parameters["dockerRegistryAccess"] = DeploymentParameter{provisionConfig.GetString(DockerRegistryAccessName)}
		deployParams.Parameters["dockerRegistryServerURL"] = DeploymentParameter{provisionConfig.GetString(DockerRegistryURLName)}
		deployParams.Parameters["dockerRegistryServerUsername"] = DeploymentParameter{provisionConfig.GetString(DockerRegistryUsernameName)}
		deployParams.Parameters["dockerRegistryServerPassword"] = DeploymentParameter{provisionConfig.GetString(DockerRegistryPasswordName)}

		template, err := getDeploymentTemplate(ctx, templateLocation)
		if err != nil {
			log.Error("unable to fetch template: ", err)
			return
		}

		template.Parameters = deployParams.Parameters
		template.Mode = resources.Incremental

		deploymentResults := make(chan error)
		if provisionConfig.GetBool(SkipDeploymentName) {
			close(deploymentResults)
		} else {
			go func(errOut chan<- error) {
				defer close(errOut)
				groups := resources.NewGroupsClient(subscriptionID)
				groups.Authorizer = auth
				groups.AddToUserAgent(userAgent)

				// Assert the presence of the specified Resource Group
				rgName := provisionConfig.GetString(ResoureGroupName)
				created, err := insertResourceGroup(ctx, groups, rgName, provisionConfig.GetString(LocationName))
				if err != nil {
					log.Errorf("unable to fetch or create resource group %s: %v\n", rgName, err)
					errOut <- err
					return
				}
				if created {
					log.Info("created resource group: ", rgName)
				} else {
					log.Info("found resource group: ", rgName)
				}
				log.Debug("site name selected: ", siteName)

				pLink := portalLink(subscriptionID, rgName)

				log.Info("beginning deployment")
				if err := doDeployment(ctx, auth, subscriptionID, rgName, template); err == nil {
					log.Infof("Check on your new Resource Group in the Azure Portal: %s\nYour site will be available shortly at: https://%s.azurewebsites.net\n", pLink, siteName)
				} else {
					log.Warnf("unable to poll for completion progress, your assets may or may not have finished provisioning.\nCheck on their status in the portal: %s\nError: %v\n", pLink, err)
					errOut <- err
					return
				}
				log.Info("finished deployment")
			}(deploymentResults)
		}

		doCache := func(ctx context.Context, errOut chan<- error, contents interface{}, location, flavor string) {
			defer close(errOut)
			log.Info("caching ", flavor)
			err := cache(ctx, contents, location)
			if err != nil {
				log.Errorf("unable to cache file %s because: %v", location, err)
				errOut <- err
				return
			}
			log.Debugf("%s cached", flavor)
		}

		templateSaveResults, parameterSaveResults := make(chan error), make(chan error)
		if provisionConfig.GetBool(SkipTemplateCacheName) {
			close(templateSaveResults)
		} else {
			go doCache(ctx, templateSaveResults, template.Template, TemplateDefault, "template")
		}

		if provisionConfig.GetBool(SkipParameterCacheName) {
			close(parameterSaveResults)
		} else {
			go doCache(ctx, parameterSaveResults, stripPasswords(deployParams), TemplateParametersDefault, "parameters")
		}

		waitOnResults := func(ctx context.Context, results <-chan error) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case err := <-results:
				return err
			}
		}

		waitOnResults(ctx, templateSaveResults)
		waitOnResults(ctx, parameterSaveResults)
		waitOnResults(ctx, deploymentResults)
	},
	Args: func(cmd *cobra.Command, args []string) error {
		if provisionConfig.GetString(SubscriptionName) == "" {
			return fmt.Errorf("no value found for %q", SubscriptionName)
		}

		hasClientID := provisionConfig.GetString(ClientIDName) != ""
		hasClientSecret := provisionConfig.GetString(ClientSecretName) != ""

		if !hasClientSecret && !hasClientID {
			provisionConfig.Set(DeviceAuthName, true)
		} else if (hasClientID || hasClientSecret) && !(hasClientID && hasClientSecret) {
			return errors.New("--client-id and --client-secret must be specified together or not at all")
		}

		if rootConfig.GetBool(VerboseName) {
			rootConfig.Set(logOutputLevelName, logOutputLevelDebug)
		}

		if rawLevel := rootConfig.GetString(logOutputLevelName); rawLevel != "" {
			var level logrus.Level
			switch rawLevel {
			case logOutputLevelDebug:
				level = logrus.DebugLevel
			case logOutputLevelInfo:
				level = logrus.InfoLevel
			case logOutputLevelWarn:
				level = logrus.WarnLevel
			case logOutputLevelError:
				level = logrus.ErrorLevel
			case logOutputLevelFatal:
				level = logrus.FatalLevel
			case logOutputLevelPanic:
				level = logrus.PanicLevel
			default:
				return fmt.Errorf("unrecognized "+logOutputLevelName+": %s", rawLevel)
			}

			log.SetLevel(level)
		}

		if provisionConfig.GetString(TemplateName) == TemplateDefault {
			provisionConfig.Set(SkipTemplateCacheName, true)
		}

		if provisionConfig.GetString(LocationName) == LocationDefaultText {
			provisionConfig.SetDefault(LocationName, LocationDefault)
		}

		paramFile := provisionConfig.GetString(TemplateParametersName)
		p, err := loadFromParameterFile(paramFile)
		if err == nil {
			setDefaults(provisionConfig, p)
			deployParams = p
		} else if paramFile != TemplateParametersDefault {
			return fmt.Errorf("unable to load parameters file: %v", err)
		}

		nameGenerator := randname.Prefixed{
			Prefix:     siteDefaultPrefix + "-",
			Len:        10,
			Acceptable: append(randname.LowercaseAlphabet, randname.ArabicNumerals...),
		}

		if sn := provisionConfig.GetString(SiteName); sn == "" || sn == siteDefaultMessage {
			provisionConfig.Set(SiteName, nameGenerator.Generate())
		}

		if rgn := provisionConfig.GetString(ResoureGroupName); rgn == "" || rgn == ResourceGroupDefault {
			provisionConfig.Set(ResoureGroupName, provisionConfig.GetString(SiteName))
		}

		environment, err = azure.EnvironmentFromName(provisionConfig.GetString(EnvironmentName))
		if err != nil {
			return err
		}

		return nil
	},
}

func stripPasswords(original *DeploymentParameters) (copy *DeploymentParameters) {
	copy = NewDeploymentParameters()
	copy.Parameters = make(map[string]DeploymentParameter, len(original.Parameters))
	copy.ContentVersion = original.ContentVersion
	copy.Schema = original.Schema

	for k, v := range original.Parameters {
		copy.Parameters[k] = v
	}
	delete(copy.Parameters, "databaseAdministratorLoginPassword")
	delete(copy.Parameters, "dockerRegistryServerPassword")
	return copy
}

func cache(ctx context.Context, contents interface{}, outputName string) error {
	if handle, err := os.Create(outputName); err == nil {
		defer handle.Close()

		enc := json.NewEncoder(handle)
		enc.SetIndent("", "  ")
		err = enc.Encode(contents)
		if err != nil {
			return err
		}
	} else {
		return err
	}
	return nil
}

func portalLink(subscriptionID, rgName string) string {
	return fmt.Sprintf("https://portal.azure.com/#resource/subscriptions/%s/resourceGroups/%s/overview", subscriptionID, rgName)
}

// insertResourceGroup checks for a Resource Groups's existence, if it is not found it creates that resource group. If
// that resource group exists, it leaves it alone.
func insertResourceGroup(ctx context.Context, groups resources.GroupsClient, name string, location string) (bool, error) {
	existenceResp, err := groups.CheckExistence(ctx, name)
	if err != nil {
		return false, err
	}

	switch existenceResp.StatusCode {
	case http.StatusNoContent:
		return false, nil
	case http.StatusNotFound:
		createResp, err := groups.CreateOrUpdate(ctx, name, resources.Group{
			Location: &location,
		})
		if err != nil {
			return false, err
		}

		if createResp.StatusCode == http.StatusCreated {
			return true, nil
		} else if createResp.StatusCode == http.StatusOK {
			return false, nil
		} else {
			return false, fmt.Errorf("unexpected status code %d during resource group creation", createResp.StatusCode)
		}
	default:
		return false, fmt.Errorf("unexpected status code %d during resource group existence check", existenceResp.StatusCode)
	}
}

func getAuthorizer(ctx context.Context, subscriptionID, clientID, clientSecret, tenantID string) (autorest.Authorizer, error) {
	const commonTenant = "common"

	if tenantID == "" {
		tenantID = commonTenant
	}

	config, err := adal.NewOAuthConfig(environment.ActiveDirectoryEndpoint, tenantID)
	if err != nil {
		return nil, err
	}

	if provisionConfig.GetBool(DeviceAuthName) {
		var intermediate *adal.Token

		client := &http.Client{}
		code, err := adal.InitiateDeviceAuth(
			client,
			*config,
			deviceClientID,
			environment.ResourceManagerEndpoint)
		if err != nil {
			return nil, err
		}
		fmt.Println(*code.Message)
		token, err := adal.WaitForUserCompletion(client, code)
		if err != nil {
			return nil, err
		}
		intermediate = token

		if tenantID == commonTenant {
			var final autorest.Authorizer
			tenantID, final, err = getTenant(ctx, intermediate, subscriptionID)
			if err != nil {
				return nil, err
			}

			return final, nil
		}
		return autorest.NewBearerAuthorizer(intermediate), nil
	}

	if tenantID == commonTenant {
		return nil, errors.New("tenant inference unsupported with Service Principal authentication")
	}

	auth, err := adal.NewServicePrincipalToken(
		*config,
		clientID,
		clientSecret,
		environment.ResourceManagerEndpoint)
	if err != nil {
		return nil, err
	}
	log.WithFields(logrus.Fields{"client": clientID}).Debug("service principal token created")
	return autorest.NewBearerAuthorizer(auth), nil
}

func getDatabaseFlavor(buffaloRoot, profile string) (string, string, error) {
	app := meta.New(buffaloRoot)
	if !app.WithPop {
		return "none", "", nil
	}

	conn, err := pop.Connect(profile)
	if err != nil {
		return "", "", err
	}

	return conn.Dialect.Name(), conn.Dialect.Details().Database, nil
}

func doDeployment(ctx context.Context, authorizer autorest.Authorizer, subscriptionID, resourceGroup string, properties *resources.DeploymentProperties) (err error) {
	deployments := resources.NewDeploymentsClient(subscriptionID)
	deployments.Authorizer = authorizer
	deployments.AddToUserAgent(userAgent)

	fut, err := deployments.CreateOrUpdate(ctx, resourceGroup, siteDefaultPrefix, resources.Deployment{Properties: properties})
	if err != nil {
		return
	}

	err = fut.WaitForCompletion(ctx, deployments.Client)
	return
}

func getDeploymentTemplate(ctx context.Context, raw string) (*resources.DeploymentProperties, error) {
	if isSupportedLink(raw) {
		buf := bytes.NewBuffer([]byte{})

		err := downloadTemplate(ctx, buf, raw)
		if err != nil {
			return nil, err
		}

		return &resources.DeploymentProperties{
			Template: json.RawMessage(buf.Bytes()),
		}, nil
	}

	handle, err := os.Open(raw)
	if err != nil {
		return nil, err
	}

	contents, err := ioutil.ReadAll(handle)
	if err != nil {
		return nil, err
	}

	return &resources.DeploymentProperties{
		Template: json.RawMessage(contents),
	}, nil
}

var redirectCodes = map[int]struct{}{
	http.StatusMovedPermanently:  {},
	http.StatusPermanentRedirect: {},
	http.StatusTemporaryRedirect: {},
	http.StatusSeeOther:          {},
	http.StatusFound:             {},
}

var temporaryFailureCodes = map[int]struct{}{
	http.StatusTooManyRequests: {},
	http.StatusGatewayTimeout:  {},
	http.StatusRequestTimeout:  {},
}

var acceptedCodes = map[int]struct{}{
	http.StatusOK: {},
}

func downloadTemplate(ctx context.Context, dest io.Writer, src string) error {
	const maxRedirects = 5
	const maxRetries = 3
	var download func(context.Context, io.Writer, string, uint) error

	log.Debug("downloading template: ", src)

	download = func(ctx context.Context, dest io.Writer, src string, depth uint) (err error) {
		if depth > maxRedirects {
			return errors.New("too many redirects")
		}

		for attempt := 0; attempt < maxRetries; attempt++ {
			var req *http.Request
			var resp *http.Response

			req, err = http.NewRequest(http.MethodGet, src, nil)
			if err != nil {
				return
			}
			req = req.WithContext(ctx)

			resp, err = http.DefaultClient.Do(req)
			if err != nil {
				return
			}

			if _, ok := acceptedCodes[resp.StatusCode]; ok {
				_, err = io.Copy(dest, resp.Body)
				return
			}

			statusCodeLogger := log.WithFields(logrus.Fields{"status-code": resp.StatusCode})

			if _, ok := redirectCodes[resp.StatusCode]; ok {
				loc := resp.Header.Get("Location")
				statusCodeLogger.WithFields(logrus.Fields{"location": loc}).Debug("following HTTP redirect")
				return download(ctx, dest, loc, depth+1)
			}

			if _, ok := temporaryFailureCodes[resp.StatusCode]; ok {
				statusCodeLogger.Debug("recoverable HTTP failure, retrying.")
				continue
			}

			err = fmt.Errorf("unexpected status code: %d", resp.StatusCode)
			return
		}

		err = errors.New("too many attempts")
		return
	}

	return download(ctx, dest, src, 1)
}

func getTenant(ctx context.Context, common *adal.Token, subscription string) (string, autorest.Authorizer, error) {
	tenants := subscriptions.NewTenantsClient()
	tenants.Authorizer = autorest.NewBearerAuthorizer(common)

	var err error
	var tenantList subscriptions.TenantListResultIterator

	subscriptionClient := subscriptions.NewClient()

	log.WithFields(logrus.Fields{"subscription": subscription}).Info("using authorization to infer tenant")

	for tenantList, err = tenants.ListComplete(ctx); err == nil && tenantList.NotDone(); err = tenantList.Next() {
		var subscriptionList subscriptions.ListResultIterator
		currentTenant := *tenantList.Value().TenantID
		currentConfig, err := adal.NewOAuthConfig(environment.ActiveDirectoryEndpoint, currentTenant)
		if err != nil {
			return "", nil, err
		}
		currentAuth, err := adal.NewServicePrincipalTokenFromManualToken(*currentConfig, deviceClientID, environment.ResourceManagerEndpoint, adal.Token{
			RefreshToken: common.RefreshToken,
		})
		if err != nil {
			return "", nil, err
		}
		subscriptionClient.Authorizer = autorest.NewBearerAuthorizer(currentAuth)

		for subscriptionList, err = subscriptionClient.ListComplete(ctx); err == nil && subscriptionList.NotDone(); err = subscriptionList.Next() {
			if currentSub := subscriptionList.Value(); currentSub.SubscriptionID != nil && strings.EqualFold(*currentSub.SubscriptionID, subscription) {
				provisionConfig.Set(TenantIDName, *tenantList.Value().TenantID)
				return *tenantList.Value().TenantID, subscriptionClient.Authorizer, nil
			}
		}
	}
	if err != nil {
		return "", nil, err
	}

	return "", nil, fmt.Errorf("unable to find subscription: %s", subscription)
}

var normalizeScheme = strings.ToLower
var supportedLinkSchemes = map[string]struct{}{
	normalizeScheme("http"):  {},
	normalizeScheme("https"): {},
}

// isSupportedLink interrogates a string to decide if it is a RequestURI that is supported by the Azure template engine
// as defined here:
// https://docs.microsoft.com/en-us/azure/azure-resource-manager/resource-group-linked-templates#external-template-and-external-parameters
func isSupportedLink(subject string) bool {
	parsed, err := url.ParseRequestURI(subject)
	if err != nil {
		return false
	}

	_, ok := supportedLinkSchemes[normalizeScheme(parsed.Scheme)]

	return ok
}

func setDefaults(conf *viper.Viper, params *DeploymentParameters) {
	if name, ok := params.Parameters["name"]; ok {
		conf.SetDefault(SiteName, name.Value)
	}

	if database, ok := params.Parameters["database"]; ok {
		conf.SetDefault(DatabaseTypeName, database.Value)
	}

	if databaseName, ok := params.Parameters["databaseName"]; ok {
		conf.SetDefault(DatabaseNameName, databaseName.Value)
	}

	if image, ok := params.Parameters["imageName"]; ok {
		conf.SetDefault(ImageName, image.Value)
	}

	if dbAdmin, ok := params.Parameters["databaseAdministratorLogin"]; ok {
		conf.SetDefault(DatabaseAdminName, dbAdmin.Value)
	}

	if dockerAccess, ok := params.Parameters["dockerRegistryAccess"]; ok {
		conf.SetDefault(DockerRegistryAccessName, dockerAccess.Value)
	}

	if dockerURL, ok := params.Parameters["dockerRegistryServerURL"]; ok {
		conf.SetDefault(DockerRegistryURLName, dockerURL.Value)
	}

	if dockerUsername, ok := params.Parameters["dockerRegistryServerUsername"]; ok {
		conf.SetDefault(DockerRegistryUsernameName, dockerUsername.Value)
	}
}

func loadFromParameterFile(paramFile string) (*DeploymentParameters, error) {
	loaded := NewDeploymentParameters()
	if _, err := os.Stat(TemplateParametersDefault); err == nil {
		provisionConfig.SetDefault(TemplateParametersName, TemplateParametersDefault)
		if handle, err := os.Open(provisionConfig.GetString(TemplateParametersName)); err == nil {
			dec := json.NewDecoder(handle)
			err = dec.Decode(loaded)
			if err != nil {
				return nil, err
			}
		}
	} else {
		return nil, err
	}
	return loaded, nil
}

func init() {
	const redactedMessage = "[redacted]"
	azureCmd.AddCommand(provisionCmd)

	godotenv.Load()

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// provisionCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// provisionCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")

	provisionConfig.BindEnv(SubscriptionName, "AZURE_SUBSCRIPTION_ID", "AZ_SUBSCRIPTION_ID")
	provisionConfig.BindEnv(ClientIDName, "AZURE_CLIENT_ID", "AZ_CLIENT_ID")
	provisionConfig.BindEnv(ClientSecretName, "AZURE_CLIENT_SECRET", "AZ_CLIENT_SECRET")
	provisionConfig.BindEnv(TenantIDName, "AZURE_TENANT_ID", "AZ_TENANT_ID")
	provisionConfig.BindEnv(EnvironmentName, "AZURE_ENVIRONMENT", "AZ_ENVIRONMENT")
	provisionConfig.BindEnv(ProfileName, "GO_ENV")
	provisionConfig.BindEnv(DatabasePasswordName, DatabasePasswordEnvVar, "BUFFALO_AZURE_DB_PASSWORD", "BUFFALO_AZ_DATABASE_PASSWORD", "BUFFALO_AZ_DB_PASSWORD")
	provisionConfig.BindEnv(DockerRegistryPasswordName, DockerRegistryPasswordEnvVar)

	provisionConfig.SetDefault(ProfileName, "development")

	if dialect, dbname, err := getDatabaseFlavor(".", provisionConfig.GetString(ProfileName)); err == nil {
		provisionConfig.SetDefault(DatabaseTypeName, dialect)
		provisionConfig.SetDefault(DatabaseNameName, dbname)
	} else {
		provisionConfig.SetDefault(DatabaseTypeName, "none")
		log.WithFields(logrus.Fields{"error": err}).Warn("unable to parse buffalo application for db dialect.")
	}

	provisionConfig.SetDefault(DatabaseAdminName, DatabaseAdminDefault)
	provisionConfig.SetDefault(ImageName, ImageDefault)
	provisionConfig.SetDefault(EnvironmentName, EnvironmentDefault)
	provisionConfig.SetDefault(ResoureGroupName, ResourceGroupDefault)
	provisionConfig.SetDefault(LocationName, LocationDefaultText)
	provisionConfig.SetDefault(SiteName, siteDefaultMessage)
	provisionConfig.SetDefault(TemplateParametersName, TemplateParametersDefault)
	provisionConfig.SetDefault(DockerRegistryAccessName, DockerRegistryAccessDefault)

	var sanitizedClientSecret string
	if rawSecret := provisionConfig.GetString(ClientSecretName); rawSecret != "" {
		const safeCharCount = 10
		if len(rawSecret) > safeCharCount {
			sanitizedClientSecret = fmt.Sprintf("...%s", rawSecret[len(rawSecret)-safeCharCount:])
		} else {
			sanitizedClientSecret = redactedMessage
		}
	}

	if _, err := os.Stat(TemplateDefault); err == nil {
		provisionConfig.SetDefault(TemplateName, TemplateDefault)
	} else {
		provisionConfig.SetDefault(TemplateName, TemplateDefaultLink)
	}

	if p, err := loadFromParameterFile(provisionConfig.GetString(TemplateParametersName)); err == nil {
		setDefaults(provisionConfig, p)
		deployParams = p
	} else {
		deployParams = NewDeploymentParameters()
	}

	dbPassText := provisionConfig.GetString(DatabasePasswordName)
	if dbPassText == "" {
		dbPassText = DatabasePasswordDefault
		provisionConfig.SetDefault(DatabasePasswordName, DatabasePasswordDefault)
	} else {
		dbPassText = redactedMessage
	}

	dockerPassText := provisionConfig.GetString(DockerRegistryPasswordName)
	if dockerPassText != "" {
		dockerPassText = redactedMessage
	}

	provisionCmd.Flags().StringP(ImageName, ImageShorthand, provisionConfig.GetString(ImageName), imageUsage)
	provisionCmd.Flags().StringP(TemplateName, TemplateShorthand, provisionConfig.GetString(TemplateName), templateUsage)
	provisionCmd.Flags().StringP(SubscriptionName, SubscriptionShorthand, provisionConfig.GetString(SubscriptionName), subscriptionUsage)
	provisionCmd.Flags().String(ClientIDName, provisionConfig.GetString(ClientIDName), clientIDUsage)
	provisionCmd.Flags().String(ClientSecretName, sanitizedClientSecret, clientSecretUsage)
	provisionCmd.Flags().Bool(DeviceAuthName, false, deviceAuthUsage)
	provisionCmd.Flags().String(TenantIDName, provisionConfig.GetString(TenantIDName), tenantUsage)
	provisionCmd.Flags().StringP(EnvironmentName, EnvironmentShorthand, provisionConfig.GetString(EnvironmentName), environmentUsage)
	provisionCmd.Flags().String(DatabaseNameName, provisionConfig.GetString(DatabaseNameName), databaseNameUsage)
	provisionCmd.Flags().StringP(DatabaseTypeName, DatabaseShorthand, provisionConfig.GetString(DatabaseTypeName), databaseUsage)
	provisionCmd.Flags().StringP(ResoureGroupName, ResourceGroupShorthand, provisionConfig.GetString(ResoureGroupName), resourceGroupUsage)
	provisionCmd.Flags().StringP(SiteName, SiteShorthand, provisionConfig.GetString(SiteName), siteUsage)
	provisionCmd.Flags().StringP(LocationName, LocationShorthand, provisionConfig.GetString(LocationName), locationUsage)
	provisionCmd.Flags().BoolP(SkipTemplateCacheName, SkipTemplateCacheShorthand, false, skipTemplateCacheUsage)
	provisionCmd.Flags().BoolP(SkipParameterCacheName, SkipParameterCacheShorthand, false, skipParameterCacheUsage)
	provisionCmd.Flags().BoolP(SkipDeploymentName, SkipDeploymentShorthand, false, skipDeploymentUsage)
	provisionCmd.Flags().StringP(DatabasePasswordName, DatabasePasswordShorthand, dbPassText, databasePasswordUsage)
	provisionCmd.Flags().String(DatabaseAdminName, provisionConfig.GetString(DatabaseAdminName), databaseAdminUsage)
	provisionCmd.Flags().StringP(TemplateParametersName, TemplateParametersShorthand, provisionConfig.GetString(TemplateParametersName), templateParametersUsage)
	provisionCmd.Flags().String(DockerRegistryAccessName, provisionConfig.GetString(DockerRegistryAccessName), dockerRegistryAccessUsage)
	provisionCmd.Flags().String(DockerRegistryURLName, provisionConfig.GetString(DockerRegistryURLName), dockerRegistryURLUsage)
	provisionCmd.Flags().String(DockerRegistryUsernameName, provisionConfig.GetString(DockerRegistryUsernameName), dockerRegistryUsernameUsage)
	provisionCmd.Flags().String(DockerRegistryPasswordName, dockerPassText, dockerRegistryPasswordUsage)

	provisionConfig.BindPFlags(provisionCmd.Flags())

	userAgentBuilder := bytes.NewBufferString("buffalo-azure")
	if version != "" {
		userAgentBuilder.WriteRune('/')
		userAgentBuilder.WriteString(version)
	}
	userAgent = userAgentBuilder.String()
}
