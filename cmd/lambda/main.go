package main

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"weddingupload/internal/upload"
)

type s3Presigner struct {
	client *s3.PresignClient
}

func (p s3Presigner) PresignPut(ctx context.Context, bucket, key, contentType string, lifetime time.Duration) (string, error) {
	request, err := p.client.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}, func(options *s3.PresignOptions) {
		options.Expires = lifetime
	})
	if err != nil {
		return "", err
	}
	return request.URL, nil
}

func main() {
	ctx := context.Background()
	awsConfig, err := config.LoadDefaultConfig(ctx,
		config.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired),
	)
	if err != nil {
		log.Fatalf("AWS-Konfiguration laden: %v", err)
	}

	maxFileSize, err := strconv.ParseInt(environment("MAX_FILE_SIZE", "104857600"), 10, 64)
	if err != nil {
		log.Fatalf("MAX_FILE_SIZE lesen: %v", err)
	}

	service, err := upload.New(upload.Config{
		Bucket:         os.Getenv("PHOTO_BUCKET"),
		TokenMapBase64: os.Getenv("TOKEN_MAP_B64"),
		MaxFileSize:    maxFileSize,
		Presigner:      s3Presigner{client: s3.NewPresignClient(s3.NewFromConfig(awsConfig))},
	})
	if err != nil {
		log.Fatalf("Upload-Service initialisieren: %v", err)
	}

	lambda.Start(func(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
		response := service.Handle(ctx, request.Body, request.IsBase64Encoded)
		return events.APIGatewayProxyResponse{
			StatusCode: response.StatusCode,
			Headers:    response.Headers,
			Body:       response.Body,
		}, nil
	})
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
