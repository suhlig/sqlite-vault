package backup

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/minio/minio-go/v7"
)

// ObjectStore abstracts storing a backup
type ObjectStore interface {
	Store(ctx context.Context, localPath, objectName string) (string, error)
}

// MinioStore wraps an S3-compatible client used to store backup objects in a bucket.
type MinioStore struct {
	client     *minio.Client
	bucketName string
}

// NewMinioStore initializes a Storage using the given S3-compatible endpoint, region, credentials and bucket.
func NewMinioStore(client *minio.Client, bucketName string) (*MinioStore, error) {
	return &MinioStore{
		client:     client,
		bucketName: bucketName,
	}, nil
}

// Store uploads the file at localPath to the bucket under objectName and returns the SHA-1 digest.
func (s MinioStore) Store(ctx context.Context, localPath, objectName string) (string, error) {
	f, err := os.Open(localPath)

	if err != nil {
		return "", fmt.Errorf("opening local file %q: %w", localPath, err)
	}

	defer func() {
		_ = f.Close()
	}()

	fi, err := f.Stat()

	if err != nil {
		return "", fmt.Errorf("statting local file %q: %w", localPath, err)
	}

	hash := sha1.New()
	tee := io.TeeReader(f, hash)

	_, err = s.client.PutObject(
		ctx,
		s.bucketName,
		objectName,
		tee,
		fi.Size(),
		minio.PutObjectOptions{
			ContentType: "application/octet-stream",
		},
	)

	if err != nil {
		return "", fmt.Errorf("uploading object %q to bucket %q: %w", objectName, s.bucketName, err)
	}

	sha1sum := hex.EncodeToString(hash.Sum(nil))

	return sha1sum, nil
}
