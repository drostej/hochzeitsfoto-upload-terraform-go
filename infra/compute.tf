# Lambda IAM Role und Policy

data "aws_iam_policy_document" "lambda_assume_role" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "lambda" {
  name_prefix        = "${var.project_name}-lambda-"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume_role.json
}

data "aws_iam_policy_document" "lambda" {
  statement {
    sid     = "UploadPhotos"
    effect  = "Allow"
    actions = ["s3:PutObject"]
    resources = [
      "${aws_s3_bucket.photos.arn}/uploads/*",
    ]
  }

  statement {
    sid     = "WriteLogs"
    effect  = "Allow"
    actions = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = [
      "arn:${data.aws_partition.current.partition}:logs:${var.aws_region}:${data.aws_caller_identity.current.account_id}:log-group:/aws/lambda/${var.project_name}-upload:*",
    ]
  }
}

resource "aws_iam_role_policy" "lambda" {
  name_prefix = "${var.project_name}-lambda-"
  role        = aws_iam_role.lambda.id
  policy      = data.aws_iam_policy_document.lambda.json
}

resource "aws_cloudwatch_log_group" "lambda" {
  name              = "/aws/lambda/${var.project_name}-upload"
  retention_in_days = 14
}

# Lambda Function

resource "aws_lambda_function" "upload" {
  function_name    = "${var.project_name}-upload"
  description      = "Erzeugt kurz gueltige S3-Upload-URLs fuer Hochzeitsgaeste."
  filename         = local.lambda_zip_path
  source_code_hash = filebase64sha256(local.lambda_zip_path)
  role             = aws_iam_role.lambda.arn
  handler          = "bootstrap"
  runtime          = "provided.al2023"
  architectures    = ["arm64"]
  memory_size      = 128
  timeout          = 8

  environment {
    variables = {
      AWS_REQUEST_CHECKSUM_CALCULATION = "WHEN_REQUIRED"
      MAX_FILE_SIZE                    = "104857600"
      PHOTO_BUCKET                     = aws_s3_bucket.photos.id
      TOKEN_MAP_B64                    = var.token_map_base64
    }
  }

  depends_on = [
    aws_cloudwatch_log_group.lambda,
    aws_iam_role_policy.lambda,
  ]
}

# API Gateway

resource "aws_api_gateway_rest_api" "upload" {
  name        = "${var.project_name}-upload"
  description = "Presigned-URL-Endpunkt fuer Hochzeitsfotos"

  endpoint_configuration {
    types = ["REGIONAL"]
  }
}

resource "aws_api_gateway_resource" "api" {
  rest_api_id = aws_api_gateway_rest_api.upload.id
  parent_id   = aws_api_gateway_rest_api.upload.root_resource_id
  path_part   = "api"
}

resource "aws_api_gateway_resource" "upload_url" {
  rest_api_id = aws_api_gateway_rest_api.upload.id
  parent_id   = aws_api_gateway_resource.api.id
  path_part   = "upload-url"
}

resource "aws_api_gateway_method" "upload_url_post" {
  rest_api_id   = aws_api_gateway_rest_api.upload.id
  resource_id   = aws_api_gateway_resource.upload_url.id
  http_method   = "POST"
  authorization = "NONE"
}

resource "aws_api_gateway_integration" "upload_url_post" {
  rest_api_id             = aws_api_gateway_rest_api.upload.id
  resource_id             = aws_api_gateway_resource.upload_url.id
  http_method             = aws_api_gateway_method.upload_url_post.http_method
  integration_http_method = "POST"
  type                    = "AWS_PROXY"
  uri                     = aws_lambda_function.upload.invoke_arn
}

resource "aws_api_gateway_deployment" "upload" {
  rest_api_id = aws_api_gateway_rest_api.upload.id

  triggers = {
    redeployment = sha1(jsonencode([
      aws_api_gateway_resource.api.id,
      aws_api_gateway_resource.upload_url.id,
      aws_api_gateway_method.upload_url_post.id,
      aws_api_gateway_integration.upload_url_post.id,
    ]))
  }

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_api_gateway_stage" "prod" {
  deployment_id = aws_api_gateway_deployment.upload.id
  rest_api_id   = aws_api_gateway_rest_api.upload.id
  stage_name    = "Prod"
}

resource "aws_api_gateway_method_settings" "all" {
  rest_api_id = aws_api_gateway_rest_api.upload.id
  stage_name  = aws_api_gateway_stage.prod.stage_name
  method_path = "*/*"

  settings {
    throttling_burst_limit = 25
    throttling_rate_limit  = 50
  }
}

resource "aws_lambda_permission" "api_gateway" {
  statement_id  = "AllowApiGatewayInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.upload.function_name
  principal     = "apigateway.amazonaws.com"
  source_arn    = "${aws_api_gateway_rest_api.upload.execution_arn}/*/POST/api/upload-url"
}

