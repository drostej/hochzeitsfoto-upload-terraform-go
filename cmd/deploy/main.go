package main

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image/color"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

type table struct {
	Name  string `json:"name"`
	Slug  string `json:"slug"`
	Token string `json:"token,omitempty"`
}

type appConfig struct {
	Title  string  `json:"title"`
	Intro  string  `json:"intro"`
	Tables []table `json:"tables"`
}

type terraformOutput struct {
	Value string `json:"value"`
}

func main() {
	projectName := flag.String("project", "hochzeitsfotos-tf", "Terraform-Projektname")
	region := flag.String("region", "eu-central-1", "AWS-Region")
	profile := flag.String("profile", "", "optionales AWS-CLI-Profil")
	prepareOnly := flag.Bool("prepare-only", false, "nur Lambda bauen und paketieren")
	flag.Parse()
	fatalIf(validateAWSConfiguration(*profile))

	root, err := findProjectRoot()
	fatalIf(err)

	// Konfiguration und Tokens laden/generieren
	configuration, tokenMapBase64, err := loadConfiguration(root)
	fatalIf(err)

	// Lambda testen, bauen und packen
	fatalIf(buildAndPackageLambda(root))
	if *prepareOnly {
		fmt.Printf("\nLambda-Paket erstellt: %s\n", filepath.Join(root, ".build", "lambda.zip"))
		return
	}

	// AWS-Infrastruktur mit Terraform deployen
	outputs, err := deployInfrastructure(root, *projectName, *region, *profile, tokenMapBase64)
	fatalIf(err)

	// Frontend-Dateien hochladen und CloudFront invalidieren
	fatalIf(uploadAndPublishFrontend(root, outputs, *profile, *region, configuration))

	// QR-Codes und CSV für Gäste generieren
	fatalIf(generateAndOutputQRCodes(root, outputs["website_url"], configuration.Tables))

	// Abschließende Informationen ausgeben
	fmt.Printf("\nFertig.\nUpload-Seite: %s\nFoto-Bucket: %s\nQR-Codes: %s\n",
		outputs["website_url"], outputs["photo_bucket_name"], filepath.Join(root, "output", "qr"))
}

// loadConfiguration lädt die Konfiguration, generiert/aktualisiert Tokens und encodiert sie für die Lambda-Umgebung.
func loadConfiguration(root string) (appConfig, string, error) {
	configPath := filepath.Join(root, "config", "tables.json")
	configuration, err := readConfig(configPath)
	if err != nil {
		return appConfig{}, "", err
	}

	if err := prepareTables(&configuration); err != nil {
		return appConfig{}, "", err
	}

	if err := writeJSON(configPath, configuration); err != nil {
		return appConfig{}, "", err
	}

	// Token-Map für Lambda erzeugen
	tokenMap := make(map[string]string, len(configuration.Tables))
	for _, table := range configuration.Tables {
		tokenMap[table.Token] = table.Slug
	}

	tokenJSON, err := json.Marshal(tokenMap)
	if err != nil {
		return appConfig{}, "", err
	}

	tokenMapBase64 := base64.StdEncoding.EncodeToString(tokenJSON)
	if len(tokenMapBase64) > 3500 {
		return appConfig{}, "", errors.New("zu viele Tische für die Lambda-Umgebungsvariable")
	}

	return configuration, tokenMapBase64, nil
}

// buildAndPackageLambda führt Tests aus und erzeugt das Lambda-ZIP-Paket für Linux/ARM64.
func buildAndPackageLambda(root string) error {
	if err := run(root, "go", []string{"test", "-buildvcs=false", "./..."}, "go test ./...", false, nil, nil); err != nil {
		return err
	}
	if err := buildLambda(root); err != nil {
		return err
	}
	return packageLambda(root)
}

// deployInfrastructure führt Terraform init und apply aus, liest die Ausgaben und validiert sie.
func deployInfrastructure(root, projectName, region, profile, tokenMapBase64 string) (map[string]string, error) {
	tfCommand, err := resolveTerraformCommand()
	if err != nil {
		return nil, err
	}

	infraDirectory := filepath.Join(root, "infra")

	// Terraform initialisieren
	if err := run(root, tfCommand, []string{"-chdir=" + infraDirectory, "init", "-input=false"}, tfCommand+" init", false, nil, nil); err != nil {
		return nil, err
	}

	// Terraform apply mit Projektvariablen und Token-Map
	applyArgs := []string{
		"-chdir=" + infraDirectory,
		"apply",
		"-input=false",
		"-auto-approve",
		"-var=project_name=" + projectName,
		"-var=aws_region=" + region,
	}
	if strings.TrimSpace(profile) != "" {
		applyArgs = append(applyArgs, "-var=aws_profile="+profile)
	}

	if err := run(root, tfCommand, applyArgs, fmt.Sprintf("%s apply (%s, %s, QR-Tokens verborgen)", tfCommand, projectName, region), false, nil, []string{"TF_VAR_token_map_base64=" + tokenMapBase64}); err != nil {
		return nil, err
	}

	// Terraform-Ausgaben lesen
	var outputBuffer bytes.Buffer
	if err := run(root, tfCommand, []string{"-chdir=" + infraDirectory, "output", "-json"}, tfCommand+"-Ausgaben lesen", true, &outputBuffer, nil); err != nil {
		return nil, err
	}

	outputs := map[string]terraformOutput{}
	if err := json.Unmarshal(outputBuffer.Bytes(), &outputs); err != nil {
		return nil, err
	}

	// Erforderliche Ausgaben validieren
	required := []string{"website_url", "website_bucket_name", "photo_bucket_name", "distribution_id"}
	for _, key := range required {
		if outputs[key].Value == "" {
			return nil, fmt.Errorf("Terraform-Ausgabe %s fehlt", key)
		}
	}

	// Ausgaben in ein einfaches String-Map umwandeln
	result := make(map[string]string)
	for k, v := range outputs {
		result[k] = v.Value
	}

	return result, nil
}

