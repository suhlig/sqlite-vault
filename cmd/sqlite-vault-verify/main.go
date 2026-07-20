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

	sqlitevault "github.com/suhlig/sqlite-vault"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func main() {
	var (
		endpoint       = flag.String("endpoint", "", "S3 endpoint (e.g. s3.amazonaws.com)")
		bucket         = flag.String("bucket", "", "S3 bucket name")
		region         = flag.String("region", "", "S3 region")
		prefix         = flag.String("prefix", "", "Object prefix, e.g. myapp")
		accessKeyFile  = flag.String("access-key-file", "", "Path to file containing S3 access key")
		secretKeyFile  = flag.String("secret-key-file", "", "Path to file containing S3 secret key")
		passphraseFile = flag.String("passphrase-file", "", "Path to file containing age passphrase")
		canaryTable    = flag.String("canary-table", "backup_canary", "Canary table name")
		maxAge         = flag.Duration("max-age", 26*time.Hour, "Maximum acceptable canary age")
		alias          = flag.String("alias", "daily-latest.alias", "Latest alias object name")
		timeout        = flag.Duration("timeout", 5*time.Minute, "Overall verification timeout")
		insecure       = flag.Bool("insecure", false, "Skip TLS verification")
	)

	flag.Parse()

	if err := run(*endpoint, *bucket, *region, *prefix, *accessKeyFile, *secretKeyFile, *passphraseFile, *canaryTable, *maxAge, *alias, *timeout, *insecure); err != nil {
		fmt.Fprintf(os.Stderr, "verification failed: %v\n", err)
		os.Exit(1)
	}
}

func run(endpoint, bucket, region, prefix, accessKeyFile, secretKeyFile, passphraseFile, canaryTable string, maxAge time.Duration, alias string, timeout time.Duration, insecure bool) error {
	if endpoint == "" {
		return fmt.Errorf("-endpoint is required")
	}
	if bucket == "" {
		return fmt.Errorf("-bucket is required")
	}
	if prefix == "" {
		return fmt.Errorf("-prefix is required")
	}
	if accessKeyFile == "" {
		return fmt.Errorf("-access-key-file is required")
	}
	if secretKeyFile == "" {
		return fmt.Errorf("-secret-key-file is required")
	}
	if passphraseFile == "" {
		return fmt.Errorf("-passphrase-file is required")
	}

	accessKey, err := readSecretFile(accessKeyFile)
	if err != nil {
		return fmt.Errorf("reading access key: %w", err)
	}

	secretKey, err := readSecretFile(secretKeyFile)
	if err != nil {
		return fmt.Errorf("reading secret key: %w", err)
	}

	passphrase, err := readSecretFile(passphraseFile)
	if err != nil {
		return fmt.Errorf("reading passphrase: %w", err)
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

func readSecretFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
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
