// Package photo re-encodes meal photos and stores them in S3 (ADR 0007).
package photo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Limits of an uploaded photo (§5.1, §6.1).
const (
	MaxBytes       = 2 << 20
	MaxLongestSide = 1600
	Quality        = 85
	PresignTTL     = 15 * time.Minute
)

// ErrInvalid means the upload is not a usable JPEG.
var ErrInvalid = errors.New("invalid photo")

// Processed is a re-encoded photo. Re-encoding drops all metadata (EXIF, GPS).
type Processed struct {
	JPEG   []byte
	Width  int
	Height int
}

// Process checks and re-encodes an uploaded JPEG.
func Process(data []byte) (Processed, error) {
	if len(data) > MaxBytes {
		return Processed{}, fmt.Errorf("%w: %d bytes", ErrInvalid, len(data))
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "jpeg" {
		return Processed{}, fmt.Errorf("%w: not a JPEG", ErrInvalid)
	}
	if max(cfg.Width, cfg.Height) > MaxLongestSide {
		return Processed{}, fmt.Errorf("%w: %dx%d is larger than %d px", ErrInvalid, cfg.Width, cfg.Height, MaxLongestSide)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return Processed{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: Quality}); err != nil {
		return Processed{}, err
	}
	b := img.Bounds()
	return Processed{JPEG: buf.Bytes(), Width: b.Dx(), Height: b.Dy()}, nil
}

// Key returns the S3 key of a meal photo.
func Key(userID, photoID string) string {
	return "meal-photos/" + userID + "/" + photoID + ".jpg"
}

// Storage stores meal photos.
type Storage interface {
	Put(ctx context.Context, key string, jpeg []byte) error
	Delete(ctx context.Context, key string) error
	PresignGet(ctx context.Context, key string) (string, error)
}

// S3 is the S3 implementation of Storage.
type S3 struct {
	Bucket  string
	client  *s3.Client
	presign *s3.PresignClient
}

// NewS3 uses the default AWS credential chain (env AWS_ACCESS_KEY_ID and
// AWS_SECRET_ACCESS_KEY on the server).
func NewS3(ctx context.Context, region, bucket string) (*S3, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}
	c := s3.NewFromConfig(cfg)
	return &S3{Bucket: bucket, client: c, presign: s3.NewPresignClient(c)}, nil
}

// Put uploads a JPEG. The bucket encrypts with SSE-S3 by default.
func (s *S3) Put(ctx context.Context, key string, data []byte) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.Bucket), Key: aws.String(key), Body: bytes.NewReader(data),
		ContentType: aws.String("image/jpeg"), ContentLength: aws.Int64(int64(len(data))),
	})
	return err
}

// Delete removes an object. Deleting a missing object is not an error.
func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(key)})
	return err
}

// PresignGet returns a GET URL that is valid for 15 minutes.
func (s *S3) PresignGet(ctx context.Context, key string) (string, error) {
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(key)},
		s3.WithPresignExpires(PresignTTL))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}