// uploadAndPublishFrontend schreibt die Frontend-Config, lädt Dateien nach S3 und invalidiert CloudFront.
func uploadAndPublishFrontend(root string, outputs map[string]string, profile, region string, configuration appConfig) error {
	// Frontend-Konfiguration mit Titel/Intro schreiben
	if err := writeFrontendConfig(root, configuration); err != nil {
		return err
	}

	profileArgs := []string{}
	if profile != "" {
		profileArgs = append(profileArgs, "--profile", profile)
	}

	// Frontend-Dateien nach S3 hochladen
	syncArgs := []string{"s3", "sync", filepath.Join(root, "frontend"), "s3://" + outputs["website_bucket_name"]}
	syncArgs = append(syncArgs, profileArgs...)
	syncArgs = append(syncArgs, "--region", region, "--delete", "--cache-control", "public,max-age=300")
	if err := run(root, "aws", syncArgs, "Webseite nach S3 kopieren", false, nil, nil); err != nil {
		return err
	}

	// CloudFront-Cache invalidieren (erzwingt Neuladung beim nächsten Request)
	invalidationArgs := []string{"cloudfront", "create-invalidation", "--distribution-id", outputs["distribution_id"]}
	invalidationArgs = append(invalidationArgs, profileArgs...)
	invalidationArgs = append(invalidationArgs, "--paths", "/*")
	return run(root, "aws", invalidationArgs, "CloudFront-Cache leeren", false, nil, nil)
}

// generateAndOutputQRCodes erzeugt QR-Codes (PNG) und eine CSV mit Upload-Links für alle Tische.
func generateAndOutputQRCodes(root, websiteURL string, tables []table) error {
	return generateQRCodes(root, websiteURL, tables)
}

func buildLambda(root string) error {
	outputDirectory := filepath.Join(root, ".build")
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return err
	}
	arguments := []string{
		"build", "-buildvcs=false", "-tags", "lambda.norpc", "-trimpath", "-ldflags=-s -w",
		"-o", filepath.Join(outputDirectory, "bootstrap"), "./cmd/lambda",
	}
	extraEnvironment := []string{"CGO_ENABLED=0", "GOOS=linux", "GOARCH=arm64"}
	return run(root, "go", arguments, "Go-Lambda für Linux/ARM64 bauen", false, nil, extraEnvironment)
}

func packageLambda(root string) error {
	input, err := os.Open(filepath.Join(root, ".build", "bootstrap"))
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.Create(filepath.Join(root, ".build", "lambda.zip"))
	if err != nil {
		return err
	}
	zipWriter := zip.NewWriter(output)
	header := &zip.FileHeader{Name: "bootstrap", Method: zip.Deflate}
	header.SetMode(0o755)
	entry, err := zipWriter.CreateHeader(header)
	if err == nil {
		_, err = io.Copy(entry, input)
	}
	if closeErr := zipWriter.Close(); err == nil {
		err = closeErr
	}
	if closeErr := output.Close(); err == nil {
		err = closeErr
	}
	return err
}

func findProjectRoot() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", errors.New("go.mod nicht gefunden; Befehl im Projektordner ausführen")
		}
		directory = parent
	}
}

func readConfig(path string) (appConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return appConfig{}, err
	}
	var configuration appConfig
	if err := json.Unmarshal(data, &configuration); err != nil {
		return appConfig{}, err
	}
	if len(configuration.Tables) == 0 {
		return appConfig{}, errors.New("config/tables.json muss mindestens einen Tisch enthalten")
	}
	return configuration, nil
}

