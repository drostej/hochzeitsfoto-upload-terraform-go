output "website_url" {
  description = "Oeffentliche HTTPS-Adresse der Upload-Seite."
  value       = "https://${aws_cloudfront_distribution.website.domain_name}"
}

output "website_bucket_name" {
  description = "Privater S3-Bucket mit den Dateien der Webseite."
  value       = aws_s3_bucket.website.id
}

output "photo_bucket_name" {
  description = "Privater S3-Bucket mit den hochgeladenen Fotos und Videos."
  value       = aws_s3_bucket.photos.id
}

output "distribution_id" {
  description = "CloudFront-ID fuer Cache-Invalidierungen."
  value       = aws_cloudfront_distribution.website.id
}
