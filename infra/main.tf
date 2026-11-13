locals {
  lambda_zip_path = abspath("${path.module}/../.build/lambda.zip")
  api_origin_name = "${aws_api_gateway_rest_api.upload.id}.execute-api.${var.aws_region}.${data.aws_partition.current.dns_suffix}"
}

