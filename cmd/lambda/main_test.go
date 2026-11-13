package main

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestPresignedURLHasNoImplicitChecksum(t *testing.T) {
	awsConfig := aws.Config{
		Region:                     "eu-central-1",
		Credentials:                credentials.NewStaticCredentialsProvider("test", "test", ""),
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
	}
	presigner := s3Presigner{client: s3.NewPresignClient(s3.NewFromConfig(awsConfig))}
	address, err := presigner.PresignPut(context.Background(), "test-bucket", "uploads/tisch/foto.jpg", "image/jpeg", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Has("x-amz-checksum-crc32") {
		t.Fatal("unerwartete implizite CRC32-Prüfsumme in der Upload-URL")
	}
	if parsed.Query().Get("X-Amz-Expires") != "300" {
		t.Fatalf("X-Amz-Expires = %q", parsed.Query().Get("X-Amz-Expires"))
	}
}
