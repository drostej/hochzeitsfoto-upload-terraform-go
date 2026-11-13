package upload

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	maxFiles          = 20
	uploadURLLifetime = 5 * time.Minute
)

var typeToExtension = map[string]string{
	"image/jpeg":      "jpg",
	"image/png":       "png",
	"image/webp":      "webp",
	"image/heic":      "heic",
	"image/heif":      "heif",
	"video/mp4":       "mp4",
	"video/quicktime": "mov",
}

var extensionToType = map[string]string{
	"jpg":  "image/jpeg",
	"jpeg": "image/jpeg",
	"png":  "image/png",
	"webp": "image/webp",
	"heic": "image/heic",
	"heif": "image/heif",
	"mp4":  "video/mp4",
	"mov":  "video/quicktime",
}

type Presigner interface {
	PresignPut(context.Context, string, string, string, time.Duration) (string, error)
}

type Config struct {
	Bucket         string
	TokenMapBase64 string
	MaxFileSize    int64
	Presigner      Presigner
	Now            func() time.Time
	RandomID       func() (string, error)
}

type Service struct {
	bucket      string
	tokenMap    map[string]string
	maxFileSize int64
	presigner   Presigner
	now         func() time.Time
	randomID    func() (string, error)
}

type FileRequest struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

type Request struct {
	Token string        `json:"token"`
	Files []FileRequest `json:"files"`
}

type UploadTarget struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	ContentType string `json:"contentType"`
}

type Response struct {
	StatusCode int
	Headers    map[string]string
	Body       string
}

type validatedFile struct {
	id          string
	contentType string
	extension   string
}

type validationError struct {
	message string
}

func (e validationError) Error() string { return e.message }

func New(config Config) (*Service, error) {
	if config.Bucket == "" {
		return nil, errors.New("PHOTO_BUCKET fehlt")
	}
	if config.Presigner == nil {
		return nil, errors.New("Presigner fehlt")
	}
	if config.MaxFileSize <= 0 {
		return nil, errors.New("MAX_FILE_SIZE muss positiv sein")
	}

	decoded, err := base64.StdEncoding.DecodeString(config.TokenMapBase64)
	if err != nil {
		return nil, fmt.Errorf("TOKEN_MAP_B64 dekodieren: %w", err)
	}
	tokenMap := map[string]string{}
	if err := json.Unmarshal(decoded, &tokenMap); err != nil || len(tokenMap) == 0 {
		return nil, errors.New("TOKEN_MAP_B64 ist ungültig oder leer")
	}

	if config.Now == nil {
		config.Now = time.Now
	}
	if config.RandomID == nil {
		config.RandomID = randomUUID
	}

	return &Service{
		bucket:      config.Bucket,
		tokenMap:    tokenMap,
		maxFileSize: config.MaxFileSize,
		presigner:   config.Presigner,
		now:         config.Now,
		randomID:    config.RandomID,
	}, nil
}

func (s *Service) Handle(ctx context.Context, rawBody string, isBase64Encoded bool) Response {
	body := []byte(rawBody)
	if isBase64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(rawBody)
		if err != nil {
			return jsonResponse(400, map[string]any{"message": "Die Anfrage ist nicht gültig."})
		}
		body = decoded
	}

	var request Request
	if err := json.Unmarshal(body, &request); err != nil {
		return jsonResponse(400, map[string]any{"message": "Die Anfrage ist nicht gültig."})
	}

	tableSlug, ok := s.tokenMap[request.Token]
	if !ok {
		return jsonResponse(403, map[string]any{"message": "Dieser QR-Code ist nicht gültig."})
	}

	files, err := validateFiles(request.Files, s.maxFileSize)
	if err != nil {
		return jsonResponse(400, map[string]any{"message": err.Error()})
	}

	prefix := path.Join("uploads", safeSlug(tableSlug), s.now().UTC().Format("2006-01-02"))
	uploads := make([]UploadTarget, 0, len(files))
	for _, file := range files {
		id, err := s.randomID()
		if err != nil {
			return jsonResponse(500, map[string]any{"message": "Der Upload konnte nicht vorbereitet werden. Bitte erneut versuchen."})
		}
		key := path.Join(prefix, id+"."+file.extension)
		uploadURL, err := s.presigner.PresignPut(ctx, s.bucket, key, file.contentType, uploadURLLifetime)
		if err != nil {
			return jsonResponse(500, map[string]any{"message": "Der Upload konnte nicht vorbereitet werden. Bitte erneut versuchen."})
		}
		uploads = append(uploads, UploadTarget{ID: file.id, URL: uploadURL, ContentType: file.contentType})
	}

	return jsonResponse(200, map[string]any{"uploads": uploads})
}

func validateFiles(files []FileRequest, maxFileSize int64) ([]validatedFile, error) {
	if len(files) < 1 || len(files) > maxFiles {
		return nil, validationError{message: "Bitte 1 bis 20 Dateien auswählen."}
	}

	validated := make([]validatedFile, 0, len(files))
	for index, file := range files {
		if file.Size < 1 || file.Size > maxFileSize {
			return nil, validationError{message: fmt.Sprintf("Datei %d überschreitet die erlaubte Größe.", index+1)}
		}
		contentType := inferContentType(file)
		extension, ok := typeToExtension[contentType]
		if !ok {
			return nil, validationError{message: fmt.Sprintf("Dateityp von Datei %d wird nicht unterstützt.", index+1)}
		}
		id := file.ID
		if id == "" {
			id = strconv.Itoa(index)
		}
		validated = append(validated, validatedFile{id: id, contentType: contentType, extension: extension})
	}
	return validated, nil
}

func inferContentType(file FileRequest) string {
	supplied := strings.ToLower(strings.TrimSpace(file.Type))
	if _, ok := typeToExtension[supplied]; ok {
		return supplied
	}
	name := strings.ToLower(file.Name)
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 && dot+1 < len(name) {
		return extensionToType[name[dot+1:]]
	}
	return ""
}

func safeSlug(value string) string {
	value = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss").Replace(strings.ToLower(value))
	var result strings.Builder
	lastDash := false
	for _, character := range value {
		isLetterOrDigit := character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
		if isLetterOrDigit {
			result.WriteRune(character)
			lastDash = false
		} else if !lastDash && result.Len() > 0 {
			result.WriteByte('-')
			lastDash = true
		}
		if result.Len() >= 50 {
			break
		}
	}
	slug := strings.Trim(result.String(), "-")
	if slug == "" {
		return "gaeste"
	}
	return slug
}

func randomUUID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	hexID := hex.EncodeToString(bytes)
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexID[0:8], hexID[8:12], hexID[12:16], hexID[16:20], hexID[20:32]), nil
}

func jsonResponse(statusCode int, value any) Response {
	body, err := json.Marshal(value)
	if err != nil {
		body = []byte(`{"message":"Interner Fehler."}`)
		statusCode = 500
	}
	return Response{
		StatusCode: statusCode,
		Headers: map[string]string{
			"Content-Type":  "application/json; charset=utf-8",
			"Cache-Control": "no-store",
		},
		Body: string(body),
	}
}
