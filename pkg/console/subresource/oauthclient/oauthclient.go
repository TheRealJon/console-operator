package oauthclient

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	oauthv1 "github.com/openshift/api/oauth/v1"

	"github.com/openshift/console-operator/pkg/api"
	"github.com/openshift/console-operator/pkg/console/subresource/util"
	"github.com/openshift/console-operator/pkg/crypto"
)

// registers the console on the oauth client as a valid application
func RegisterConsoleToOAuthClient(client *oauthv1.OAuthClient, host string, randomBits string) *oauthv1.OAuthClient {
	SetRedirectURI(client, host)
	// client.Secret = randomBits
	SetSecretString(client, randomBits)
	return client
}

// for ManagementState.Removed
// Console does not have create/delete priviledges on oauth clients, only update
func DeRegisterConsoleFromOAuthClient(client *oauthv1.OAuthClient) *oauthv1.OAuthClient {
	client.RedirectURIs = []string{}
	// changing the string to anything else will invalidate the client
	client.Secret = crypto.Random256BitsString()
	return client
}

func DefaultOauthClient() *oauthv1.OAuthClient {
	return Stub()
}

func Stub() *oauthv1.OAuthClient {
	// we cannot set an ownerRef on the OAuthClient as it is cluster scoped
	return &oauthv1.OAuthClient{
		ObjectMeta: metav1.ObjectMeta{
			Name: api.OAuthClientName,
		},
	}
}

func GetSecretString(client *oauthv1.OAuthClient) string {
	return client.Secret
}

func SetSecretString(client *oauthv1.OAuthClient, randomBits string) *oauthv1.OAuthClient {
	client.Secret = string(randomBits)
	return client
}

// we are the only application for this client
// in the future we may accept multiple routes
// for now, we can clobber the slice & reset the entire thing
func SetRedirectURI(client *oauthv1.OAuthClient, host string) *oauthv1.OAuthClient {
	client.RedirectURIs = []string{}
	client.RedirectURIs = append(client.RedirectURIs, util.HTTPS(host)+"/auth/callback")
	return client
}

func SetRedirectURIs(client *oauthv1.OAuthClient, uris []string) *oauthv1.OAuthClient {
	client.RedirectURIs = uris
	return client
}

func GetRedirectURIs(client *oauthv1.OAuthClient) []string {
	return client.RedirectURIs
}