func prepareTables(configuration *appConfig) error {
	usedTokens := map[string]bool{}
	usedSlugs := map[string]bool{}
	for index := range configuration.Tables {
		table := &configuration.Tables[index]
		if strings.TrimSpace(table.Name) == "" {
			return fmt.Errorf("bei Tisch %d fehlt der Name", index+1)
		}
		table.Slug = slugify(firstNonEmpty(table.Slug, table.Name))
		if table.Token == "" {
			tokenBytes := make([]byte, 18)
			if _, err := rand.Read(tokenBytes); err != nil {
				return err
			}
			table.Token = base64.RawURLEncoding.EncodeToString(tokenBytes)
		}
		if table.Slug == "" || usedSlugs[table.Slug] {
			return fmt.Errorf("ungültiger oder doppelter Slug: %s", table.Slug)
		}
		if len(table.Token) < 12 || usedTokens[table.Token] {
			return fmt.Errorf("ungültiger oder doppelter Token bei %s", table.Name)
		}
		for _, character := range table.Token {
			if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-') {
				return fmt.Errorf("Token bei %s enthält ungültige Zeichen", table.Name)
			}
		}
		usedSlugs[table.Slug] = true
		usedTokens[table.Token] = true
	}
	return nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func writeFrontendConfig(root string, configuration appConfig) error {
	frontendConfig := map[string]string{
		"title": firstNonEmpty(configuration.Title, "Unsere Hochzeit"),
		"intro": firstNonEmpty(configuration.Intro, "Teile deine schönsten Fotos und Videos mit uns."),
	}
	data, err := json.MarshalIndent(frontendConfig, "", "  ")
	if err != nil {
		return err
	}
	prefix := []byte("window.WEDDING_CONFIG = ")
	return os.WriteFile(filepath.Join(root, "frontend", "config.js"), append(append(prefix, data...), []byte(";\n")...), 0o644)
}

func generateQRCodes(root, websiteURL string, tables []table) error {
	qrDirectory := filepath.Join(root, "output", "qr")
	if err := os.MkdirAll(qrDirectory, 0o755); err != nil {
		return err
	}
	csvFile, err := os.Create(filepath.Join(root, "output", "links.csv"))
	if err != nil {
		return err
	}
	defer csvFile.Close()
	writer := csv.NewWriter(csvFile)
	if err := writer.Write([]string{"Tisch", "Ordner", "Upload-URL"}); err != nil {
		return err
	}
	for _, table := range tables {
		link, err := url.Parse(websiteURL)
		if err != nil {
			return err
		}
		query := link.Query()
		query.Set("t", table.Token)
		query.Set("n", table.Name)
		link.RawQuery = query.Encode()
		code, err := qrcode.New(link.String(), qrcode.High)
		if err != nil {
			return err
		}
		code.ForegroundColor = color.RGBA{R: 0x2f, G: 0x2a, B: 0x25, A: 0xff}
		code.BackgroundColor = color.White
		if err := code.WriteFile(1000, filepath.Join(qrDirectory, table.Slug+".png")); err != nil {
			return err
		}
		if err := writer.Write([]string{table.Name, table.Slug, link.String()}); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func run(directory, command string, arguments []string, display string, capture bool, output *bytes.Buffer, extraEnvironment []string) error {
	fmt.Printf("\n› %s\n", display)
	cmd := exec.Command(command, arguments...)
	cmd.Dir = directory
	cmd.Env = mergedEnvironment(extraEnvironment)
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	if capture {
		cmd.Stdout = output
	} else {
		cmd.Stdout = os.Stdout
	}
	return cmd.Run()
}

var executableLookup = exec.LookPath

func resolveTerraformCommand() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("TF_BIN")); configured != "" {
		return configured, nil
	}
	for _, candidate := range []string{"terraform", "tofu"} {
		if _, err := executableLookup(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", errors.New("weder \"terraform\" noch \"tofu\" im PATH gefunden; installiere Terraform oder OpenTofu oder setze TF_BIN")
}

func validateAWSConfiguration(profile string) error {
	if strings.TrimSpace(profile) == "" {
		return nil
	}
	if hasAWSEnvironmentCredentials() {
		return fmt.Errorf("-profile %q kollidiert mit bereits gesetzten AWS-Umgebungsvariablen. Wenn du awsume oder exportierte AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY nutzt, rufe den Befehl ohne -profile auf", profile)
	}
	return nil
}

func hasAWSEnvironmentCredentials() bool {
	return strings.TrimSpace(os.Getenv("AWS_ACCESS_KEY_ID")) != "" && strings.TrimSpace(os.Getenv("AWS_SECRET_ACCESS_KEY")) != ""
}

func mergedEnvironment(extra []string) []string {
	overrides := make(map[string]bool, len(extra))
	for _, item := range extra {
		name, _, found := strings.Cut(item, "=")
		if found {
			overrides[name] = true
		}
	}
	environment := make([]string, 0, len(os.Environ())+len(extra))
	for _, item := range os.Environ() {
		name, _, found := strings.Cut(item, "=")
		if !found || !overrides[name] {
			environment = append(environment, item)
		}
	}
	return append(environment, extra...)
}

func slugify(value string) string {
	value = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss").Replace(strings.ToLower(value))
	var result strings.Builder
	lastDash := false
	for _, character := range value {
		valid := character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
		if valid {
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
	return strings.Trim(result.String(), "-")
}

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func fatalIf(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		os.Exit(1)
	}
}
