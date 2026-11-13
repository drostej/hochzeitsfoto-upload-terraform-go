# Hochzeitsfoto-Upload mit Go, Terraform und AWS

Eine kleine mobile Webseite für Hochzeitsgäste: QR-Code scannen, Fotos oder Videos auswählen und direkt in einen privaten Amazon-S3-Bucket hochladen. Die Gäste benötigen weder ein AWS-Konto noch eine App.

Terraform erstellt die komplette Infrastruktur:

- privaten S3-Bucket für Fotos und Videos
- privaten S3-Bucket für die Webseite
- CloudFront-Verteilung mit HTTPS und Origin Access Control
- API Gateway unter derselben Domain (`/api/upload-url`)
- Go-Lambda für kurz gültige S3-Upload-URLs
- IAM-Rolle, Bucket-Policies, Verschlüsselung und Log-Aufbewahrung

Das Go-Deploymentprogramm baut die Lambda, ruft Terraform auf, lädt die Webseite hoch und erzeugt für jeden Tisch einen druckbaren QR-Code.

## Voraussetzungen

- AWS Account und lokal konfigurierte AWS-Zugangsdaten
- [AWS CLI v2](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html)
- [Terraform ab Version 1.10](https://developer.hashicorp.com/terraform/install) oder [OpenTofu](https://opentofu.org/)
- [Go ab Version 1.22](https://go.dev/doc/install)

Prüfe die Anmeldung beispielsweise mit:

```bash
aws sts get-caller-identity
```

## 1. Hochzeit und Tische anpassen

Bearbeite `config/tables.json`:

```json
{
  "title": "Unsere Hochzeit",
  "intro": "Schön, dass du mit uns feierst! Teile deine schönsten Fotos und Videos vom heutigen Tag.",
  "tables": [
    { "name": "Tisch 1", "slug": "tisch-01" },
    { "name": "Tisch 2", "slug": "tisch-02" }
  ]
}
```

Beim ersten Lauf erzeugt das Programm für jeden Gast einen zufälligen Token und schreibt ihn dauerhaft in diese Datei. Bereits vorhandene Tokens bleiben erhalten, damit gedruckte QR-Codes weiterhin funktionieren.

Nach dem ersten Lauf enthält `config/tables.json` damit ebenfalls Zugangstokens und sollte nicht in ein öffentliches Repository eingecheckt werden.

## 2. Deployment

Im Projektordner:

```bash
go mod download
go run -buildvcs=false ./cmd/deploy \
  -project hochzeitsfotos-tf \
  -region eu-central-1
```

Bei einem benannten AWS-Profil:

```bash
go run -buildvcs=false ./cmd/deploy \
  -project hochzeitsfotos-tf \
  -region eu-central-1 \
  -profile mein-aws-profil
```

Wenn du dich bereits mit `awsume` angemeldet hast oder `AWS_ACCESS_KEY_ID` und `AWS_SECRET_ACCESS_KEY` schon als Umgebungsvariablen gesetzt sind, **verwende kein** `-profile`. In diesem Fall nutzt das Deploymentprogramm automatisch die normale AWS-Credential-Chain:

```bash
go run -buildvcs=false ./cmd/deploy \
  -project hochzeitsfotos-tf \
  -region eu-central-1
```

Das Deploymentprogramm verwendet automatisch `terraform`, wenn es im `PATH` gefunden wird, andernfalls `tofu`. Falls du ein bestimmtes Binärprogramm erzwingen möchtest, kannst du `TF_BIN` setzen:

```bash
TF_BIN=tofu go run -buildvcs=false ./cmd/deploy \
  -project hochzeitsfotos-tf \
  -region eu-central-1
```

CloudFront benötigt beim ersten Anlegen üblicherweise einige Minuten. Danach zeigt das Programm die fertige HTTPS-Adresse und den Namen des Foto-Buckets an.

Es entstehen außerdem:

- `output/qr/*.png`: QR-Codes mit 1000 × 1000 Pixeln
- `output/links.csv`: Tischname, S3-Ordner und vollständiger Upload-Link

Diese erzeugten Dateien enthalten die geheimen Gast-Tokens. Gib die Links und QR-Codes nur an eure Gäste weiter.

## Typische Befehle im Alltag

### Tests ausführen

Alle Go-Tests im Projekt:

```bash
go test ./...
```

Nur die Deploy-Tests:

```bash
go test ./cmd/deploy -v
```

Nur die Lambda-Tests:

```bash
go test ./cmd/lambda -v
```

### Deployment starten

Normales Deployment:

```bash
go run -buildvcs=false ./cmd/deploy \
  -project hochzeitsfotos-tf \
  -region eu-central-1
```

Mit AWS-Profil aus `~/.aws/config` oder `~/.aws/credentials`:

```bash
go run -buildvcs=false ./cmd/deploy \
  -project hochzeitsfotos-tf \
  -region eu-central-1 \
  -profile mein-aws-profil
```

Mit OpenTofu statt Terraform:

```bash
TF_BIN=tofu go run -buildvcs=false ./cmd/deploy \
  -project hochzeitsfotos-tf \
  -region eu-central-1
```

### Nur vorbereiten, noch nichts deployen

Damit werden Tokens aktualisiert, Tests ausgeführt und das Lambda-ZIP gebaut, aber noch kein `terraform apply` gestartet:

```bash
go run -buildvcs=false ./cmd/deploy -prepare-only
```

### Terraform oder OpenTofu direkt verwenden

Konfiguration validieren:

```bash
terraform -chdir=infra validate
```

Mit OpenTofu entsprechend:

```bash
tofu -chdir=infra validate
```

Initialisieren:

```bash
terraform -chdir=infra init
```

Plan anzeigen:

```bash
terraform -chdir=infra plan \
  -var=project_name=hochzeitsfotos-tf \
  -var=aws_region=eu-central-1 \
  -var=token_map_base64=...
```

Den Wert für `token_map_base64` erzeugt normalerweise das Deploymentprogramm automatisch. Für manuelle Terraform- oder OpenTofu-Aufrufe ist daher meist zuerst `go run -buildvcs=false ./cmd/deploy -prepare-only` sinnvoll.

Outputs lesen:

```bash
terraform -chdir=infra output -json
```

Wenn du bereits mit `awsume` angemeldet bist oder `AWS_ACCESS_KEY_ID` und `AWS_SECRET_ACCESS_KEY` gesetzt sind, verwende dabei kein `-profile` und keine `-var=aws_profile=...`-Angabe.

### Fotos aus S3 herunterladen

Alle Fotos herunterladen:

```bash
aws s3 sync s3://MEIN-FOTO-BUCKET ./hochzeitsfotos
```

Nur einen Tisch herunterladen:

```bash
aws s3 sync s3://MEIN-FOTO-BUCKET/uploads/tisch-01 ./hochzeitsfotos/tisch-01
```

### Häufige Stolperfallen

- `config/tables.json` enthält nach dem ersten Lauf geheime Gast-Tokens und gehört nicht in ein öffentliches Repository.
- `output/links.csv` und `output/qr/*.png` enthalten ebenfalls geheime Tokens.
- Ein lokaler Terraform-State speichert sensible Daten im Klartext.
- SAM wird in diesem Projekt nicht verwendet.

## Was das Deploymentprogramm erledigt

1. QR-Tokens prüfen oder neu erzeugen
2. Go-Tests ausführen
3. die Lambda für Linux/ARM64 bauen und als `.build/lambda.zip` paketieren
4. `terraform init` und `terraform apply` ausführen
5. die Webseite in den privaten S3-Bucket kopieren
6. den CloudFront-Cache invalidieren
7. QR-Codes und CSV-Datei erzeugen

SAM ist dafür nicht erforderlich.

## Terraform separat verwenden

Um nur das Lambda-Paket und die Tokens vorzubereiten:

```bash
go run -buildvcs=false ./cmd/deploy -prepare-only
```

Danach kannst du Terraform oder OpenTofu auch direkt aufrufen. Die Token-Zuordnung muss als Base64-codierte JSON-Map an `token_map_base64` übergeben werden; das Deploymentprogramm übernimmt diesen Schritt normalerweise automatisch.

Die wichtigsten Dateien:

- `infra/main.tf`: gemeinsame `locals`
- `infra/storage.tf`: S3-Buckets und Bucket-Policies
- `infra/compute.tf`: Lambda, IAM und API Gateway
- `infra/cdn.tf`: CloudFront-Verteilung und Cache-Policies
- `infra/variables.tf`: Eingaben und Validierungen
- `infra/outputs.tf`: URL, Buckets und CloudFront-ID
- `infra/backend.tf.example`: Beispiel für einen entfernten S3-State

## Terraform-State schützen

Obwohl `token_map_base64` als `sensitive` markiert ist, speichert Terraform den Wert im State. Ein lokaler State liegt im Klartext auf der Festplatte. Für einen dauerhaften Einsatz solltest du daher einen privaten, verschlüsselten S3-State-Bucket verwenden:

1. State-Bucket einmalig außerhalb dieses Projekts erstellen.
2. `infra/backend.tf.example` nach `infra/backend.tf` kopieren.
3. Bucketname, Key und Region anpassen.
4. `terraform -chdir=infra init -migrate-state` ausführen.

Der Beispiel-Backendblock aktiviert die S3-basierte State-Sperre mit `use_lockfile = true`.

## Fotos herunterladen

Den Bucketnamen gibt das Deployment aus. Danach etwa:

```bash
aws s3 sync s3://MEIN-FOTO-BUCKET ./hochzeitsfotos
```

Die Ablage ist nach Tisch und Datum gegliedert:

```text
uploads/
  familie/
    2026-09-14/
      2b5b3fb1-....jpg
```

Originaldateinamen werden nicht gespeichert. So gelangen keine problematischen Dateipfade in den Bucket und gleichnamige Bilder überschreiben sich nicht.

## Grenzen und Schutzmaßnahmen

- erlaubte Formate: JPG/JPEG, PNG, WebP, HEIC/HEIF, MP4 und MOV
- höchstens 20 Dateien pro Vorgang
- höchstens 100 MB pro Datei
- Upload-URLs sind fünf Minuten gültig
- zufällige Objektnamen statt Originaldateinamen
- keine öffentliche Leseberechtigung auf den Buckets
- AWS-seitige Verschlüsselung und HTTPS-Pflicht
- API-Gateway-Drosselung gegen versehentliche Lastspitzen

Ein QR-Token ist keine Benutzeranmeldung. Wer den Link kennt, kann innerhalb der Veranstaltungszeit Upload-URLs anfordern. Nach der Feier kannst du die App entfernen oder die Tokens austauschen.

## Änderungen erneut ausrollen

Einfach denselben Deploymentbefehl erneut ausführen. Terraform aktualisiert nur die notwendigen Ressourcen. Änderungen am Go-Code lösen durch den Hash des Lambda-ZIP-Dateipakets ebenfalls ein Lambda-Update aus.

## Infrastruktur entfernen

Die beiden Buckets haben absichtlich `force_destroy = false`, damit Fotos oder Webdateien nicht versehentlich gelöscht werden. Sichere zuerst die Bilder und leere anschließend beide Buckets. Danach:

```bash
TF_VAR_token_map_base64=e30= terraform -chdir=infra destroy \
  -var=project_name=hochzeitsfotos-tf \
  -var=aws_region=eu-central-1
```

Mit OpenTofu funktioniert derselbe Befehl entsprechend mit `tofu` statt `terraform`.

Verwende dabei denselben Projektname und dieselbe Region wie beim Deployment; ergänze bei Bedarf `-var=aws_profile=mein-aws-profil`. Der Dummy-Wert `e30=` entspricht einer leeren JSON-Map und wird nur benötigt, damit Terraform bzw. OpenTofu beim Zerstören die Pflichtvariable einlesen kann. Das Tool fragt vor dem Löschen nach einer Bestätigung.
