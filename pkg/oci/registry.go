package oci

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"

	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
	"oras.land/oras-go/v2/registry/remote/retry"
)

type RegistryOptions struct {
	PlainHTTP                bool
	InsecureSkipTLSVerify    bool
	CAFile                   string
	Username                 string
	Password                 string
	AccessToken              string
	RefreshToken             string
	DisableDockerCredentials bool
}

func newRepository(ref remoteReference, opts RegistryOptions) (*remote.Repository, error) {
	repo, err := remote.NewRepository(ref.repository)
	if err != nil {
		return nil, fmt.Errorf("create remote OCI repository %s: %w", ref.repository, err)
	}
	repo.PlainHTTP = opts.PlainHTTP

	httpClient, err := registryHTTPClient(opts)
	if err != nil {
		return nil, err
	}
	credential, err := credentialFunc(ref.registry, opts)
	if err != nil {
		return nil, err
	}
	repo.Client = &auth.Client{
		Client:     httpClient,
		Header:     auth.DefaultClient.Header.Clone(),
		Cache:      auth.NewCache(),
		Credential: credential,
	}

	return repo, nil
}

func registryHTTPClient(opts RegistryOptions) (*http.Client, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: opts.InsecureSkipTLSVerify, // #nosec G402 -- controlled by the explicit --insecure-skip-tls-verify registry option.
	}
	if opts.CAFile != "" {
		rootCAs, err := x509.SystemCertPool()
		if err != nil || rootCAs == nil {
			rootCAs = x509.NewCertPool()
		}

		caBytes, err := os.ReadFile(opts.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read OCI registry CA file %s: %w", opts.CAFile, err)
		}
		if ok := rootCAs.AppendCertsFromPEM(caBytes); !ok {
			return nil, fmt.Errorf("parse OCI registry CA file %s: no PEM certificates found", opts.CAFile)
		}
		tlsConfig.RootCAs = rootCAs
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	// Large registry blobs can fail mid-stream with HTTP/2 PROTOCOL_ERROR.
	// Request retries cannot recover a body that already started streaming, so
	// use HTTP/1.1 for registry requests, including redirected blob downloads.
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	return &http.Client{Transport: retry.NewTransport(transport)}, nil
}

func credentialFunc(registryHost string, opts RegistryOptions) (auth.CredentialFunc, error) {
	if opts.Username != "" || opts.Password != "" || opts.AccessToken != "" || opts.RefreshToken != "" {
		return auth.StaticCredential(registryHost, auth.Credential{
			Username:     opts.Username,
			Password:     opts.Password,
			AccessToken:  opts.AccessToken,
			RefreshToken: opts.RefreshToken,
		}), nil
	}
	if opts.DisableDockerCredentials {
		return nil, nil
	}

	store, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
	if err != nil {
		return nil, fmt.Errorf("load Docker OCI credentials: %w", err)
	}
	return credentials.Credential(store), nil
}
