package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	sqlitevault "github.com/suhlig/sqlite-vault/v2"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func main() {
	var (
		endpoint    = flag.String("endpoint", "", "S3 endpoint (e.g. s3.amazonaws.com)")
		bucket      = flag.String("bucket", "", "S3 bucket name")
		region      = flag.String("region", "", "S3 region")
		prefix      = flag.String("prefix", "", "Object prefix, e.g. myapp")
		canaryTable = flag.String("canary-table", "backup_canary", "Canary table name")
		maxAge      = flag.Duration("max-age", 26*time.Hour, "Maximum acceptable canary age")
		alias       = flag.String("alias", "daily-latest.alias", "Latest alias object name")
		timeout     = flag.Duration("timeout", 5*time.Minute, "Overall verification timeout")
		insecure    = flag.Bool("insecure", false, "Skip TLS verification")
	)

	flag.Parse()

	accessKey := os.Getenv(accessKeyEnv)
	secretKey := os.Getenv(secretKeyEnv)
	passphrase := os.Getenv(passphraseEnv)
	if accessKey == "" {
		fmt.Fprintf(os.Stderr, "%s is required\n", accessKeyEnv)
		os.Exit(1)
	}
	if secretKey == "" {
		fmt.Fprintf(os.Stderr, "%s is required\n", secretKeyEnv)
		os.Exit(1)
	}
	if passphrase == "" {
		fmt.Fprintf(os.Stderr, "%s is required\n", passphraseEnv)
		os.Exit(1)
	}

	if err := run(*endpoint, *bucket, *region, *prefix, accessKey, secretKey, passphrase, *canaryTable, *maxAge, *alias, *timeout, *insecure); err != nil {
		fmt.Fprintf(os.Stderr, "verification failed: %v\n", err)
		os.Exit(1)
	}
}

const (
	accessKeyEnv  = "SQLITE_VAULT_VERIFY_ACCESS_KEY"
	secretKeyEnv  = "SQLITE_VAULT_VERIFY_SECRET_KEY"
	passphraseEnv = "SQLITE_VAULT_VERIFY_PASSPHRASE"
)

func run(endpoint, bucket, region, prefix, accessKey, secretKey, passphrase, canaryTable string, maxAge time.Duration, alias string, timeout time.Duration, insecure bool) error {
	if endpoint == "" {
		return fmt.Errorf("-endpoint is required")
	}
	if bucket == "" {
		return fmt.Errorf("-bucket is required")
	}
	if prefix == "" {
		return fmt.Errorf("-prefix is required")
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	options := &minio.Options{
		Creds:     credentials.NewStaticV4(accessKey, secretKey, ""),
		Region:    region,
		Secure:    !insecure,
		Transport: transport,
	}

	client, err := minio.New(endpoint, options)
	if err != nil {
		return fmt.Errorf("creating minio client: %w", err)
	}

	store, err := sqlitevault.NewMinioStore(client, bucket)
	if err != nil {
		return fmt.Errorf("creating minio store: %w", err)
	}

	verifier := sqlitevault.NewVerifier(store, passphrase).
		WithCanary(canaryTable)

	slot, err := slotFromAlias(alias)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return verifier.VerifyLatest(ctx, prefix, slot, maxAge)
}

func slotFromAlias(alias string) (string, error) {
	alias = strings.TrimSpace(alias)
	if !strings.HasSuffix(alias, ".alias") {
		return "", fmt.Errorf("alias %q does not end with .alias", alias)
	}
	alias = strings.TrimSuffix(alias, ".alias")
	parts := strings.Split(alias, ".")
	if len(parts) == 0 {
		return "", fmt.Errorf("empty alias")
	}
	last := parts[len(parts)-1]
	if !strings.HasSuffix(last, "-latest") {
		return "", fmt.Errorf("alias %q does not end with -latest", alias)
	}
	slot := strings.TrimSuffix(last, "-latest")
	if slot == "" {
		return "", fmt.Errorf("could not determine slot from alias %q", alias)
	}
	return slot, nil
}
