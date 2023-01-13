package oauthclient

import (
	oauthv1 "github.com/openshift/api/oauth/v1"

	"github.com/openshift/console-operator/pkg/console/assets"
)

func DefaultManagedClusterOauthClient(secret string, redirectUris []string) *oauthv1.OAuthClient {
	client := ManagedClusterOAuthClientStub()
	SetRedirectURIs(client, redirectUris)
	SetSecretString(client, secret)
	return client
}

func ManagedClusterOAuthClientStub() *oauthv1.OAuthClient {
	return ReadOAuthClientV1OrDie(assets.MustAsset("oauth/console-managed-cluster-oauth-client.yaml"))
}
