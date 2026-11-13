variable "project_name" {
  description = "photoapp"
  type        = string
  default     = "hochzeitsfotos-tf"

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{2,23}$", var.project_name))
    error_message = "project_name muss mit einem Kleinbuchstaben beginnen, 3 bis 24 Zeichen lang sein und darf nur a-z, 0-9 und Bindestriche enthalten."
  }
}

variable "aws_region" {
  description = "AWS-Region für S3, Lambda und API Gateway."
  type        = string
  default     = "eu-central-1"
}

variable "aws_profile" {
  description = "Optionales lokales AWS-Profil. Leer verwendet die normale AWS-Credential-Chain."
  type        = string
  default     = ""
}

variable "token_map_base64" {
  description = "Base64-codierte Zuordnung der geheimen QR-Tokens zu Tischordnern."
  type        = string
  sensitive   = true

  validation {
    condition     = can(base64decode(var.token_map_base64))
    error_message = "token_map_base64 muss gültiges Base64 enthalten."
  }
}
