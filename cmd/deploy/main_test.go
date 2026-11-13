package main

import (
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareTablesAddsStableShapeTokens(t *testing.T) {
	configuration := appConfig{Tables: []table{{Name: "Gäste & Freunde"}}}
	if err := prepareTables(&configuration); err != nil {
		t.Fatal(err)
	}
	generated := configuration.Tables[0]
	if generated.Slug != "gaeste-freunde" {
		t.Fatalf("Slug = %q", generated.Slug)
	}
	if len(generated.Token) != 24 {
		t.Fatalf("Token-Länge = %d", len(generated.Token))
	}
}

func TestGenerateQRCodesWritesPNGAndCSV(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "output"), 0o755); err != nil {
		t.Fatal(err)
	}
	tables := []table{{Name: "Tisch 1", Slug: "tisch-01", Token: "0123456789abcdef"}}
	if err := generateQRCodes(root, "https://example.cloudfront.net", tables); err != nil {
		t.Fatal(err)
	}

	pngFile, err := os.Open(filepath.Join(root, "output", "qr", "tisch-01.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer pngFile.Close()
	image, err := png.Decode(pngFile)
	if err != nil {
		t.Fatal(err)
	}
	if image.Bounds().Dx() != 1000 || image.Bounds().Dy() != 1000 {
		t.Fatalf("QR-Größe = %dx%d", image.Bounds().Dx(), image.Bounds().Dy())
	}

	if _, err := os.Stat(filepath.Join(root, "output", "links.csv")); err != nil {
		t.Fatal(err)
	}
}

func TestMergedEnvironmentOverridesExistingValue(t *testing.T) {
	t.Setenv("WEDDING_UPLOAD_TEST", "old")
	environment := mergedEnvironment([]string{"WEDDING_UPLOAD_TEST=new"})
	values := []string{}
	for _, item := range environment {
		if strings.HasPrefix(item, "WEDDING_UPLOAD_TEST=") {
			values = append(values, item)
		}
	}
	if len(values) != 1 || values[0] != "WEDDING_UPLOAD_TEST=new" {
		t.Fatalf("Umgebungsvariable nicht eindeutig überschrieben: %v", values)
	}
}

func TestResolveTerraformCommandHonorsTFBIN(t *testing.T) {
	t.Setenv("TF_BIN", "custom-terraform")
	lookupCalled := false
	original := executableLookup
	executableLookup = func(file string) (string, error) {
		lookupCalled = true
		return "", errors.New("should not be called")
	}
	t.Cleanup(func() { executableLookup = original })

	command, err := resolveTerraformCommand()
	if err != nil {
		t.Fatal(err)
	}
	if command != "custom-terraform" {
		t.Fatalf("Befehl = %q", command)
	}
	if lookupCalled {
		t.Fatal("LookPath wurde trotz TF_BIN aufgerufen")
	}
}

func TestResolveTerraformCommandFallsBackToTofu(t *testing.T) {
	t.Setenv("TF_BIN", "")
	original := executableLookup
	executableLookup = func(file string) (string, error) {
		switch file {
		case "terraform":
			return "", errors.New("not found")
		case "tofu":
			return "/opt/homebrew/bin/tofu", nil
		default:
			return "", errors.New("unexpected lookup")
		}
	}
	t.Cleanup(func() { executableLookup = original })

	command, err := resolveTerraformCommand()
	if err != nil {
		t.Fatal(err)
	}
	if command != "tofu" {
		t.Fatalf("Befehl = %q", command)
	}
}

func TestResolveTerraformCommandReturnsHelpfulError(t *testing.T) {
	t.Setenv("TF_BIN", "")
	original := executableLookup
	executableLookup = func(file string) (string, error) {
		return "", errors.New("not found")
	}
	t.Cleanup(func() { executableLookup = original })

	_, err := resolveTerraformCommand()
	if err == nil {
		t.Fatal("Fehler erwartet")
	}
	if !strings.Contains(err.Error(), "tofu") || !strings.Contains(err.Error(), "TF_BIN") {
		t.Fatalf("unerwartete Fehlermeldung: %v", err)
	}
}

func TestValidateAWSConfigurationRejectsProfileWithEnvironmentCredentials(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")

	err := validateAWSConfiguration("tefde-sandbox-confuser")
	if err == nil {
		t.Fatal("Fehler erwartet")
	}
	if !strings.Contains(err.Error(), "awsume") || !strings.Contains(err.Error(), "ohne -profile") {
		t.Fatalf("unerwartete Fehlermeldung: %v", err)
	}
}

func TestValidateAWSConfigurationAllowsProfileWithoutEnvironmentCredentials(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")

	if err := validateAWSConfiguration("tefde-sandbox-confuser"); err != nil {
		t.Fatal(err)
	}
}

func TestHasAWSEnvironmentCredentials(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
	if !hasAWSEnvironmentCredentials() {
		t.Fatal("AWS-Umgebungsvariablen wurden nicht erkannt")
	}

	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	if hasAWSEnvironmentCredentials() {
		t.Fatal("unvollständige AWS-Umgebungsvariablen wurden fälschlich erkannt")
	}
}
