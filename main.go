package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io/ioutil"
	"log"
	"os"

	"golang.org/x/oauth2/google"
	cloudres "google.golang.org/api/cloudresourcemanager/v1"
	iam "google.golang.org/api/iam/v1"
	pubsub "google.golang.org/api/pubsub/v1"
)

const (
	// Set this to false to keep the service account and topic around for manual inspection
	cleanup = true

	// These cannot already exist within the project
	testSubscriptionName   = "test-sub"
	testServiceAccountName = "test-sa"

	roleResourcePrefix    = "roles/"
	projectResourcePrefix = "projects/"
	pubsubRolePrefix      = "pubsub."
	topicPrefix           = "topics/"
	subscriptionPrefix    = "subscriptions/"
	saBindingPrefix       = "serviceAccount:"
)

type JWT struct {
	Type                    string `json:"type"`
	ProjectID               string `json:"project_id"`
	PrivateKeyID            string `json:"private_key_id"`
	PrivateKey              string `json:"private_key"`
	ClientEmail             string `json:"client_email"`
	ClientID                string `json:"client_id"`
	AuthURI                 string `json:"auth_uri"`
	TokenURI                string `json:"token_uri"`
	AuthProviderX509CertURL string `json:"auth_provider_x509_cert_url"`
	ClientX509CertURL       string `json:"client_x509_cert_url"`
}

func jwtFromFile(filePath string) (JWT, error) {
	jwt := new(JWT)

	raw, err := ioutil.ReadFile(filePath)
	if err != nil {
		return *jwt, err
	}
	err = json.Unmarshal(raw, &jwt)
	if err != nil {
		return *jwt, err
	}
	return *jwt, nil
}

func topicResourceName(projectID string, topicName string) string {
	return projectResourcePrefix + projectID + "/" + topicPrefix + topicName
}

func createTopic(topicsService *pubsub.ProjectsTopicsService, projectID string, name string) (*pubsub.Topic, error) {
	return topicsService.Create(topicResourceName(projectID, name), &pubsub.Topic{
		Name: name,
	}).Do()
}

func deleteTopic(topicsService *pubsub.ProjectsTopicsService, topic *pubsub.Topic) error {
	_, err := topicsService.Delete(topic.Name).Do()
	return err
}

func subscriptionResourceName(projectID string, subscriptionName string) string {
	return projectResourcePrefix + projectID + "/" + subscriptionPrefix + subscriptionName
}

func createSubscription(pubsubService *pubsub.Service, projectID string, subscriptionName string, topicName string) (*pubsub.Subscription, error) {
	return pubsubService.Projects.Subscriptions.Create(subscriptionResourceName(projectID, subscriptionName), &pubsub.Subscription{
		Topic: topicName,
	}).Do()
}

func deleteSubscription(pubsubService *pubsub.Service, projectID string, subscriptionName string, topicName string) error {
	_, err := pubsubService.Projects.Subscriptions.Delete(subscriptionResourceName(projectID, subscriptionName)).Do()
	return err
}

func createServiceAccount(iamServiceAccountsService *iam.ProjectsServiceAccountsService, projectID string, name string) (*iam.ServiceAccount, error) {
	var resourceName = projectResourcePrefix + projectID

	newSARequest := iam.CreateServiceAccountRequest{
		AccountId: name,
		ServiceAccount: &iam.ServiceAccount{
			DisplayName: name,
		},
	}

	return iamServiceAccountsService.Create(resourceName, &newSARequest).Do()
}

func deleteServiceAccount(iamServiceAccountsService *iam.ProjectsServiceAccountsService, sa *iam.ServiceAccount) error {
	_, err := iamServiceAccountsService.Delete(sa.Name).Do()
	return err
}

func createServiceAccountKey(iamKeysService *iam.ProjectsServiceAccountsKeysService, sa *iam.ServiceAccount) (*iam.ServiceAccountKey, error) {
	return iamKeysService.Create(sa.Name, &iam.CreateServiceAccountKeyRequest{}).Do()
}

