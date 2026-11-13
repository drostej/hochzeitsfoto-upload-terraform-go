package upload

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type fakePresigner struct {
	bucket      string
	key         string
	contentType string
	lifetime    time.Duration
}

func (f *fakePresigner) PresignPut(_ context.Context, bucket, key, contentType string, lifetime time.Duration) (string, error) {
	f.bucket = bucket
	f.key = key
	f.contentType = contentType
	f.lifetime = lifetime
	return "https://example.invalid/presigned", nil
}

func testService(t *testing.T) (*Service, *fakePresigner) {
	t.Helper()
	tokenMap, _ := json.Marshal(map[string]string{"0123456789abcdef": "Tisch 01"})
	presigner := &fakePresigner{}
	service, err := New(Config{
		Bucket:         "foto-bucket",
		TokenMapBase64: base64.StdEncoding.EncodeToString(tokenMap),
		MaxFileSize:    100_000_000,
		Presigner:      presigner,
		Now:            func() time.Time { return time.Date(2026, 9, 12, 19, 30, 0, 0, time.UTC) },
		RandomID:       func() (string, error) { return "12345678-1234-4123-8123-123456789abc", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, presigner
}

func TestHandleCreatesPresignedUpload(t *testing.T) {
	service, presigner := testService(t)
	body := `{"token":"0123456789abcdef","files":[{"id":"0","name":"foto.JPG","type":"image/jpeg","size":1234}]}`
	response := service.Handle(context.Background(), body, false)

	if response.StatusCode != 200 {
		t.Fatalf("Status = %d, Body = %s", response.StatusCode, response.Body)
	}
	if presigner.key != "uploads/tisch-01/2026-09-12/12345678-1234-4123-8123-123456789abc.jpg" {
		t.Fatalf("unerwarteter Schlüssel: %s", presigner.key)
	}
	if presigner.contentType != "image/jpeg" || presigner.lifetime != 5*time.Minute {
		t.Fatalf("unerwartete Presign-Werte: %s, %s", presigner.contentType, presigner.lifetime)
	}
}

func TestHandleRejectsUnknownToken(t *testing.T) {
	service, _ := testService(t)
	response := service.Handle(context.Background(), `{"token":"falsch","files":[{"name":"foto.jpg","size":1}]}`, false)
	if response.StatusCode != 403 {
		t.Fatalf("Status = %d", response.StatusCode)
	}
}

func TestHandleInfersMOVContentType(t *testing.T) {
	service, presigner := testService(t)
	body := `{"token":"0123456789abcdef","files":[{"name":"clip.MOV","type":"","size":1234}]}`
	response := service.Handle(context.Background(), body, false)
	if response.StatusCode != 200 || presigner.contentType != "video/quicktime" {
		t.Fatalf("Status = %d, Typ = %s", response.StatusCode, presigner.contentType)
	}
}

func TestHandleLimitsBatchSize(t *testing.T) {
	service, _ := testService(t)
	files := make([]string, 21)
	for index := range files {
		files[index] = `{"name":"foto.jpg","type":"image/jpeg","size":100}`
	}
	body := `{"token":"0123456789abcdef","files":[` + strings.Join(files, ",") + `]}`
	response := service.Handle(context.Background(), body, false)
	if response.StatusCode != 400 {
		t.Fatalf("Status = %d", response.StatusCode)
	}
}
