package main

import (
	"expvar"
	"io/ioutil"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"time"

	"crypto/x509"

	envstruct "code.cloudfoundry.org/go-envstruct"
	"code.cloudfoundry.org/log-cache/internal/auth"
	"code.cloudfoundry.org/log-cache/internal/metrics"
	"code.cloudfoundry.org/log-cache/internal/promql"
	. "code.cloudfoundry.org/log-cache/internal/proxy"
	logtls "code.cloudfoundry.org/log-cache/internal/tls"
	"code.cloudfoundry.org/log-cache/pkg/client"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	log := log.New(os.Stderr, "", log.LstdFlags)
	log.Print("Starting Log Cache CF Auth Reverse Proxy...")
	defer log.Print("Closing Log Cache CF Auth Reverse Proxy.")

	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("failed to load config: %s", err)
	}
	envstruct.WriteReport(cfg)

	metrics := metrics.New(expvar.NewMap("CFAuthProxy"))

	uaaClient := auth.NewUAAClient(
		cfg.UAA.Addr,
		cfg.UAA.ClientID,
		cfg.UAA.ClientSecret,
		buildUAAClient(cfg),
		metrics,
		log,
	)

	capiClient := auth.NewCAPIClient(
		cfg.CAPI.ExternalAddr,
		buildCAPIClient(cfg),
		metrics,
		log,
	)

	metaFetcher := client.NewClient(cfg.LogCacheGatewayAddr)

	middlewareProvider := auth.NewCFAuthMiddlewareProvider(
		uaaClient,
		capiClient,
		metaFetcher,
		promql.ExtractSourceIds,
		capiClient,
	)

	proxy := NewCFAuthProxy(
		cfg.LogCacheGatewayAddr,
		cfg.Addr,
		WithAuthMiddleware(middlewareProvider.Middleware),
	)
	proxy.Start()

	// health endpoints (pprof and expvar)
	log.Printf("Health: %s", http.ListenAndServe(cfg.HealthAddr, nil))
}

func buildUAAClient(cfg *Config) *http.Client {
	tlsConfig := logtls.NewBaseTLSConfig()
	tlsConfig.InsecureSkipVerify = cfg.SkipCertVerify

	tlsConfig.RootCAs = loadCA(cfg.UAA.CAPath)

	transport := &http.Transport{
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     tlsConfig,
		DisableKeepAlives:   true,
	}

	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: transport,
	}
}

func buildCAPIClient(cfg *Config) *http.Client {
	tlsConfig := logtls.NewBaseTLSConfig()
	tlsConfig.ServerName = cfg.CAPI.CommonName

	tlsConfig.RootCAs = loadCA(cfg.CAPI.CAPath)

	tlsConfig.InsecureSkipVerify = cfg.SkipCertVerify
	transport := &http.Transport{
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     tlsConfig,
		DisableKeepAlives:   true,
	}

	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: transport,
	}
}

func loadCA(caCertPath string) *x509.CertPool {
	caCert, err := ioutil.ReadFile(caCertPath)
	if err != nil {
		log.Fatalf("failed to read CA certificate: %s", err)
	}

	certPool := x509.NewCertPool()
	ok := certPool.AppendCertsFromPEM(caCert)
	if !ok {
		log.Fatal("failed to parse CA certificate.")
	}

	return certPool
}