func collapseBindings(bindings []*cloudres.Binding, role string) *cloudres.Binding {
	// seems not really necessary, but collapse the bindings into single role entries just in case.
	for _, binding := range bindings {
		if binding.Role == roleResourcePrefix+role {
			return binding
		}
	}
	return nil
}

func collapsePubsubBindings(bindings []*pubsub.Binding, role string) *pubsub.Binding {
	// seems not really necessary, but collapse the bindings into single role entries just in case.
	for _, binding := range bindings {
		if binding.Role == roleResourcePrefix+role {
			return binding
		}
	}
	return nil
}

func addMemberToPubSubPolicy(policy *pubsub.Policy, sa *iam.ServiceAccount, role string) {
	binding := collapsePubsubBindings(policy.Bindings, role)
	if binding != nil {
		// If the binding is not nil, append the service account to the list of members.
		binding.Members = append(binding.Members, saBindingPrefix+sa.Email)
	} else {
		// Otherwise, create a new binding with the member.
		binding = &pubsub.Binding{
			Members: []string{saBindingPrefix + sa.Email},
			Role:    roleResourcePrefix + role,
		}
		b := append(policy.Bindings, binding)
		policy.Bindings = b
	}
}

func addMemberToPolicy(policy *cloudres.Policy, sa *iam.ServiceAccount, role string) {
	binding := collapseBindings(policy.Bindings, role)
	if binding != nil {
		// If the binding is not nil, append the service account to the list of members.
		binding.Members = append(binding.Members, saBindingPrefix+sa.Email)
	} else {
		// Otherwise, create a new binding with the member.
		binding = &cloudres.Binding{
			Members: []string{saBindingPrefix + sa.Email},
			Role:    roleResourcePrefix + role,
		}
		b := append(policy.Bindings, binding)
		policy.Bindings = b
	}
}

func grantProjectPermission(projectsService *cloudres.ProjectsService, projectID string, sa *iam.ServiceAccount, role string) (*cloudres.Policy, error) {
	currPolicy, err := projectsService.GetIamPolicy(projectID, &cloudres.GetIamPolicyRequest{}).Do()
	if err != nil {
		return nil, err
	}
	// @todo role prefix?
	addMemberToPolicy(currPolicy, sa, role)
	return projectsService.SetIamPolicy(projectID, &cloudres.SetIamPolicyRequest{
		Policy: currPolicy,
	}).Do()
}

func grantProjectPermissions(projectsService *cloudres.ProjectsService, projectID string, sa *iam.ServiceAccount, roles []string) error {
	for _, role := range roles {
		_, err := grantProjectPermission(projectsService, projectID, sa, role)
		if err != nil {
			return err
		}
	}
	return nil
}

func grantPermissionOnTopic(topicsService *pubsub.ProjectsTopicsService, topic *pubsub.Topic, sa *iam.ServiceAccount, role string) (*pubsub.Policy, error) {
	currPolicy, err := topicsService.GetIamPolicy(topic.Name).Do()
	if err != nil {
		return nil, err
	}
	addMemberToPubSubPolicy(currPolicy, sa, pubsubRolePrefix+role)
	return topicsService.SetIamPolicy(topic.Name, &pubsub.SetIamPolicyRequest{
		Policy: currPolicy,
	}).Do()
}

func grantPermissionsOnTopic(topicsService *pubsub.ProjectsTopicsService, topic *pubsub.Topic, sa *iam.ServiceAccount, roles []string) error {
	for _, role := range roles {
		_, err := grantPermissionOnTopic(topicsService, topic, sa, role)
		if err != nil {
			return err
		}
	}
	return nil
}

func projectPermsToCheck() []string {
	return []string{
		"pubsub.subscriptions.create",
		"pubsub.subscriptions.list",
		"pubsub.topics.list",
	}
}

func topicPermsToCheck() []string {
	return []string{
		"pubsub.topics.get",
		"pubsub.topics.subscribe",
		"pubsub.topics.publish",
		"pubsub.topics.update",
		"pubsub.topics.attachSubscription",
		"pubsub.topics.delete",
		"pubsub.topics.getIamPolicy",
		"pubsub.topics.setIamPolicy",
	}
}

func getPermissions(projectsService *cloudres.ProjectsService, projectID string) (*cloudres.TestIamPermissionsResponse, error) {
	return projectsService.TestIamPermissions(projectResourcePrefix+projectID, &cloudres.TestIamPermissionsRequest{
		Permissions: projectPermsToCheck(),
	}).Do()
}

func getPermissionsOnTopic(topicsService *pubsub.ProjectsTopicsService, projectID string, topic *pubsub.Topic) (*pubsub.TestIamPermissionsResponse, error) {
	return topicsService.TestIamPermissions(topic.Name, &pubsub.TestIamPermissionsRequest{
		Permissions: topicPermsToCheck(),
	}).Do()
}

func main() {
	ctx := context.Background()

	// Get the location of the All-Access JWT
	jwtPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if jwtPath == "" {
		log.Fatalf("GOOGLE_APPLICATION_CREDENTIALS environment variable must be set to a full-access JWT credential file.")
	}

	// Extract ProjectID from the JWT
	jwt, err := jwtFromFile(jwtPath)
	if err != nil {
		log.Fatalf("Failed to load JWT from file: %s\n", err)
	}
	projectID := jwt.ProjectID

	// Create a new Default GCP Client
	// Uses credentials from GOOGLE_APPLICATION_CREDENTIALS environment variable
	gcpClient, err := google.DefaultClient(ctx, pubsub.PubsubScope, iam.CloudPlatformScope)
	if err != nil {
		log.Printf("Failed to get new Google Default Client: %s\n", err)
	}
	cloudResService, err := cloudres.New(gcpClient)
	if err != nil {
		log.Printf("Failed to create CloudRes Service: %s\n", err)
	}
	projectsService := cloudres.NewProjectsService(cloudResService)

	// Create a new IAM service
	iamService, err := iam.New(gcpClient)
	if err != nil {
		log.Printf("Failed to create IAM Service: %s\n", err)
	}
	iamServiceAccountsService := iam.NewProjectsServiceAccountsService(iamService)
	iamKeysService := iam.NewProjectsServiceAccountsKeysService(iamService)

	// Create a new service account
	serviceAccount, err := createServiceAccount(iamServiceAccountsService, projectID, testServiceAccountName)
	if err != nil {
		log.Printf("Failed to create service account: %s\n", err)
	}
	if cleanup {
		// Defer the deletion to clean up after we're done.
		defer deleteServiceAccount(iamServiceAccountsService, serviceAccount)
	}

	// Create a new Pubsub service
	pubsubService, err := pubsub.New(gcpClient)
	if err != nil {
		log.Printf("Failed to create PubSub Service: %s\n", err)
	}
	topicsService := pubsub.NewProjectsTopicsService(pubsubService)

	// Create a new topic
	topic, err := createTopic(topicsService, projectID, "test-topic")
	if err != nil {
		log.Printf("Failed to create topic: %s\n", err)
	}
	if cleanup {
		// Defer the deletion to clean up after we're done.
		defer deleteTopic(topicsService, topic)
	}

	// Grant Service Account permissions to the new topic
	err = grantPermissionsOnTopic(topicsService, topic, serviceAccount, []string{"subscriber"})
	if err != nil {
		log.Printf("Failed to grant permissions on topic: %s\n", err)
	}

	// Create a new key for the service account
	key, err := createServiceAccountKey(iamKeysService, serviceAccount)
	if err != nil {
		log.Printf("Failed to create service account key: %s\n", err)
	}

	decodedKey, err := base64.StdEncoding.DecodeString(key.PrivateKeyData)
	if err != nil {
		log.Printf("Failed to decode service account key: %s\n", err)
	}

	// Save the key to a temporary location
	file, err := ioutil.TempFile(os.TempDir(), "")
	if err != nil {
		log.Printf("Failed to create temp file: %s\n", err)
	}
	defer os.Remove(file.Name())
	err = ioutil.WriteFile(file.Name(), decodedKey, 0644)
	if err != nil {
		log.Printf("Failed to write key to file: %s\n", err)
	}

	// Set the GOOGLE_APPLICATION_CREDENTIALS environment variable to the new key
	err = os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", file.Name())
	if err != nil {
		log.Printf("Failed to set env var: %s\n", err)
	}

	// Create a new GCP Client using the new key
	newGCPClient, err := google.DefaultClient(ctx, pubsub.PubsubScope)
	if err != nil {
		log.Printf("Failed to get new Google Default Client: %s\n", err)
	}

	// Create a new Pubsub service
	newPubsubService, err := pubsub.New(newGCPClient)
	if err != nil {
		log.Printf("Failed to create PubSub Service: %s\n", err)
	}
	newTopicsService := pubsub.NewProjectsTopicsService(pubsubService)

	// Test permissions
	perms, err := getPermissionsOnTopic(newTopicsService, projectID, topic)
	if err != nil {
		// This fails.
		// Error 400: The IAM operation failed with a non-retryable error: Unknown error. See https://cloud.google.com/pubsub/access_control for more information., badRequest
		log.Printf("Failed to get permissions on topic: %s\n", err)
	}
	if perms != nil {
		for _, perm := range perms.Permissions {
			log.Printf("Allowed: %v\n", perm)
		}
	}

	// Create a new Pubsub subscription
	testSub, err := createSubscription(newPubsubService, projectID, testSubscriptionName, topic.Name)
	if err != nil {
		// This fails.
		// Error 403: User not authorized to perform this action., forbidden
		log.Printf("Failed to create subscription with only a topic role: %s\n", err)
	}
	if testSub != nil {
		if cleanup {
			defer deleteSubscription(newPubsubService, projectID, testSubscriptionName, topic.Name)
		}

		log.Println("SUCCESS using topic role!")
		log.Printf("%+v", testSub)
	}

	// Show that granting a global role does indeed give the Service Account the needed permission to subscribe

	newGCPClient, err = google.DefaultClient(ctx, pubsub.PubsubScope, cloudres.CloudPlatformScope)
	if err != nil {
		log.Printf("Failed to get new Google Default Client: %s\n", err)
	}
	newCloudResService, err := cloudres.New(newGCPClient)
	if err != nil {
		log.Printf("Failed to create CloudRes Service: %s\n", err)
	}
	newProjectsService := cloudres.NewProjectsService(newCloudResService)

	// Grant global subscriber role to service account
	err = grantProjectPermissions(projectsService, projectID, serviceAccount, []string{
		pubsubRolePrefix + "subscriber",
	})
	if err != nil {
		log.Printf("Failed to grant global permission: %s\n", err)
	}

	// Test permissions
	projectPerms, err := getPermissions(newProjectsService, projectID)
	if err != nil {
		log.Printf("Failed to get project permissions: %s\n", err)
	}
	if projectPerms != nil {
		for _, perm := range projectPerms.Permissions {
			log.Printf("Allowed: %v\n", perm)
		}
	}

	// Try again to create a new Pubsub subscription
	testSub2, err := createSubscription(newPubsubService, projectID, testSubscriptionName+"2", topic.Name)
	if err != nil {
		log.Printf("Failed to create subscription, even with global role: %s\n", err)
	}
	if testSub2 != nil {
		if cleanup {
			defer deleteSubscription(newPubsubService, projectID, testSubscriptionName+"2", topic.Name)
		}

		log.Println("SUCCESS using global role!")
		log.Printf("%+v", testSub2)
	}
}
